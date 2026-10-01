package vm

import (
	"errors"
	"net/http"
	"net/url"

	"NanoKVM-Server/logs"

	"github.com/gin-gonic/gin"
)

func (s *Service) GetLogs(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("X-Content-Type-Options", "nosniff")
	if len(c.Request.URL.RawQuery) > 128 {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid log query"})
		return
	}
	query, err := url.ParseQuery(c.Request.URL.RawQuery)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid log query"})
		return
	}
	for key, values := range query {
		if (key != "source" && key != "boot") || len(values) != 1 {
			c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "invalid log query"})
			return
		}
	}
	source, boot := query.Get("source"), query.Get("boot")
	if source == "" {
		source = "system"
	}
	if boot == "" {
		boot = "current"
	}
	snapshot, err := logs.Default.ReadBoot(source, boot)
	if errors.Is(err, logs.ErrReadRate) {
		c.Header("Retry-After", "5")
		c.JSON(http.StatusTooManyRequests, gin.H{"code": -1, "msg": "log read rate exceeded"})
		return
	}
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"code": -1, "msg": "unsupported log source"})
		return
	}
	snapshot.ArchiveAvailable = logs.Default.ArchiveHealthy()
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "success", "data": snapshot})
}

func (s *Service) GetLogBoots(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	boots, available := logs.Default.Boots()
	c.JSON(http.StatusOK, gin.H{"code": 0, "msg": "success", "data": gin.H{"boots": boots, "archiveAvailable": available}})
}
