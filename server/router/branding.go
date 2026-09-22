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
	r.GET("/api/branding/logo", service.Logo)
	r.GET("/api/branding/favicon", service.Favicon)
	admin := r.Group("/api/branding").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	admin.POST("/logo", service.UploadLogo)
	admin.DELETE("/logo", service.DeleteLogo)
	admin.POST("/favicon", service.UploadFavicon)
	admin.DELETE("/favicon", service.DeleteFavicon)
	admin.POST("/button-color", service.SetButtonColor)
	admin.DELETE("/button-color", service.DeleteButtonColor)
	admin.POST("/banner-style", service.SetBannerStyle)
}
