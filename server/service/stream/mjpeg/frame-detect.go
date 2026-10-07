package mjpeg

import (
	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"
	"time"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
)

// UpdateFrameDetect saves the device-wide frame-detect choice and applies it.
func UpdateFrameDetect(c *gin.Context) {
	var req proto.UpdateFrameDetectReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}

	if err := common.SaveFrameDetect(req.Enabled); err != nil {
		log.Errorf("save frame detect failed: %s", err)
		rsp.ErrRsp(c, -2, "save frame detect failed")
		return
	}

	common.GetKvmVision().SetFrameDetect(common.FrameDetectFrames(req.Enabled))

	rsp.OkRsp(c)
	log.Debugf("update frame detect: %t", req.Enabled)
}

func StopFrameDetect(c *gin.Context) {
	var req proto.StopFrameDetectReq
	var rsp proto.Response

	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid parameters")
		return
	}

	// Nothing to pause, and restoring would enable it against the saved choice.
	if !common.FrameDetectEnabled() {
		rsp.OkRsp(c)
		return
	}

	duration := 10 * time.Second
	if req.Duration > 0 {
		duration = time.Duration(req.Duration) * time.Second
	}

	vision := common.GetKvmVision()

	vision.SetFrameDetect(0)
	time.Sleep(duration)
	// Restore the saved choice, which an administrator may have changed meanwhile.
	vision.SetFrameDetect(common.FrameDetectFrames(common.FrameDetectEnabled()))

	rsp.OkRsp(c)
}
