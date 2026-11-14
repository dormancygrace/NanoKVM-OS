package vpn

import (
	"errors"
	"os"

	"github.com/gin-gonic/gin"
)

// Purge reports whether an Uninstall request also deletes the VPN's
// profiles, login and settings ({"purge": true}). A request without it keeps
// them, so that installing again restores the previous state.
func Purge(c *gin.Context) bool {
	var req struct {
		Purge bool `json:"purge"`
	}
	return c.ShouldBindJSON(&req) == nil && req.Purge
}

// RemoveState deletes the directories and files that hold a VPN's state.
func RemoveState(paths ...string) error {
	var errs []error
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}
