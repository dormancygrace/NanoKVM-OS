package proto

type WakeOnLANReq struct {
	Mac       string `form:"mac" validate:"required"`
	Interface string `form:"interface"`
}

type GetWolInterfacesRsp struct {
	Interfaces []string `json:"interfaces"`
}

type GetMacRsp struct {
	Macs []string `json:"macs"`
}

type DeleteMacReq struct {
	Mac string `form:"mac" validate:"required"`
}

type SetMacNameReq struct {
	Mac  string `form:"mac" validate:"required"`
	Name string `form:"name" validate:"required"`
}

type GetWifiRsp struct {
	Enabled   bool     `json:"enabled"`
	Model     string   `json:"model"`
	Bands     []string `json:"bands"`
	Band      string   `json:"band"`
	Busy      bool     `json:"busy"`
	Error     string   `json:"error"`
	Supported bool     `json:"supported"`
	ApMode    bool     `json:"apMode"`
	Connected bool     `json:"connected"`
	Ssid      string   `json:"ssid"`
	// PreferredBand affects subsequent association attempts only. Reading or
	// changing it never tears down an existing Wi-Fi connection.
	PreferredBand string `json:"preferredBand"`
	// The current association, while connected.
	Connection *WifiConnection `json:"connection,omitempty"`
}

// WifiConnection comes from wpa_supplicant's STATUS and SIGNAL_POLL. A value
// that the driver does not report is zero or empty.
type WifiConnection struct {
	Frequency  int    `json:"frequency"`            // MHz
	Channel    int    `json:"channel,omitempty"`    // IEEE channel of the frequency
	Width      string `json:"width,omitempty"`      // channel width, such as "40 MHz"
	Signal     int    `json:"signal,omitempty"`     // dBm
	LinkSpeed  int    `json:"linkSpeed,omitempty"`  // Mbit/s
	Generation int    `json:"generation,omitempty"` // Wi-Fi 4, 5, 6...
	KeyMgmt    string `json:"keyMgmt,omitempty"`    // wpa_supplicant key_mgmt, such as SAE
	IP         string `json:"ip,omitempty"`
}

type SetWifiBandPreferenceReq struct {
	PreferredBand string `json:"preferredBand" validate:"required,oneof=2.4 5"`
}

type ConnectWifiReq struct {
	Ssid     string `validate:"required"`
	Password string `validate:"required"`
}

type GetDNSRsp struct {
	Mode      string   `json:"mode"`
	Servers   []string `json:"servers"`
	Effective []string `json:"effective"`
	DHCP      []string `json:"dhcp"`
	Info      DNSInfo  `json:"info"`
}

type SetDNSReq struct {
	Mode    string   `json:"mode" validate:"required,oneof=manual dhcp"`
	Servers []string `json:"servers"`
}

type DNSInfo struct {
	Interface     string   `json:"interface"`
	Type          string   `json:"type"`
	Address       string   `json:"address"`
	SubnetMask    string   `json:"subnetMask"`
	Gateway       string   `json:"gateway"`
	SearchDomains []string `json:"searchDomains"`
}

type EthernetConfig struct {
	Enabled     bool   `json:"enabled"`
	AdminUp     bool   `json:"adminUp"`
	LinkUp      bool   `json:"linkUp"`
	Mode        string `json:"mode"`
	Interface   string `json:"interface"`
	Address     string `json:"address"`
	SubnetMask  string `json:"subnetMask"`
	Gateway     string `json:"gateway"`
	VLANEnabled bool   `json:"vlanEnabled"`
	VLANID      int    `json:"vlanId"`
	// IPv4 address (CIDR) now on eth0, or on its VLAN; empty without one.
	CurrentAddress string `json:"currentAddress,omitempty"`
}

type SetEthernetReq struct {
	Enabled     *bool  `json:"enabled" validate:"required"`
	Mode        string `json:"mode" validate:"required,oneof=dhcp static"`
	Address     string `json:"address"`
	SubnetMask  string `json:"subnetMask"`
	Gateway     string `json:"gateway"`
	VLANEnabled *bool  `json:"vlanEnabled"`
	VLANID      int    `json:"vlanId"`
}

type GatewayRoute struct {
	Interface string `json:"interface"`
	Gateway   string `json:"gateway"`
	Metric    int    `json:"metric"`
}

type GatewayPreferenceRsp struct {
	Preferred string         `json:"preferred"`
	Routes    []GatewayRoute `json:"routes"`
}

type SetGatewayPreferenceReq struct {
	Preferred string `json:"preferred" validate:"required,oneof=auto ethernet wifi"`
}
