package picoclaw

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"

	mcpservice "NanoKVM-Server/service/mcp"
)

// JSON-RPC 2.0 types for MCP HTTP transport

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

// MCP tool definitions

var mcpToolDefinitions = []map[string]interface{}{
	{
		"name":        "kvm_screenshot",
		"description": "Capture the current HDMI frame from the downstream remote host as a base64-encoded JPEG image.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"width":   map[string]interface{}{"type": "integer", "description": "Target width in pixels (optional, default: 960)"},
				"height":  map[string]interface{}{"type": "integer", "description": "Target height in pixels (optional)"},
				"quality": map[string]interface{}{"type": "integer", "description": "JPEG quality 1-100 (optional, default: 60)"},
			},
		},
	},
	{
		"name":        "kvm_actions",
		"description": "Send one or more HID actions (click, type, hotkey, scroll, drag, move, wait) to the downstream remote host. Use normalized [0,1] coordinates for mouse actions.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"actions": map[string]interface{}{
					"type":        "array",
					"description": "Array of action objects. Each requires an 'action' field (click, move, type, hotkey, scroll, drag, wait).",
					"items": map[string]interface{}{
						"type": "object",
						"properties": map[string]interface{}{
							"action":    map[string]interface{}{"type": "string"},
							"x":         map[string]interface{}{"type": "number"},
							"y":         map[string]interface{}{"type": "number"},
							"button":    map[string]interface{}{"type": "string"},
							"text":      map[string]interface{}{"type": "string"},
							"keys":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
							"direction": map[string]interface{}{"type": "string"},
							"amount":    map[string]interface{}{"type": "integer"},
							"duration_ms": map[string]interface{}{
								"type":        "integer",
								"minimum":     0,
								"maximum":     maxWaitDurationMS,
								"description": "Wait duration in milliseconds, up to 30000",
							},
							"from": map[string]interface{}{"type": "object", "properties": map[string]interface{}{"x": map[string]interface{}{"type": "number"}, "y": map[string]interface{}{"type": "number"}}},
							"to":   map[string]interface{}{"type": "object", "properties": map[string]interface{}{"x": map[string]interface{}{"type": "number"}, "y": map[string]interface{}{"type": "number"}}},
						},
						"required": []string{"action"},
					},
				},
			},
			"required": []string{"actions"},
		},
	},
	{
		"name": "kvm_screen_text",
		"description": "Read the text on the downstream remote host's screen (OCR on the NanoKVM's TPU; needs the TPU add-on, " +
			"inputs up to 1920x1080; takes 3-15 s). Returns the lines in reading order and their pieces with boxes in " +
			"normalized [0,1] coordinates, as kvm_actions uses: click the middle of a box to click that text.",
		"inputSchema": map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"language": map[string]interface{}{
					"type": "string",
					"enum": []string{"latin", "chinese", "cyrillic"},
					"description": "latin (English and European languages), chinese (Simplified and Traditional Chinese, " +
						"Japanese, English) or cyrillic (Russian, Ukrainian and others, with English); omitted, the device's default",
				},
				"area": map[string]interface{}{
					"type":        "object",
					"description": "Read only this part of the screen (normalized): it is read at full resolution, so small text is found",
					"properties": map[string]interface{}{
						"x": map[string]interface{}{"type": "number"}, "y": map[string]interface{}{"type": "number"},
						"w": map[string]interface{}{"type": "number"}, "h": map[string]interface{}{"type": "number"},
					},
					"required": []string{"x", "y", "w", "h"},
				},
			},
		},
	},
}

// SetTextReader gives kvm_screen_text the OCR of the TPU add-on.
func (s *Service) SetTextReader(reader mcpservice.ScreenTextReader) {
	s.textReader = reader
}

// MCPHandler handles MCP HTTP transport (JSON-RPC 2.0 over POST).
func (s *Service) MCPHandler(c *gin.Context) {
	var req jsonRPCRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32700, Message: "parse error"},
		})
		return
	}

	if req.JSONRPC != "2.0" {
		c.JSON(http.StatusOK, jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32600, Message: "invalid request: jsonrpc must be 2.0"},
		})
		return
	}

	var resp jsonRPCResponse
	switch req.Method {
	case "initialize":
		resp = s.mcpInitialize(req)
	case "tools/list":
		resp = s.mcpToolsList(req)
	case "tools/call":
		resp = s.mcpToolsCall(req, c)
	case "ping":
		resp = jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}
	default:
		resp = jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}

	c.JSON(http.StatusOK, resp)
}

func mcpModeConflictResponse(req jsonRPCRequest, err *PicoclawError, status controlStatusForMCP) jsonRPCResponse {
	message := "PicoClaw control mode is not active"
	reason := CodeControlRequired
	if err != nil && err.Message != "" {
		message = err.Message
		reason = err.Code
	}
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Error: &jsonRPCError{
			Code:    -32003,
			Message: message,
			Data: map[string]interface{}{
				"reason":        reason,
				"control_mode":  status.Mode,
				"transitioning": status.Transitioning,
			},
		},
	}
}

type controlStatusForMCP struct {
	Mode          string
	Transitioning bool
}

func (s *Service) mcpInitialize(req jsonRPCRequest) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "nanokvm",
				"version": "1.0.0",
			},
		},
	}
}

