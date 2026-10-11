package middleware

import (
	"NanoKVM-Server/common"
	"github.com/gin-gonic/gin"
	"net/http"
)

func RequireVideo(c *gin.Context) {
	if common.VideoDisabled() {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"code": 503, "msg": "video recovery is active"})
		return
	}
	c.Next()
}
