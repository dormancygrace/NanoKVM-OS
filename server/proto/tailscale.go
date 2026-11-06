package proto

type TailscaleState string

const (
	TailscaleNotInstall TailscaleState = "notInstall"
	TailscaleNotRunning TailscaleState = "notRunning"
	TailscaleNotLogin   TailscaleState = "notLogin"
	TailscaleStopped    TailscaleState = "stopped"
	TailscaleRunning    TailscaleState = "running"
)

type GetTailscaleStatusRsp struct {
	State TailscaleState `json:"state"`
	// Busy: an install, uninstall or other operation is still running, so
	// the state is about to change (a page whose request timed out waits).
	Busy    bool   `json:"busy,omitempty"`
	Name    string `json:"name"`
	IP      string `json:"ip"`
	Account string `json:"account"`
	// ThisConnection: the request reached one of the device's Tailscale
	// addresses, whatever name the browser used for it.
	ThisConnection bool `json:"thisConnection,omitempty"`
}

type LoginTailscaleRsp struct {
	Url string `json:"url"`
}
