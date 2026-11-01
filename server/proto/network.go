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
	// AccessPoint: the administrator's access point is up (not the setup one
	// of ApMode); Wi-Fi joins no network meanwhile.
	AccessPoint bool `json:"accessPoint"`
	// With AccessPoint: the devices associated with it; Ssid is its name.
	APStations int    `json:"apStations"`
	Connected  bool   `json:"connected"`
	Ssid       string `json:"ssid"`
	// PreferredBand affects subsequent association attempts only. Reading or
	// changing it never tears down an existing Wi-Fi connection.
	PreferredBand string `json:"preferredBand"`
	// The current association, while connected.
	Connection *WifiConnection `json:"connection,omitempty"`
	// ThisConnection: the request reached the device over this interface.
	ThisConnection bool `json:"thisConnection"`
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

// WifiAP: the administrator's Wi-Fi access point. Password is write-only:
// empty in a request keeps the stored one.
type WifiAP struct {
	Enabled  bool   `json:"enabled"`
	SSID     string `json:"ssid"`
	Password string `json:"password,omitempty"`
	Band     string `json:"band"`
	// 0: chosen by a scan when the access point starts.
	Channel int  `json:"channel"`
	Hidden  bool `json:"hidden"`
	// Three octets of a private /24; empty: derived from the serial number.
	Subnet string `json:"subnet"`
	// Clients reach the internet through the device (masqueraded).
	Internet bool `json:"internet"`
	// The DNS servers handed to its clients with internet (IPv4, up to
	// three); empty: the device's own.
	DNS []string `json:"dns"`
}

type WifiAPRsp struct {
	WifiAP
	PasswordSet bool            `json:"passwordSet"`
	Running     bool            `json:"running"`
	Address     string          `json:"address"`
	Stations    []WifiAPStation `json:"stations"`
	// The device's DNS servers, which its clients get without a list.
	DeviceDNS []string `json:"deviceDns"`
}

// A device on the access point. IP and Hostname are empty until it has a
// lease; Signal is in dBm, Connected in seconds, Rate (the last transmit
// rate to it) in Mbit/s. No byte counts: aic8800 reports the whole
// interface's for every station.
type WifiAPStation struct {
	MAC       string  `json:"mac"`
	IP        string  `json:"ip,omitempty"`
	Hostname  string  `json:"hostname,omitempty"`
	Signal    int     `json:"signal,omitempty"`
	Connected int     `json:"connected"`
	Rate      float64 `json:"rate,omitempty"`
}
