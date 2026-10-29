package proto

type GetMCPConfigRsp struct {
	Enabled       bool   `json:"enabled"`
	APIKey        string `json:"apiKey"`
	ControlMode   string `json:"controlMode"`
	Transitioning bool   `json:"transitioning"`
	// The power, virtual_media and wake_on_lan tools are offered.
	AllowHostControl bool `json:"allowHostControl"`
}

type SetMCPConfigReq struct {
	Enabled *bool `json:"enabled" form:"enabled" validate:"required"`
}

type SetMCPHostControlReq struct {
	Enabled *bool `json:"enabled" form:"enabled" validate:"required"`
}
