package vm

import (
	"encoding/base64"
	"net/http"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/common"
	"NanoKVM-Server/proto"
)

// WatermarkBlockRsp is a block's place and its PNG (base64; empty if missing).
type WatermarkBlockRsp struct {
	common.WatermarkBlock
	Image string `json:"image"`
}

// WatermarkRsp is the saved watermark and its limits.
type WatermarkRsp struct {
	Enabled   bool                `json:"enabled"`
	Margin    int                 `json:"margin"`
	Design    string              `json:"design"`
	Blocks    []WatermarkBlockRsp `json:"blocks"`
	RefWidth  int                 `json:"refWidth"`
	MaxWidth  int                 `json:"maxWidth"`
	MaxHeight int                 `json:"maxHeight"`
	MaxArea   int                 `json:"maxArea"`
	MaxBlocks int                 `json:"maxBlocks"`
	MaxBytes  int                 `json:"maxBytes"`
	// Blocks the device could not put on an enabled output (it tries again).
	Missing int `json:"missing"`
}

// WatermarkBlockReq is a block: its place and, optionally, a new PNG (base64);
// without one the block keeps the saved picture of the same index.
type WatermarkBlockReq struct {
	common.WatermarkBlock
	Image *string `json:"image"`
}

type WatermarkReq struct {
	Enabled bool                `json:"enabled"`
	Margin  int                 `json:"margin"`
	Design  string              `json:"design"`
	Blocks  []WatermarkBlockReq `json:"blocks"`
}

func watermarkResponse() WatermarkRsp {
	settings, pictures := common.LoadWatermark()
	blocks := make([]WatermarkBlockRsp, len(settings.Blocks))
	for i, b := range settings.Blocks {
		blocks[i] = WatermarkBlockRsp{WatermarkBlock: b, Image: base64.StdEncoding.EncodeToString(pictures[i])}
	}
	return WatermarkRsp{
		Enabled:   settings.Enabled,
		Margin:    settings.Margin,
		Design:    settings.Design,
		Blocks:    blocks,
		RefWidth:  common.WatermarkRefWidth,
		MaxWidth:  common.WatermarkMaxWidth,
		MaxHeight: common.WatermarkMaxHeight,
		MaxArea:   common.WatermarkMaxArea,
		MaxBlocks: common.WatermarkMaxBlocks,
		MaxBytes:  common.WatermarkMaxBytes,
		Missing:   common.GetKvmVision().WatermarkMissing(),
	}
}

func (s *Service) GetWatermark(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, watermarkResponse())
}

func (s *Service) SetWatermark(c *gin.Context) {
	var req WatermarkReq
	var rsp proto.Response
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, common.WatermarkMaxRequest)
	if err := c.ShouldBindJSON(&req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	settings := common.WatermarkSettings{Enabled: req.Enabled, Margin: req.Margin, Design: req.Design,
		Blocks: make([]common.WatermarkBlock, len(req.Blocks))}
	pictures := make([][]byte, len(req.Blocks))
	for i, b := range req.Blocks {
		settings.Blocks[i] = b.WatermarkBlock
		if b.Image != nil {
			decoded, err := base64.StdEncoding.DecodeString(*b.Image)
			if err != nil {
				rsp.ErrRsp(c, -1, "block pictures must be base64")
				return
			}
			pictures[i] = decoded
		}
	}
	if err := common.SaveWatermark(settings, pictures); err != nil {
		rsp.ErrRsp(c, -2, err.Error())
		return
	}
	// The overview's picture with the old watermark is not shown again:
	// neither the cached one nor one being taken while the new one goes on.
	previewCaptureSwitched()
	defer previewCaptureSwitched()
	if err := common.ApplyWatermark(); err != nil {
		log.Errorf("watermark not applied: %v", err)
		rsp.ErrRsp(c, -3, "saved, but not applied: "+err.Error())
		return
	}
	rsp.OkRspWithData(c, watermarkResponse())
}
