package proto

type ZerotierState string

const (
	ZerotierNotInstall ZerotierState = "notInstall"
	ZerotierNotRunning ZerotierState = "notRunning"
	// The service runs but has not reached the root servers yet.
	ZerotierConnecting ZerotierState = "connecting"
	ZerotierRunning    ZerotierState = "running"
)

type ZerotierNetwork struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Status    string   `json:"status"`
	Type      string   `json:"type"`
	Device    string   `json:"device"`
	Addresses []string `json:"addresses"`
}

type GetZerotierStatusRsp struct {
	State    ZerotierState     `json:"state"`
	Address  string            `json:"address,omitempty"`
	IP       string            `json:"ip,omitempty"`
	Version  string            `json:"version,omitempty"`
	Networks []ZerotierNetwork `json:"networks,omitempty"`
}

type ZerotierNetworkReq struct {
	Network string `json:"network"`
}
