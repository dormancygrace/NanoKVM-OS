package stream

import (
	"NanoKVM-Server/proto"
	"github.com/gin-gonic/gin"
)

// GetEncoderState is advisory; subscriptions still atomically reject conflicts.
func GetEncoderState(c *gin.Context) {
	config, active := ActiveEncoderConfig()
	c.Header("Cache-Control", "no-store")
	var rsp proto.Response
	rsp.OkRspWithData(c, gin.H{"active": active, "codec": config.Codec})
}
