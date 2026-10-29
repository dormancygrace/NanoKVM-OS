package mcpservice

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"NanoKVM-Server/service/controlmode"

	"github.com/google/jsonschema-go/jsonschema"
	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
	log "github.com/sirupsen/logrus"
)

const (
	powerToolName        = "power"
	virtualMediaToolName = "virtual_media"
	wakeOnLANToolName    = "wake_on_lan"
)

// The power tool's actions and the BMC power actions they run (bmc.Power).
var powerActions = map[string]string{
	"on":        "On",
	"shutdown":  "GracefulShutdown",
	"force_off": "ForceOff",
	"reset":     "ForceRestart",
	"cycle":     "PowerCycle",
	"press":     "PushPowerButton",
}

// HostControl changes the host itself: its power, the medium on the virtual
// disk and Wake-on-LAN. MCP offers these tools only while the administrator
// allows it (Config.AllowHostControl); a nil part leaves its tool out.
type HostControl struct {
	Power PowerControl
	Media MediaControl
	Wake  func(mac, iface string) error
}

// PowerControl runs a BMC power action ("On", "ForceOff", ...).
type PowerControl interface {
	Do(ctx context.Context, action string) error
}

// MediaControl is the virtual disk of Settings > Virtual media.
type MediaControl interface {
	Images() ([]string, error)
	MountedImage() (file string, cdrom bool, err error)
	// MountImageFile mounts an image of the list; "" ejects.
	MountImageFile(name string, cdrom bool) error
}

type PowerParams struct {
	Action string `json:"action"`
}

type VirtualMediaParams struct {
	Action string `json:"action"`
	File   string `json:"file,omitempty"`
	Cdrom  bool   `json:"cdrom,omitempty"`
}

type VirtualMediaResult struct {
	OK      bool     `json:"ok"`
	Images  []string `json:"images,omitempty"`
	Mounted string   `json:"mounted"`
	Cdrom   bool     `json:"cdrom"`
	// Why there is no virtual disk to mount on (USB storage off), if so.
	Unavailable string `json:"unavailable,omitempty"`
}

type WakeOnLANParams struct {
	MAC       string `json:"mac"`
	Interface string `json:"interface,omitempty"`
}

// hostControlAllowed reads the switch for every request, so turning it off
// takes the tools away at once.
func hostControlAllowed() bool {
	cfg, err := loadConfig()
	if err != nil {
		log.Errorf("MCP config: %v", err)
		return false
	}
	return cfg.AllowHostControl
}

func registerHostControlTools(server *protocol.Server, executor *toolExecutor, control HostControl) {
	if control.Power != nil {
		protocol.AddTool(server, &protocol.Tool{Name: powerToolName, Description: "Press the host's power or reset button. on: power on " +
			"unless known to be on; shutdown: short press so the OS shuts down; force_off: hold the power button; reset: press reset; " +
			"cycle: off, then on; press: one short press whatever the state. Without a power LED (host_status power.known false) " +
			"every action presses blindly", InputSchema: powerSchema()}, powerHandler(executor, control.Power))
	}
	if control.Media != nil {
		protocol.AddTool(server, &protocol.Tool{Name: virtualMediaToolName, Description: "The NanoKVM's virtual USB disk: list the " +
			"images on the NanoKVM, show what is mounted, mount one (as a disk, or as a CD/DVD drive with cdrom) or eject. " +
			"The host sees the drive change at once", InputSchema: virtualMediaSchema()}, virtualMediaHandler(executor, control.Media))
	}
	if control.Wake != nil {
		protocol.AddTool(server, &protocol.Tool{Name: wakeOnLANToolName, Description: "Send a Wake-on-LAN magic packet to a " +
			"MAC address on the NanoKVM's network", InputSchema: wakeOnLANSchema()}, wakeOnLANHandler(executor, control.Wake))
	}
}

// beginControl lets a host control tool run only in MCP mode, and keeps the
// mode from changing until it is done. It takes no input lease: like a
// hardware BMC (and the Redfish and IPMI services), power and media work
// whoever holds the keyboard.
func (e *toolExecutor) beginControl() (func(), error) {
	if e == nil || e.control == nil {
		return func() {}, nil
	}
	release, err := e.control.AcquireWrite(controlmode.ModeMCP)
	if err != nil {
		return nil, fmt.Errorf("MCP service is disabled: %w", err)
	}
	return release, nil
}

