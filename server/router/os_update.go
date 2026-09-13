package router

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/osupdate"
	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

func updateReply(c *gin.Context, data any, err error) {
	var rsp proto.Response
	if err != nil {
		rsp.ErrRsp(c, -1, err.Error())
	} else {
		rsp.OkRspWithData(c, data)
	}
}

func prepareWithHelper(lock *os.File, name string) (*osupdate.PreparedReceipt, error) {
	cmd := exec.Command(osupdate.Helper, "prepare-inherited", name)
	cmd.ExtraFiles = []*os.File{lock}
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = err.Error()
		}
		return nil, fmt.Errorf("updater prepare failed: %s", message)
	}
	receipt, err := osupdate.DecodePreparedReceipt(&stdout)
	if err != nil {
		return nil, fmt.Errorf("invalid updater response")
	}
	return receipt, nil
}
func osUpdateRouter(r *gin.Engine) {
	r.GET("/api/os/update/health", func(c *gin.Context) { c.Status(http.StatusOK) })
	api := r.Group("/api/os/update").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	api.GET("", func(c *gin.Context) {
		updateReply(c, gin.H{"installed": osupdate.GetInstalled(), "check": osupdate.GetCheck(), "operation": osupdate.GetResult(), "repository": osupdate.Repository}, nil)
	})
	api.POST("/check", func(c *gin.Context) { osupdate.Check(c.Request.Context()); updateReply(c, osupdate.GetCheck(), nil) })
	api.POST("/upload", func(c *gin.Context) {
		lock, err := osupdate.Lock()
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		defer lock.Close()
		f, err := os.CreateTemp(osupdate.Base, "upload-")
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		name := f.Name()
		defer os.Remove(name)
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, osupdate.MaxBundle)
		_, err = io.Copy(f, c.Request.Body)
		if err == nil {
			err = f.Sync()
		}
		ce := f.Close()
		if err == nil {
			err = ce
		}
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		b, err := prepareWithHelper(lock, name)
		updateReply(c, b, err)
	})
	api.POST("/download", func(c *gin.Context) {
		release := osupdate.GetCheck().Release
		if release == nil {
			updateReply(c, nil, fmt.Errorf("check for an update first"))
			return
		}
		lock, err := osupdate.Lock()
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		osupdate.SetResult(osupdate.Result{State: "downloading", Message: "Downloading signed NanoKVM OS update"})
		go func() {
			defer lock.Close()
			f, err := os.CreateTemp(osupdate.Base, "download-")
			if err != nil {
				osupdate.SetResult(osupdate.Result{State: "failed", Message: err.Error()})
				return
			}
			name := f.Name()
			defer os.Remove(name)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			err = osupdate.Download(ctx, *release, f)
			if err == nil {
				err = f.Sync()
			}
			ce := f.Close()
			if err == nil {
				err = ce
			}
			if err == nil {
				_, err = prepareWithHelper(lock, name)
			}
			if err != nil {
				osupdate.SetResult(osupdate.Result{State: "failed", Message: err.Error()})
			}
		}()
		updateReply(c, gin.H{"state": "downloading"}, nil)
	})
	api.POST("/install", func(c *gin.Context) {
		var req struct {
			ID string `json:"id"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			updateReply(c, nil, err)
			return
		}
		lock, err := osupdate.Lock()
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		defer lock.Close()
		name, err := osupdate.PackagePath(req.ID)
		if err == nil {
			var info os.FileInfo
			info, err = os.Lstat(name)
			if err == nil && (!info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
				err = fmt.Errorf("invalid prepared package")
			}
			var got string
			if err == nil {
				got, err = osupdate.HashFile(name)
			}
			if err == nil && got != req.ID {
				err = fmt.Errorf("package changed")
			}
		}
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		logFile, err := os.OpenFile(osupdate.Base+"/installer.log", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			updateReply(c, nil, err)
			return
		}
		defer logFile.Close()
		cmd := exec.Command(osupdate.Helper, "install-inherited", req.ID)
		cmd.ExtraFiles = []*os.File{lock}
		cmd.Stdout = logFile
		cmd.Stderr = logFile
		if err = cmd.Start(); err != nil {
			updateReply(c, nil, err)
			return
		}
		go cmd.Wait()
		updateReply(c, gin.H{"state": "installing"}, nil)
	})
}
