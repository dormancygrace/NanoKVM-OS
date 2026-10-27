package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/proto"
	bmcservice "NanoKVM-Server/service/bmc"
	"NanoKVM-Server/service/vm"
)

func BMCLoopbackHTTPAllowedPaths() []string {
	return []string{bmc.ATXPath}
}

func bmcRouter(r *gin.Engine) {
	atx := vm.NewService()
	// For the IPMI add-on's daemon only: loopback with the internal token.
	localAPI := r.Group("/api/internal").Use(middleware.CheckLoopbackInternalToken())
	localAPI.GET("/atx", atx.GetATXInternal)
	localAPI.POST("/atx", atx.PressATXInternal)

	service := bmcservice.NewService(vm.ATX{})
	// Redfish has its own account (the BMC account of the settings page) and
	// checks it itself, as on any BMC: these routes take no web login. With
	// Redfish off they answer 404.
	redfish := gin.WrapF(service.ServeRedfish)
	r.Any("/redfish", redfish)
	r.Any("/redfish/*path", redfish)

	api := r.Group("/api/bmc").Use(middleware.CheckToken(), middleware.RequireRole(authn.RoleAdmin))
	api.GET("/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		var response proto.Response
		status, err := service.Status()
		if err != nil {
			response.ErrRsp(c, -1, err.Error())
			return
		}
		response.OkRspWithData(c, status)
	})
	api.PUT("/config", func(c *gin.Context) {
		var request bmcservice.Settings
		var response proto.Response
		if err := c.ShouldBindJSON(&request); err != nil {
			response.ErrRsp(c, -1, "invalid configuration")
			return
		}
		if err := service.Configure(request); err != nil {
			response.ErrRsp(c, -1, err.Error())
			return
		}
		response.OkRsp(c)
	})
	api.POST("/ipmi/install", func(c *gin.Context) {
		var response proto.Response
		if err := service.InstallIPMI(); err != nil {
			response.ErrRsp(c, -1, err.Error())
			return
		}
		response.OkRsp(c)
	})
	api.POST("/ipmi/uninstall", func(c *gin.Context) {
		var response proto.Response
		if err := service.UninstallIPMI(); err != nil {
			response.ErrRsp(c, -1, err.Error())
			return
		}
		response.OkRsp(c)
	})
}