func (s *Service) mcpToolsList(req jsonRPCRequest) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"tools": mcpToolDefinitions,
		},
	}
}

func (s *Service) mcpToolsCall(req jsonRPCRequest, c *gin.Context) jsonRPCResponse {
	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32602, Message: "invalid params"},
		}
	}

	switch params.Name {
	case "kvm_screenshot":
		return s.mcpScreenshot(req, params.Arguments, c)
	case "kvm_screen_text":
		// Reading is no input: like the screenshot, it needs no control mode.
		return s.mcpScreenText(req, params.Arguments, c)
	case "kvm_actions":
		releaseMode, modeErr := s.acquireControlMode()
		if modeErr != nil {
			controlStatus := controlStatusForMCP{}
			if status, err := s.control.Status(); err == nil {
				controlStatus = controlStatusForMCP{
					Mode:          string(status.Mode),
					Transitioning: status.Transitioning,
				}
			}
			return mcpModeConflictResponse(req, modeErr, controlStatus)
		}
		defer releaseMode()

		operationCtx, releaseOperation := s.beginControlOperation(c.Request.Context())
		defer releaseOperation()
		c.Request = c.Request.WithContext(operationCtx)
		return s.mcpActions(req, params.Arguments, c)
	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &jsonRPCError{Code: -32602, Message: fmt.Sprintf("unknown tool: %s", params.Name)},
		}
	}
}

func (s *Service) mcpScreenshot(req jsonRPCRequest, args json.RawMessage, c *gin.Context) jsonRPCResponse {
	var params struct {
		Width   uint16 `json:"width"`
		Height  uint16 `json:"height"`
		Quality uint16 `json:"quality"`
	}
	if args != nil {
		_ = json.Unmarshal(args, &params)
	}

	query := ScreenshotQuery{
		Format:  "base64",
		Width:   params.Width,
		Height:  params.Height,
		Quality: params.Quality,
	}

	data, meta, err := s.captureScreenshot(c.Request.Context(), query)
	if err != nil {
		return mcpToolError(req, err.Message)
	}

	b64 := base64.StdEncoding.EncodeToString(data)
	s.publishMCPObservation(c, "screenshot captured", b64)

	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": "screenshot captured",
				},
				{
					"type":     "image",
					"data":     b64,
					"mimeType": "image/jpeg",
				},
			},
			"meta": map[string]interface{}{
				"source_width":   meta.SourceWidth,
				"source_height":  meta.SourceHeight,
				"capture_width":  meta.CaptureWidth,
				"capture_height": meta.CaptureHeight,
			},
		},
	}
}

func (s *Service) mcpScreenText(req jsonRPCRequest, args json.RawMessage, c *gin.Context) jsonRPCResponse {
	if s.textReader == nil {
		return mcpToolError(req, mcpservice.ErrNoTextReader.Error())
	}
	var params struct {
		Language string                `json:"language"`
		Area     *mcpservice.ScreenBox `json:"area"`
	}
	if len(args) > 0 {
		if err := json.Unmarshal(args, &params); err != nil {
			return mcpToolError(req, "invalid screen text arguments")
		}
	}
	if a := params.Area; a != nil && (a.X < 0 || a.Y < 0 || a.W <= 0 || a.H <= 0 || a.X+a.W > 1 || a.Y+a.H > 1) {
		return mcpToolError(req, "area must be non-empty and lie within the screen")
	}
	text, err := s.textReader.ReadText(c.Request.Context(), params.Language, params.Area)
	if err != nil {
		return mcpToolError(req, err.Error())
	}
	resultJSON, _ := json.Marshal(mcpservice.ScreenTextResult{OK: true, ScreenText: text})
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{{"type": "text", "text": string(resultJSON)}},
		},
	}
}

func (s *Service) publishMCPObservation(c *gin.Context, text string, imageBase64 string) {
	if strings.TrimSpace(imageBase64) == "" {
		return
	}

	sessionID, session, err := s.requireActiveGatewaySession(c)
	if err != nil || session == nil || session.Downstream == nil {
		return
	}

	message := newPicoGatewayObservationMessage(sessionID, map[string]any{
		"content":      text,
		"image_base64": imageBase64,
		"mime_type":    "image/jpeg",
	})
	if writeErr := session.writeDownstreamJSON(s.config.Get(), message); writeErr != nil {
		log.Warnf("failed to deliver picoclaw observation message: %v", writeErr)
	}
}

func (s *Service) mcpActions(req jsonRPCRequest, args json.RawMessage, c *gin.Context) jsonRPCResponse {
	var params struct {
		Actions []Action `json:"actions"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return mcpToolError(req, "invalid actions payload")
	}
	if len(params.Actions) == 0 {
		return mcpToolError(req, "empty actions array")
	}

	sessionID := c.GetHeader(sessionIDHeader)
	if sessionID == "" {
		sessionID = s.lock.Owner()
	}

	result, err := s.executeActions(c.Request.Context(), sessionID, params.Actions)
	if err != nil {
		return mcpToolError(req, err.Message)
	}

	resultJSON, _ := json.Marshal(result)
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": string(resultJSON),
				},
			},
		},
	}
}

func mcpToolError(req jsonRPCRequest, message string) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]interface{}{
			"isError": true,
			"content": []map[string]interface{}{
				{
					"type": "text",
					"text": message,
				},
			},
		},
	}
}
