package router

import (
	"encoding/base64"
	"errors"
	"net/http"
	"sync"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/osupdate"
	"NanoKVM-Server/recovery"
	"github.com/gin-gonic/gin"
)

// One KDF/decoded archive at a time on a memory-constrained device.
var recoveryMu sync.Mutex

type recoveryRequest struct {
	Password        string           `json:"password"`
	ArchivePassword string           `json:"archivePassword"`
	Archive         []byte           `json:"archive"`
	Options         recovery.Options `json:"options"`
	Token           string           `json:"token"`
}

func recoveryRouter(r *gin.Engine) {
	api := r.Group("/api/system/recovery").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	api.GET("", func(c *gin.Context) {
		updateReply(c, gin.H{"available": recovery.Available(), "status": recovery.Live().Status()}, nil)
	})
	api.POST("/:action", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		if !recoveryMu.TryLock() {
			updateReply(c, nil, errors.New("another recovery request is running"))
			return
		}
		defer recoveryMu.Unlock()
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, (recovery.MaxArchive*4/3)+(64<<10))
		var req recoveryRequest
		if err := c.ShouldBindJSON(&req); err != nil {
			updateReply(c, nil, errors.New("invalid or excessive recovery request"))
			return
		}
		principal, ok := middleware.CurrentPrincipal(c)
		if !ok {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		user, valid, err := authn.DefaultStore.Authenticate(principal.Username, req.Password)
		if err != nil || !valid || user.Role != authn.RoleAdmin {
			updateReply(c, nil, errors.New("current administrator password is required"))
			return
		}
		if c.Param("action") == "reboot" && recovery.Live().Pending() {
			updateReply(c, nil, recovery.Reboot())
			return
		}
		lock, err := osupdate.Lock()
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		defer lock.Close()
		e := recovery.Live()
		if c.Param("action") == "export" {
			a, err := e.Snapshot()
			if err != nil {
				updateReply(c, nil, err)
				return
			}
			data, err := recovery.Seal(a, req.ArchivePassword)
			updateReply(c, gin.H{"archive": base64.StdEncoding.EncodeToString(data)}, err)
			return
		}
		if !recovery.Available() {
			updateReply(c, nil, errors.New("update nanokvm-base before restoring or resetting settings"))
			return
		}
		var archive *recovery.Archive
		if req.Options.Action == "restore" {
			a, err := recovery.Open(req.Archive, req.ArchivePassword)
			if err != nil {
				updateReply(c, nil, err)
				return
			}
			archive = &a
		}
		plan, preview, err := e.Prepare(archive, req.Options)
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		switch c.Param("action") {
		case "preview":
			updateReply(c, preview, nil)
		case "apply":
			if req.Token == "" || req.Token != preview.Token {
				updateReply(c, nil, errors.New("settings or selection changed; preview again"))
				return
			}
			if err = e.Stage(plan); err == nil {
				err = recovery.Reboot()
			}
			updateReply(c, gin.H{"status": e.Status()}, err)
		default:
			c.AbortWithStatus(http.StatusNotFound)
		}
	})
}
