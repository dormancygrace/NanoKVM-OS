package proto

import "NanoKVM-Server/utils"

type IP struct {
	Name    string `json:"name"`
	Addr    string `json:"addr"`
	Version string `json:"version"`
	Type    string `json:"type"`
}

type GetInfoRsp struct {
	IPs                []IP   `json:"ips"`
	Mdns               string `json:"mdns"`
	Image              string `json:"image"`
	Application        string `json:"application"`
	BundledApplication string `json:"bundledApplication,omitempty"`
	DeviceKey          string `json:"deviceKey"`
}

type GetHardwareRsp struct {
	Version string `json:"version"`
}

type SetGpioReq struct {
	Type     string `validate:"required"`  // reset / power
	Duration uint   `validate:"omitempty"` // press time (unit: milliseconds)
}

type GetGpioRsp struct {
	PWR bool `json:"pwr"` // power led
	HDD bool `json:"hdd"` // hdd led
}

type SetScreenReq struct {
	ConfirmPowerCycle bool   `json:"confirmPowerCycle"`
	Type              string `validate:"required"` // resolution / fps / quality
	Value             int    `validate:"number"`   // value
}

type GetScriptsRsp struct {
	Files []string `json:"files"`
}

type UploadScriptRsp struct {
	File string `json:"file"`
}

type RunScriptReq struct {
	Name string `validate:"required"`
	Type string `validate:"required"` // foreground | background
}

type RunScriptRsp struct {
	Log string `json:"log"`
}

type DeleteScriptReq struct {
	Name string `validate:"required"`
}

// autostart
type GetAutostartRsp struct {
	Files []string `json:"files"`
}

type UploadAutostartReq struct {
	Content string `json:"content"`
}

type GetVirtualDeviceRsp struct {
	PointerProfile          string                     `json:"pointerProfile"`
	WindowsPointerSupported bool                       `json:"windowsPointerSupported"`
	Keyboard                bool                       `json:"keyboard"`
	Relative                bool                       `json:"relative"`
	Absolute                bool                       `json:"absolute"`
	Revision                string                     `json:"revision"`
	Network                 bool                       `json:"network"`
	Media                   bool                       `json:"media"`
	Disk                    bool                       `json:"disk"`
	Serial                  bool                       `json:"serial"`
	Audio                   bool                       `json:"audio"`
	HID                     bool                       `json:"hid"`
	Mode                    string                     `json:"mode"`
	Budget                  USBEndpointBudget          `json:"budget"`
	Costs                   map[string]USBEndpointCost `json:"costs"`
	Identity                USBIdentity                `json:"identity"`
	// the writable FAT32 user disk, a third LUN of the virtual storage
	UserDisk bool `json:"userDisk"`
}

// USBIdentity is what the connected computer sees in the device descriptor.
// The serial number is the chip UID and read-only; Default says whether the
// four writable fields are the factory ones.
type USBIdentity struct {
	VendorID     string `json:"vendorId"`
	ProductID    string `json:"productId"`
	Manufacturer string `json:"manufacturer"`
	Product      string `json:"product"`
	SerialNumber string `json:"serialNumber,omitempty"`
	Default      bool   `json:"default"`
}

// USBIdentityReq sets the identity; an empty field keeps the factory value.
type USBIdentityReq struct {
	VendorID     string `json:"vendorId"`
	ProductID    string `json:"productId"`
	Manufacturer string `json:"manufacturer"`
	Product      string `json:"product"`
}

// SerialConsoleRsp describes the headless-Linux console helper: the USB
// serial number the host matches, the script URL and the helper disk image.
type SerialConsoleRsp struct {
	SerialNumber string            `json:"serialNumber"`
	Port         string            `json:"port"`
	Script       SerialConsoleFile `json:"script"`
	Disk         SerialConsoleDisk `json:"disk"`
}

type SerialConsoleFile struct {
	Path      string `json:"path"`
	Available bool   `json:"available"`
	Sha256    string `json:"sha256,omitempty"`
	Size      int64  `json:"size,omitempty"`
}

// SerialConsoleDisk is the helper disk: shipped with the firmware, and
// attached as a read-only second LUN of the virtual drive by S03usbdev while
// USB serial and the virtual disk are enabled.
type SerialConsoleDisk struct {
	File      string `json:"file"`
	Available bool   `json:"available"`
	Attached  bool   `json:"attached"`
	// Stale: attached, but to a replaced image; refreshed at the next USB reconnect.
	Stale bool `json:"stale"`
}

type SerialConsoleTestRsp struct {
	Ok       bool   `json:"ok"`
	State    string `json:"state"`
	Response string `json:"response,omitempty"`
}

type USBEndpointBudget struct {
	InUsed   int `json:"inUsed"`
	InLimit  int `json:"inLimit"`
	OutUsed  int `json:"outUsed"`
	OutLimit int `json:"outLimit"`
}

type USBEndpointCost struct {
	In  int `json:"in"`
	Out int `json:"out"`
}

type UpdateVirtualDeviceReq struct {
	Device string `validate:"required"`
}

