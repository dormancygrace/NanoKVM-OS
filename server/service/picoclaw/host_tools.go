package picoclaw

import (
	"errors"
	"os"

	"github.com/gin-gonic/gin"
)

type HostToolsUpdateRequest struct {
	Enabled *bool `json:"enabled"`
}

// UpdateHostTools turns the agent's tools on the NanoKVM itself (shell, file
// writes, scheduled commands) on or off. It is separate from the model
// config, which needs the API key again on every save.
func (s *Service) UpdateHostTools(c *gin.Context) {
	s.ensureDependencies()
	var req HostToolsUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil {
		writePicoclawError(c, newPicoclawError(CodeInvalidAction, "invalid host tools payload"))
		return
	}

	// Turning the tools off must reach a gateway that is running already.
	applied := s.applyConfigChange(c, "host tools setting", "host_tools", !*req.Enabled, func() *PicoclawError {
		if err := s.runtimeIntent.SetHostTools(*req.Enabled, "host_tools"); err != nil {
			return newPicoclawError(CodeRuntimeUnavailable, "failed to store the host tools setting: "+err.Error())
		}
		// Without a config yet, the next start writes the defaults.
		if err := ensurePicoclawStartupDefaults(); err != nil && !errors.Is(err, os.ErrNotExist) {
			return newPicoclawError(CodeRuntimeUnavailable, "host tools setting saved, but failed to persist picoclaw defaults: "+err.Error())
		}
		return nil
	})
	if !applied {
		return
	}

	writeSuccess(c, gin.H{
		"host_tools": *req.Enabled,
		"status":     s.runtimeStatus(),
	})
}
