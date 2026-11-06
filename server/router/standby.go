package router

import (
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/config"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/standby"
	"NanoKVM-Server/utils"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	log "github.com/sirupsen/logrus"
)

// Installed before the other routes: admission closes atomically against
// finite writes. Streaming sessions are stopped by OpenRC, while background
// transfers and update workers use the additional preparation locks.
func standbyAdmission() gin.HandlerFunc {
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if path == "/api/vm/system/shutdown" || path == "/standby" || strings.HasPrefix(path, "/standby/") || path == "/standby.js" {
			c.Next()
			return
		}
		if c.Request.Method == http.MethodGet && (websocket.IsWebSocketUpgrade(c.Request) || path == "/api/stream/mjpeg") {
			if !standby.Requests.EnterStream() {
				c.AbortWithStatusJSON(409, gin.H{"code": -1, "msg": "NanoKVM shutdown is in progress"})
				return
			}
			c.Next()
			return
		}
		mutating := c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS"
		// These GET handlers perform a radio scan or mount the user disk.
		// Keep their side effects excluded while ordinary status reads continue.
		if path == "/api/network/wifi/scan" || path == "/api/storage/user-disk/files" || path == "/api/storage/user-disk/files/download" {
			mutating = true
		}
		if !standby.Requests.Enter(mutating) {
			c.AbortWithStatusJSON(409, gin.H{"code": -1, "msg": "NanoKVM shutdown is in progress"})
			return
		}
		defer standby.Requests.Leave(mutating)
		c.Next()
	}
}
func standbyRouter(r *gin.Engine) {
	if err := standby.RemoveStale(); err != nil {
		log.Errorf("standby: %v", err)
	}
	var mu sync.RWMutex
	var pending *standby.Prepared
	var committed bool
	handler := standby.Handler(standby.Config{}, standby.NewState(standby.Config{}), func() error { return errors.New("not ready") })
	serve := func(c *gin.Context) { mu.RLock(); h := handler; mu.RUnlock(); h.ServeHTTP(c.Writer, c.Request) }
	r.GET("/standby", serve)
	r.GET("/standby.js", serve)
	r.GET("/standby/status", serve)
	r.POST("/standby/start", serve)
	r.POST("/api/vm/system/shutdown", middleware.RejectCrossOriginWrites(), middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin), func(c *gin.Context) {
		var request struct {
			MagicWake bool   `json:"magicWake"`
			Action    string `json:"action"`
			Token     string `json:"token"`
		}
		var rsp proto.Response
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if err := c.ShouldBindJSON(&request); err != nil {
			rsp.ErrRsp(c, -1, "invalid shutdown request")
			return
		}
		mu.Lock()
		defer mu.Unlock()
		if request.Action == "commit" || request.Action == "cancel" {
			if pending == nil || subtle.ConstantTimeCompare([]byte(request.Token), []byte(pending.Config.Token)) != 1 {
				rsp.ErrRsp(c, -1, "invalid shutdown capability")
				return
			}
			if request.Action == "cancel" {
				if committed {
					rsp.ErrRsp(c, -1, "shutdown already committed")
					return
				}
				pending.Close()
				pending = nil
				standby.Requests.Cancel()
				rsp.OkRsp(c)
				return
			}
			if !committed {
				if err := pending.Arm(); err != nil {
					pending.Close()
					pending = nil
					standby.Requests.Cancel()
					rsp.ErrRsp(c, -1, err.Error())
					return
				}
				committed = true
			}
			rsp.OkRsp(c)
			return
		}
		if request.Action != "prepare" {
			rsp.ErrRsp(c, -1, "invalid shutdown action")
			return
		}
		if err := standby.Requests.Begin(); err != nil {
			rsp.ErrRsp(c, -1, err.Error())
			return
		}
		ok := false
		defer func() {
			if !ok {
				standby.Requests.Cancel()
			}
		}()
		conf := config.GetInstance()
		host := c.Request.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		client, _, clientErr := net.SplitHostPort(c.Request.RemoteAddr)
		if clientErr != nil {
			rsp.ErrRsp(c, -1, "cannot identify the management client")
			return
		}
		cfg := standby.Config{Address: host, ClientAddress: client, Port: conf.Port.Http, MagicWake: request.MagicWake}
		if conf.Proto == "https" {
			// Preserve the transport and the certificate the browser accepted;
			// never fall back to plaintext if staging the served pair fails.
			if c.Request.TLS == nil {
				rsp.ErrRsp(c, -1, "open the direct HTTPS address before shutdown")
				return
			}
			var err error
			cfg.Port = conf.Port.Https
			cfg.Certificate, cfg.PrivateKey, err = servedCertificatePEM()
			if err == nil {
				_, err = tls.X509KeyPair(cfg.Certificate, cfg.PrivateKey)
			}
			if err != nil {
				rsp.ErrRsp(c, -1, "could not stage the current HTTPS certificate")
				return
			}
		}
		prepared, err := standby.Prepare(cfg)
		if err != nil {
			rsp.ErrRsp(c, -1, err.Error())
			return
		}
		state := standby.NewState(prepared.Config)
		handler = standby.Handler(prepared.Config, state, func() error { return errors.New("shutdown is still running") })
		pending = prepared
		time.AfterFunc(time.Minute, func() {
			mu.Lock()
			defer mu.Unlock()
			if pending == prepared && !committed {
				prepared.Close()
				pending = nil
				standby.Requests.Cancel()
				state.Fail(errors.New("shutdown preparation expired; ordinary services remain running"))
			}
		})
		ok = true
		c.Header("Cache-Control", "no-store")
		rsp.OkRspWithData(c, gin.H{"token": prepared.Config.Token, "mac": prepared.Config.MAC})
	})
}

// servedCertificatePEM encodes the pair the HTTPS listener presents, which
// differs from the configured files after a fallback or an unreloaded change.
func servedCertificatePEM() (certPEM, keyPEM []byte, err error) {
	cert := utils.ServerCertificate.Current().Cert
	if cert == nil {
		return nil, nil, errors.New("no served certificate")
	}
	for _, der := range cert.Certificate {
		certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})...)
	}
	key, err := x509.MarshalPKCS8PrivateKey(cert.PrivateKey)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}), nil
}
