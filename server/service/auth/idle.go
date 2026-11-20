package auth

import (
	"github.com/gin-gonic/gin"

	"NanoKVM-Server/config"
	"NanoKVM-Server/middleware"
	"NanoKVM-Server/proto"
)

// idleRsp is the inactivity limit as a signed-in client sees it. Seconds.
type idleRsp struct {
	// Available is false with authentication disabled: nothing to sign out of.
	Available bool  `json:"available"`
	Limit     int   `json:"limit"`
	Left      int   `json:"left"`
	Limits    []int `json:"limits"`
}

func idleAnswer(c *gin.Context, touch bool) idleRsp {
	limit, left := middleware.IdleStatus(c, touch)
	return idleRsp{
		Available: config.GetInstance().Authentication != "disable",
		Limit:     limit, Left: left, Limits: middleware.IdleLimits,
	}
}

// GetIdle tells the page how long its sign-in still has.
func (s *Service) GetIdle(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, idleAnswer(c, false))
}

// ReportActivity starts the sign-in's period again: the page saw input.
func (s *Service) ReportActivity(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, idleAnswer(c, true))
}

// SetIdle changes the limit for every sign-in.
func (s *Service) SetIdle(c *gin.Context) {
	var rsp proto.Response
	// The limit is required: a missing or null one is not "no limit".
	var req struct {
		Limit *int `json:"limit"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || req.Limit == nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	if err := middleware.SetIdleLimit(*req.Limit); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	rsp.OkRspWithData(c, idleAnswer(c, false))
}
