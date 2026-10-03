package stream

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
)

var encoderSelectionMutex sync.Mutex
var encoderSelectionFile = "/etc/kvm/encoder_codec"

func init() {
	if data, err := os.ReadFile(encoderSelectionFile); err == nil {
		codec := VideoCodec(strings.TrimSpace(string(data)))
		if codec == VideoCodecH264 || codec == VideoCodecH265 {
			defaultVideoSource.selectConfig(EncoderConfig{Codec: codec})
		}
	}
}

func saveEncoderSelection(codec VideoCodec) error {
	file, err := os.CreateTemp(filepath.Dir(encoderSelectionFile), ".encoder-codec-*")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.WriteString(string(codec) + "\n"); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, encoderSelectionFile)
}

// The selected codec remains authoritative while old subscribers disconnect.
func GetEncoderState(c *gin.Context) {
	config, active := ActiveEncoderConfig()
	selectedConfig, selected := defaultVideoSource.selectedConfig()
	if selected {
		config = selectedConfig
	}
	c.Header("Cache-Control", "no-store")
	var rsp proto.Response
	rsp.OkRspWithData(c, gin.H{"active": active, "selected": selected, "codec": config.Codec})
}

// Selecting a codec is an explicit shared-setting change, never a join side effect.
func SetEncoderState(c *gin.Context) {
	if principal, ok := middleware.CurrentPrincipal(c); !ok || principal.Role != authn.RoleAdmin {
		c.AbortWithStatus(http.StatusForbidden)
		return
	}
	var req struct {
		Codec VideoCodec `json:"codec"`
	}
	var rsp proto.Response
	if err := c.ShouldBindJSON(&req); err != nil || (req.Codec != VideoCodecH264 && req.Codec != VideoCodecH265) {
		rsp.ErrRsp(c, -1, "codec must be h264 or h265")
		return
	}
	encoderSelectionMutex.Lock()
	defer encoderSelectionMutex.Unlock()
	if err := saveEncoderSelection(req.Codec); err != nil {
		rsp.ErrRsp(c, -2, "cannot save encoder selection")
		return
	}
	defaultVideoSource.selectConfig(EncoderConfig{Codec: req.Codec})
	rsp.OkRsp(c)
}
