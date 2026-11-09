package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/auth"
)

func authRouter(r *gin.Engine) {
	service := auth.NewService()

	r.POST("/api/auth/login", service.Login)          // login
	r.POST("/api/auth/login/totp", service.LoginTOTP) // second factor of a login

	api := r.Group("/api").Use(middleware.CheckToken())

	api.GET("/auth/password", service.IsPasswordUpdated) // is password updated
	api.GET("/auth/account", service.GetAccount)         // get account
	api.POST("/auth/password", service.ChangePassword)   // change password
	api.POST("/auth/logout", service.Logout)             // logout

	// Two-factor authentication of the signed-in user.
	api.POST("/auth/totp/setup", service.TOTPSetup)
	api.POST("/auth/totp/enable", service.TOTPEnable)
	api.POST("/auth/totp/disable", service.TOTPDisable)
	api.POST("/auth/totp/recovery-codes", service.TOTPRecoveryCodes)

	admin := r.Group("/api").Use(
		middleware.CheckToken(),
		middleware.RequireRole(authn.RoleAdmin),
	)
	admin.GET("/auth/users", service.ListUsers)
	admin.POST("/auth/users", service.CreateUser)
	admin.PUT("/auth/users/:username", service.UpdateUser)
	admin.DELETE("/auth/users/:username", service.DeleteUser)
	admin.POST("/auth/users/:username/password", service.ChangeUserPassword)
	admin.DELETE("/auth/users/:username/totp", service.ResetUserTOTP)
}