func powerHandler(executor *toolExecutor, power PowerControl) protocol.ToolHandlerFor[PowerParams, ToolOKResult] {
	return func(ctx context.Context, _ *protocol.CallToolRequest, input PowerParams) (*protocol.CallToolResult, ToolOKResult, error) {
		action, ok := powerActions[strings.ToLower(strings.TrimSpace(input.Action))]
		if !ok {
			return nil, ToolOKResult{}, fmt.Errorf("unknown power action: %s", input.Action)
		}
		release, err := executor.beginControl()
		if err != nil {
			return nil, ToolOKResult{}, err
		}
		defer release()
		if err := power.Do(ctx, action); err != nil {
			return nil, ToolOKResult{}, err
		}
		return nil, ToolOKResult{OK: true}, nil
	}
}

func virtualMediaHandler(executor *toolExecutor, media MediaControl) protocol.ToolHandlerFor[VirtualMediaParams, VirtualMediaResult] {
	return func(ctx context.Context, _ *protocol.CallToolRequest, input VirtualMediaParams) (*protocol.CallToolResult, VirtualMediaResult, error) {
		var result VirtualMediaResult
		switch action := strings.ToLower(strings.TrimSpace(input.Action)); action {
		case "list", "status":
			if action == "list" {
				images, err := media.Images()
				if err != nil {
					return nil, result, err
				}
				result.Images = images
			}
		case "mount", "eject":
			file := ""
			if action == "mount" {
				if input.File == "" {
					return nil, result, errors.New("mount needs file, one of the listed images")
				}
				file = input.File
			}
			release, err := executor.beginControl()
			if err != nil {
				return nil, result, err
			}
			err = media.MountImageFile(file, input.Cdrom && file != "")
			release()
			if err != nil {
				return nil, result, err
			}
		default:
			return nil, result, fmt.Errorf("unknown virtual media action: %s", input.Action)
		}
		// The list and the state still answer without a virtual disk: the
		// agent learns why a mount would fail.
		mounted, cdrom, err := media.MountedImage()
		if err != nil {
			result.Unavailable = err.Error()
		}
		result.OK, result.Mounted, result.Cdrom = true, mounted, cdrom
		return nil, result, nil
	}
}

func wakeOnLANHandler(executor *toolExecutor, wake func(mac, iface string) error) protocol.ToolHandlerFor[WakeOnLANParams, ToolOKResult] {
	return func(ctx context.Context, _ *protocol.CallToolRequest, input WakeOnLANParams) (*protocol.CallToolResult, ToolOKResult, error) {
		release, err := executor.beginControl()
		if err != nil {
			return nil, ToolOKResult{}, err
		}
		defer release()
		if err := wake(input.MAC, input.Interface); err != nil {
			return nil, ToolOKResult{}, err
		}
		return nil, ToolOKResult{OK: true}, nil
	}
}

func powerSchema() *jsonschema.Schema {
	return objectSchema(map[string]*jsonschema.Schema{
		"action": {Type: "string", Enum: []any{"on", "shutdown", "force_off", "reset", "cycle", "press"}},
	}, "action")
}

func virtualMediaSchema() *jsonschema.Schema {
	return objectSchema(map[string]*jsonschema.Schema{
		"action": {Type: "string", Enum: []any{"list", "status", "mount", "eject"}},
		"file":   {Type: "string", Description: "mount: an image path as list returns it"},
		"cdrom":  {Type: "boolean", Description: "mount: present the image as a CD/DVD drive (ISO files)"},
	}, "action")
}

func wakeOnLANSchema() *jsonschema.Schema {
	return objectSchema(map[string]*jsonschema.Schema{
		"mac":       {Type: "string", Description: "MAC address of the host to wake, e.g. 00:11:22:33:44:55"},
		"interface": {Type: "string", Description: "Network interface of the NanoKVM to send from; omitted, the NanoKVM picks one"},
	}, "mac")
}
