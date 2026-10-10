package main

import (
	"context"
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
	defer func() { stopMemory(); dispose() }()

	run()
}

func initialize(stopMemory context.CancelFunc) {
	if err := config.EnsurePicoclawInternalToken(); err != nil {
		log.Fatalf("failed to initialize picoclaw internal token: %v", err)
	}

	logger.Init()
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

	// init screen parameters
	_ = common.GetScreen()

	// init HDMI
	if utils.IsHdmiDisabled() {
		vm.DisableHdmiCapture()
	} else {
		vm.EnableHdmiCapture()
	}
	vm.SetHdmiViewerCount(0)

	// The monitor profile is kept in the HDMI receiver across restarts; it
	// must not exceed the video memory of this boot (a smaller mode may have
	// been selected after it was saved). Programming can take a while.
	go func() {
		changed, err := common.FitMonitorToVideoMemory()
		if err != nil {
			logrus.Errorf("failed to fit the monitor profile to the video memory: %v", err)
		} else if changed {
			logrus.Info("lowered the saved monitor profile to the video memory of this boot")
			restartAfterMonitorFit()
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
		if err := utils.EnsureServerCertificate(conf.Cert.Crt, conf.Cert.Key); err != nil {
			panic(fmt.Sprintf("prepare HTTPS certificate: %v", err))
		}
		httpsPortStr := strconv.Itoa(conf.Port.Https)

		go func() {
			server := utils.NewHTTPServer(utils.ListenAddr(conf.Host, httpsPortStr), r, os.Getenv("NANOKVM_HTTP2") != "off")
			err := server.ListenAndServeTLS(conf.Cert.Crt, conf.Cert.Key)
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