// Pointers distinguish an explicit false from an accidentally omitted field.
type SetUSBCompositionReq struct {
	PointerProfile string `json:"pointerProfile" validate:"omitempty,oneof=default windows"`
	Keyboard       *bool  `json:"keyboard" validate:"required"`
	Relative       *bool  `json:"relative" validate:"required"`
	Absolute       *bool  `json:"absolute" validate:"required"`
	Network        *bool  `json:"network" validate:"required"`
	Disk           *bool  `json:"disk" validate:"required"`
	Serial         *bool  `json:"serial" validate:"required"`
	Audio          *bool  `json:"audio" validate:"required"`
	Mode           string `json:"mode" validate:"required,oneof=normal hid-only"`
	Revision       string `json:"revision" validate:"required"`
	// nil keeps the identity in place (older clients send none)
	Identity *USBIdentityReq `json:"identity"`
	// nil keeps the user disk as it is; true needs the disk function and an image
	UserDisk *bool `json:"userDisk"`
}

type UpdateVirtualDeviceRsp struct {
	On bool `json:"on"`
}

type SetMemoryLimitReq struct {
	Enabled bool  `validate:"omitempty"`
	Limit   int64 `validate:"omitempty"`
}

type GetMemoryLimitRsp struct {
	Enabled bool  `json:"enabled"`
	Limit   int64 `json:"limit"`
}

type SetOledReq struct {
	Sleep int `validate:"omitempty"`
}

type GetOLEDRsp struct {
	Exist bool `json:"exist"`
	Sleep int  `json:"sleep"`
}

type GetGetHdmiStateRsp struct {
	ViewerCount int  `json:"viewerCount"`
	Enabled     bool `json:"enabled"`
	Signal      bool `json:"signal"`
	IdleTimeout int  `json:"idleTimeout"`
}

type SetHdmiIdleTimeoutReq struct {
	Minutes int `validate:"gte=0,lte=10080"`
}

type GetSSHStateRsp struct {
	Enabled bool `json:"enabled"`
}

type EnableSSHReq struct {
	Password string `json:"password" validate:"required"`
}

type GetSwapRsp struct {
	Size int64 `json:"size"` // unit: MB
}

type SetSwapReq struct {
	Size int64 `validate:"omitempty"` // unit: MB
}

type GetMouseJigglerRsp struct {
	Enabled bool   `json:"enabled"`
	Mode    string `json:"mode"`
}

type SetMouseJigglerReq struct {
	Enabled bool   `validate:"omitempty"`
	Mode    string `validate:"omitempty"`
}

type GetMdnsStateRsp struct {
	Enabled bool `json:"enabled"`
}

type SetHostnameReq struct {
	Hostname string `validate:"required"`
}

type GetHostnameRsp struct {
	Hostname string `json:"hostname"`
}

type SetWebTitleReq struct {
	Title string `validate:"omitempty"`
}

type GetWebTitleRsp struct {
	Title string `json:"title"`
}

type SetTlsReq struct {
	Enabled bool `validate:"omitempty"`
}

// TLSStatusRsp describes HTTPS and the certificate pair. Certificate is the
// pair named by the configuration (nil with Error when it cannot be read);
// Served is the pair the listener presents while HTTPS is on; Pending says
// the two differ.
type TLSStatusRsp struct {
	Enabled     bool                   `json:"enabled"`
	Custom      bool                   `json:"custom"`
	CertFile    string                 `json:"certFile"`
	KeyFile     string                 `json:"keyFile"`
	Certificate *utils.CertificateInfo `json:"certificate,omitempty"`
	Served      *utils.CertificateInfo `json:"served,omitempty"`
	Pending     bool                   `json:"pending"`
	Error       string                 `json:"error,omitempty"`
}

type InputRegion struct {
	Mode               string               `json:"mode"`
	FrameWidth         int                  `json:"frameWidth"`
	FrameHeight        int                  `json:"frameHeight"`
	Left               int                  `json:"left"`
	Top                int                  `json:"top"`
	Width              int                  `json:"width"`
	Height             int                  `json:"height"`
	Resolutions        []OriginalResolution `json:"resolutions,omitempty"`
	SelectedResolution string               `json:"selectedResolution"`
	Regions            []ManualRegion       `json:"regions,omitempty"`
	SelectedRegion     string               `json:"selectedRegion"`
}

type ManualRegion struct {
	FrameWidth  int `json:"frameWidth"`
	FrameHeight int `json:"frameHeight"`
	Left        int `json:"left"`
	Top         int `json:"top"`
	Width       int `json:"width"`
	Height      int `json:"height"`
}

type OriginalResolution struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

type SetInputRegionReq struct {
	Mode               string                `json:"mode"`
	FrameWidth         *int                  `json:"frameWidth,omitempty"`
	FrameHeight        *int                  `json:"frameHeight,omitempty"`
	Left               *int                  `json:"left,omitempty"`
	Top                *int                  `json:"top,omitempty"`
	Width              *int                  `json:"width,omitempty"`
	Height             *int                  `json:"height,omitempty"`
	Resolutions        *[]OriginalResolution `json:"resolutions,omitempty"`
	SelectedResolution *string               `json:"selectedResolution,omitempty"`
	Regions            *[]ManualRegion       `json:"regions,omitempty"`
	SelectedRegion     *string               `json:"selectedRegion,omitempty"`
}

type GetInputRegionRsp struct {
	InputRegion
}

type GetInputResolutionRsp struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Explicit false is valid; omitted fields are rejected.
type SetUSBInternetReq struct {
	Enabled         *bool `json:"enabled" validate:"required"`
	ExpectedEnabled *bool `json:"expectedEnabled" validate:"required"`
}
