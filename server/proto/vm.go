package proto

type IP struct {
	Name    string `json:"name"`
	Addr    string `json:"addr"`
	Version string `json:"version"`
	Type    string `json:"type"`
}

type GetInfoRsp struct {
	IPs         []IP   `json:"ips"`
	Mdns        string `json:"mdns"`
	Image       string `json:"image"`
	Application string `json:"application"`
	DeviceKey   string `json:"deviceKey"`
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
	Type  string `validate:"required"` // resolution / fps / quality
	Value int    `validate:"number"`   // value
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
	Keyboard bool                       `json:"keyboard"`
	Relative bool                       `json:"relative"`
	Absolute bool                       `json:"absolute"`
	Revision string                     `json:"revision"`
	Network  bool                       `json:"network"`
	Media    bool                       `json:"media"`
	Disk     bool                       `json:"disk"`
	Serial   bool                       `json:"serial"`
	Audio    bool                       `json:"audio"`
	HID      bool                       `json:"hid"`
	Mode     string                     `json:"mode"`
	Budget   USBEndpointBudget          `json:"budget"`
	Costs    map[string]USBEndpointCost `json:"costs"`
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
	Keyboard *bool  `json:"keyboard" validate:"required"`
	Relative *bool  `json:"relative" validate:"required"`
	Absolute *bool  `json:"absolute" validate:"required"`
	Network  *bool  `json:"network" validate:"required"`
	Disk     *bool  `json:"disk" validate:"required"`
	Serial   *bool  `json:"serial" validate:"required"`
	Audio    *bool  `json:"audio" validate:"required"`
	Mode     string `json:"mode" validate:"required,oneof=normal hid-only"`
	Revision string `json:"revision" validate:"required"`
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
