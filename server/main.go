package main

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"NanoKVM-Server/common"
	"NanoKVM-Server/config"
	"NanoKVM-Server/internal/oomscore"
	"NanoKVM-Server/logger"
	"NanoKVM-Server/logs"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/router"
	"NanoKVM-Server/service/extensions/acme"
	"NanoKVM-Server/service/failsafe"
	"NanoKVM-Server/service/network"
	"NanoKVM-Server/service/vm"
	"NanoKVM-Server/service/vm/jiggler"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	cors "github.com/rs/cors/wrapper/gin"
	"github.com/sirupsen/logrus"
)

func main() {
	ctx, stopMemory := context.WithCancel(context.Background())
	defer stopMemory()
	initialize(stopMemory)
	startDiagnosticCPUProfile()
	go vm.RunMemoryMaintenance(ctx)
	go logs.RunArchive(ctx)
	go acme.RunRenewal(ctx)
	defer func() { stopMemory(); dispose() }()

	run()
}

func initialize(stopMemory context.CancelFunc) {
	if err := config.EnsurePicoclawInternalToken(); err != nil {
		log.Fatalf("failed to initialize picoclaw internal token: %v", err)
	}

	logger.Init()
	// The native video libraries report failures on stderr, which the service
	// discards; standard log output already reaches the application log.
	if err := common.CaptureNativeStderr(logs.Application); err != nil {
		logrus.Warnf("native stderr is not logged: %v", err)
	} else {
		log.SetOutput(logs.Application)
	}
	recovering, recoveryErr := failsafe.Init(func() error {
		err := execSelf(os.Environ())
		logrus.Errorf("video recovery restart failed: %v", err)
		return err
	})
	if recoveryErr != nil {
		logrus.Errorf("video crash guard: %v", recoveryErr)
	}
	if recovering {
		common.DisableVideo()
		logrus.Warn("video recovery active; management remains available")
	}
	utils.InitGoMemLimit()
	// The server owns video capture; let the OOM killer take other processes
	// first. Set here rather than by the init script so that every way of
	// starting the server gets it.
	if err := oomscore.Set(0, oomscore.Critical); err != nil {
		log.Printf("failed to set OOM score adjustment: %v", err)
	}
	vm.ApplySavedCPUFrequency()
	if err := network.InitializeIPv6(); err != nil {
		log.Printf("failed to initialize IPv6 policy: %v", err)
	}
	if err := network.EnsureDHCPIntegration(); err != nil {
		log.Printf("failed to set up the DHCP DNS and gateway hooks: %v", err)
	}

	if !recovering {
		if err := failsafe.VideoPhase("initializing"); err != nil {
			logrus.Warnf("video phase: %v", err)
		}
	}
	// init screen parameters
	_ = common.GetScreen()

	// init HDMI
	if recovering || utils.IsHdmiDisabled() {
		vm.DisableHdmiCapture()
	} else {
		vm.EnableHdmiCapture()
	}
	vm.SetHdmiViewerCount(0)
	if !recovering {
		if err := failsafe.VideoPhase("ready"); err != nil {
			logrus.Warnf("video phase: %v", err)
		}
	}

	// The monitor profile is kept in the HDMI receiver across restarts and
	// reinstalls; it must not exceed the video memory of this boot (a smaller
	// mode may have been selected after it was saved), and after an
	// installation it must be the saved one. Programming can take a while.
	go func() {
		if recovering {
			return
		}
		changed, err := common.FitMonitorToVideoMemory()
		if err != nil {
			logrus.Errorf("failed to fit the monitor profile to the video memory: %v", err)
		} else if changed {
			logrus.Info("lowered the saved monitor profile to the video memory of this boot")
			restartAfterMonitorFit()
		}
		// After a fit the profile is recorded and this does nothing.
		if path, err := common.ProgramMonitorProfileOnFirstStart(); err != nil {
			logrus.Errorf("failed to program the monitor profile on the first start: %v", err)
		} else if path != "" {
			logrus.Infof("programmed the monitor profile %s on the first start", path)
		}
	}()

	// run mouse jiggler
	jiggler.GetJiggler().Run()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGQUIT)
	go func() {
		sig := <-sigChan
		log.Printf("\nReceived signal: %v\n", sig)

		stopMemory()
		dispose()
		os.Exit(0)
	}()
}

func run() {
	conf := config.GetInstance()

	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery())
	if conf.Authentication == "disable" {
		r.Use(cors.AllowAll())
	}

	router.Init(r)

	httpAddr := utils.ListenAddr(conf.Host, strconv.Itoa(conf.Port.Http))
	loopbackHTTPAddr := utils.ListenAddr("127.0.0.1", strconv.Itoa(conf.Port.Http))
	needsLoopbackHTTP := utils.NeedsDedicatedLoopbackListener(conf.Host)

	if conf.Proto == "https" {
		cert := conf.Cert.WithDefaults()
		if err := utils.PrepareServerCertificate(cert.Crt, cert.Key); err != nil {
			panic(fmt.Sprintf("prepare HTTPS certificate: %v", err))
		}
		httpsPortStr := strconv.Itoa(conf.Port.Https)

		go func() {
			server := utils.NewHTTPServer(utils.ListenAddr(conf.Host, httpsPortStr), r, os.Getenv("NANOKVM_HTTP2") != "off")
			// The pair comes from ServerCertificate, so an uploaded or renewed
			// certificate applies to new connections without a restart.
			server.TLSConfig = &tls.Config{GetCertificate: utils.ServerCertificate.GetCertificate}
			err := server.ListenAndServeTLS("", "")
			if err != nil {
				panic("start https server failed")
			}
		}()

		if needsLoopbackHTTP {
			go func() {
				if err := middleware.ListenAndServeLoopbackHTTPRedirect(
					loopbackHTTPAddr,
					httpsPortStr,
					r,
					nil,
					"",
					router.LoopbackHTTPAllowedPaths()...,
				); err != nil {
					panic("start loopback http server failed")
				}
			}()
		}

		if err := middleware.ListenAndServeLoopbackHTTPRedirect(
			httpAddr,
			httpsPortStr,
			r,
			acme.ChallengeHandler(),
			acme.ChallengePrefix,
			router.LoopbackHTTPAllowedPaths()...,
		); err != nil {
			panic("start http server failed")
		}
	} else {
		if needsLoopbackHTTP {
			go func() {
				if err := utils.NewHTTPServer(loopbackHTTPAddr, r, true).ListenAndServe(); err != nil {
					panic("start loopback http server failed")
				}
			}()
		}

		if err := utils.NewHTTPServer(httpAddr, r, true).ListenAndServe(); err != nil {
			panic("start http server failed")
		}
	}
}

func dispose() {
	common.GetKvmVision().Close()
	_ = logs.FlushArchives()
}
