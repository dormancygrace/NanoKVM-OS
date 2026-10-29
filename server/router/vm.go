package router

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
	"NanoKVM-Server/service/failsafe"
	"NanoKVM-Server/service/vm"
)

func vmRouter(r *gin.Engine) {
	service := vm.NewService()

	api := r.Group("/api").Use(middleware.CheckToken())
	admin := r.Group("/api").Use(
		middleware.CheckToken(),
		middleware.RequireRole(authn.RoleAdmin),
	)

	admin.GET("/vm/video/crashes", func(c *gin.Context) {
		var rsp proto.Response
		rsp.OkRspWithData(c, failsafe.Crashes())
	})
	admin.POST("/vm/video/retry", func(c *gin.Context) {
		var rsp proto.Response
		if err := failsafe.Retry(); err != nil {
			rsp.ErrRsp(c, 1, err.Error())
			return
		}
		rsp.OkRsp(c)
	})
	api.GET("/vm/date-time", service.GetDateTime)
	admin.POST("/vm/date-time", service.SetDateTime)

	api.GET("/vm/dashboard", service.GetDashboard)
	admin.GET("/vm/logs", service.GetLogs)
	admin.GET("/vm/logs/boots", service.GetLogBoots)
	admin.GET("/vm/diagnostics", service.GetDiagnostics)
	admin.GET("/vm/diagnostics/report", service.DownloadDiagnosticsReport)
	api.GET("/vm/info", service.GetInfo)         // get device information
	api.GET("/vm/hardware", service.GetHardware) // get hardware version

	// HDMI, ATX LEDs, input, audio and (administrators) USB in one poll.
	api.GET("/vm/live-status", func(c *gin.Context) {
		principal, _ := middleware.CurrentPrincipal(c)
		service.GetLiveStatus(c, principal.Role == authn.RoleAdmin)
	})

	// ATX switch: the board's default is written at the first start.
	vm.EnsureATXSetting()
	api.GET("/vm/atx", service.GetATX)
	admin.POST("/vm/atx", service.SetATX)

	api.POST("/vm/gpio", requireInputOwner(), service.SetGpio)                 // update gpio
	api.GET("/vm/gpio", service.GetGpio)                                       // get gpio
	admin.POST("/vm/screen", middleware.RequireVideo, service.SetScreen)       // update screen
	api.GET("/vm/screen", service.GetScreen)                                   // read shared screen settings
	admin.GET("/vm/screen/text", service.GetScreenText)                        // read the text on the screen (TPU add-on)
	admin.GET("/vm/screen/text/settings", service.GetScreenTextSettings)       // on or off, installed languages
	admin.POST("/vm/screen/text/settings", service.SetScreenTextSettings)      // turn text recognition on or off
	api.GET("/vm/video/capabilities", service.GetVideoCapabilities)            // video choices and limits
	admin.POST("/vm/video", middleware.RequireVideo, service.SetVideoSettings) // apply video settings together

	api.GET("/vm/input-region", service.GetInputRegion)
	api.POST("/vm/input-region", service.SetInputRegion)
	api.GET("/vm/input-resolution", service.GetInputResolution)

	admin.GET("/vm/terminal", service.Terminal) // web terminal

	admin.GET("/vm/script", service.GetScripts)                    // get script
	admin.POST("/vm/script/upload", service.UploadScript)          // upload script
	admin.POST("/vm/script/run", service.RunScript)                // run script
	admin.DELETE("/vm/script", service.DeleteScript)               // delete script
	admin.POST("/vm/script/autostart", service.SetScriptAutostart) // run a script at startup

	admin.GET("/vm/device/virtual", service.GetVirtualDevice)     // get virtual device
	admin.POST("/vm/device/virtual", service.UpdateVirtualDevice) // update virtual device
	admin.PUT("/vm/device/virtual", service.SetUSBComposition)    // apply an entire composition

	admin.GET("/vm/serial-console", service.GetSerialConsole)        // headless-Linux console helper
	admin.POST("/vm/serial-console/test", service.TestSerialConsole) // Enter on ttyGS0, read the prompt
	// Public: the host fetches the script with curl/wget and has no credentials.
	r.GET(vm.SerialConsoleScriptPath, service.ServeSerialConsoleScript)

	admin.GET("/vm/usb-internet", service.GetUSBInternet)
	admin.PUT("/vm/usb-internet", service.SetUSBInternet)

	admin.GET("/vm/memory/status", service.GetMemoryStatus)
	admin.POST("/vm/memory/swap", service.SetMemorySwap)

	admin.GET("/vm/memory/limit", service.GetMemoryLimit)  // get memory limit
	admin.POST("/vm/memory/limit", service.SetMemoryLimit) // set memory limit

	admin.GET("/vm/cpu-frequency", service.GetCPUFrequency)
	admin.POST("/vm/cpu-frequency", service.SetCPUFrequency)

	admin.GET("/vm/oled", service.GetOLED)  // get OLED configuration
	admin.POST("/vm/oled", service.SetOLED) // set OLED configuration

	// Only supported by PCIe version
	api.GET("/vm/hdmi", service.GetHdmiState)                                  // get HDMI state
	api.POST("/vm/hdmi/reset", middleware.RequireVideo, service.ResetHdmi)     // reset hdmi
	admin.POST("/vm/hdmi/enable", middleware.RequireVideo, service.EnableHdmi) // enable hdmi
	admin.POST("/vm/hdmi/disable", service.DisableHdmi)                        // disable hdmi
	admin.POST("/vm/hdmi/timeout", service.SetHdmiIdleTimeout)

	admin.POST("/vm/ssh/keys/import", service.ImportSSHKeys) // append root public keys
	admin.GET("/vm/ssh", service.GetSSHState)                // get SSH state
	admin.POST("/vm/ssh/enable", service.EnableSSH)          // enable SSH
	admin.POST("/vm/ssh/disable", service.DisableSSH)        // disable SSH

	admin.GET("/vm/swap", service.GetSwap)  // get swap file size
	admin.POST("/vm/swap", service.SetSwap) // set swap file size

	admin.GET("/vm/mouse-jiggler", service.GetMouseJiggler)   // get mouse jiggler
	admin.POST("/vm/mouse-jiggler/", service.SetMouseJiggler) // set mouse jiggler

	api.GET("/vm/hostname", service.GetHostname)    // Get Hostname
	admin.POST("/vm/hostname", service.SetHostname) // Set Hostname

	api.GET("/vm/web-title", service.GetWebTitle)    // Get web title
	admin.POST("/vm/web-title", service.SetWebTitle) // Set web title

	admin.GET("/vm/mdns", service.GetMdnsState)         // get mDNS state
	admin.POST("/vm/mdns/enable", service.EnableMdns)   // enable mDNS
	admin.POST("/vm/mdns/disable", service.DisableMdns) // disable mDNS

	admin.GET("/vm/bluetooth", service.GetBluetoothState)         // get Bluetooth state and version
	admin.POST("/vm/bluetooth/enable", service.EnableBluetooth)   // load Bluetooth now and at boot
	admin.POST("/vm/bluetooth/disable", service.DisableBluetooth) // unload Bluetooth, not at boot

	admin.POST("/vm/tls", service.SetTls)                                  // enable/disable TLS
	admin.GET("/vm/tls", service.GetTls)                                   // HTTPS and certificate status
	admin.POST("/vm/tls/certificate", service.UploadTlsCertificate)        // install a custom certificate
	admin.POST("/vm/tls/certificate/delete", service.DeleteTlsCertificate) // back to the generated certificate
	admin.POST("/vm/tls/reload", service.ReloadTls)                        // serve the pair on disk

	admin.POST("/vm/system/reboot", service.Reboot) // reboot system
}
