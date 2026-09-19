package router

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/branding"
	"github.com/gin-gonic/gin"
)

func brandingRouter(r *gin.Engine) {
	service := branding.New()
	// Public read access is necessary for the login page and browser favicon.
	r.GET("/api/branding", service.Get)
	r.GET("/api/branding/logo", service.Image)
	admin := r.Group("/api/branding").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	admin.POST("", service.Set)
	admin.POST("/logo", service.Upload)
	admin.DELETE("/logo", service.Delete)
}
