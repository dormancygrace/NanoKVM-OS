package router

import (
	"NanoKVM-Server/authn"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/service/stream"
	"NanoKVM-Server/service/stream/audio"
	"NanoKVM-Server/service/stream/direct"
	"NanoKVM-Server/service/stream/mjpeg"
	"NanoKVM-Server/service/stream/webrtc"

	"github.com/gin-gonic/gin"
)

func streamRouter(r *gin.Engine) {
	api := r.Group("/api").Use(middleware.CheckToken())
	admin := r.Group("/api").Use(
		middleware.CheckToken(),
		middleware.RequireRole(authn.RoleAdmin),
	)

	api.GET("/stream/mjpeg", middleware.RequireVideo, mjpeg.Connect)            // mjpeg stream
	api.GET("/stream/mjpeg/tiles", middleware.RequireVideo, mjpeg.ConnectTiles) // changed tiles only (websocket)
	admin.POST("/stream/mjpeg/detect", mjpeg.UpdateFrameDetect)                 // update frame detect (device-wide)
	api.POST("/stream/mjpeg/detect/stop", mjpeg.StopFrameDetect)                // temporary stop frame detect

	api.GET("/stream/state", stream.GetEncoderState)
	api.POST("/stream/state", stream.SetEncoderState)
	api.GET("/stream/audio", audio.Connect)
	api.GET("/stream/audio/status", audio.Status)

	api.GET("/stream/video", middleware.RequireVideo, webrtc.Connect)             // configurable video stream (webrtc)
	api.GET("/stream/video/direct", middleware.RequireVideo, direct.Connect)      // configurable video stream (direct)
	api.GET("/stream/h264", middleware.RequireVideo, webrtc.ConnectLegacy)        // legacy h264 stream (webrtc)
	api.GET("/stream/h264/direct", middleware.RequireVideo, direct.ConnectLegacy) // legacy h264 stream (direct)
}
