package netsvc

import (
	"errors"
	"net"
	"os"
	"regexp"
	"strings"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
)

// SNMP: the net-snmp agent answers read-only SNMP v2c requests about the
// NanoKVM itself (system, interfaces, load, memory, storage) for a monitoring
// system. The package's own configuration answers "public" on the local host
// only; the one written here has a community of its own, generated at the
// first use, and listens on the network.

const (
	snmpConf = "/etc/snmp/snmpd.conf"
	snmpView = "nanokvm"
)

var snmp = &unit{
	name:     "SNMP",
	binary:   "/usr/sbin/snmpd",
	service:  "snmpd",
	pidfile:  "/run/snmpd.pid",
	configs:  []string{snmpConf},
	complete: func() bool { return ours(snmpConf) },
	fresh:    freshSNMP,
}

type snmpSettings struct {
	Community string `json:"community"`
	// who may ask: an IPv4 address or network (CIDR); empty is anyone
	Network  string `json:"network"`
	Location string `json:"location"`
	Contact  string `json:"contact"`
}

type snmpStatus struct {
	Installed bool         `json:"installed"`
	Enabled   bool         `json:"enabled"`
	Running   bool         `json:"running"`
	Foreign   bool         `json:"foreign"`
	Settings  snmpSettings `json:"settings"`
}

var (
	snmpCommunity = regexp.MustCompile(`^[A-Za-z0-9_.-]{8,32}$`)
	// one line of plain text: no control characters, no quote
	snmpText = regexp.MustCompile(`^[^\x00-\x1f\x7f"\\]{0,64}$`)
)

// source is the network as the agent takes it: an IPv4 address, or a network
// without host bits (192.168.1.7/24 becomes 192.168.1.0/24). The agent
// listens on IPv4 only.
func source(text string) (string, bool) {
	if text == "" {
		return "", true
	}
	if ip := net.ParseIP(text); ip != nil {
		return text, ip.To4() != nil
	}
	_, network, err := net.ParseCIDR(text)
	if err != nil || network.IP.To4() == nil {
		return "", false
	}
	return network.String(), true
}

func (s snmpSettings) validate() error {
	if !snmpCommunity.MatchString(s.Community) {
		return errors.New("the community is 8 to 32 letters, digits, dots, dashes or underscores")
	}
	if _, ok := source(s.Network); !ok {
		return errors.New("the network is an IPv4 address or a network such as 192.168.1.0/24")
	}
	if !snmpText.MatchString(s.Location) || !snmpText.MatchString(s.Contact) {
		return errors.New("location and contact are up to 64 characters of plain text")
	}
	return nil
}

func (s snmpSettings) file() []byte {
	source := s.Network
	if source == "" {
		source = "default"
	}
	var b strings.Builder
	b.WriteString(marker)
	b.WriteString("agentAddress udp:161\n")
	// What a monitoring system reads about a host: system, interfaces and
	// addresses, uptime, storage and processors, load and memory. Not the
	// rest of the tree: the process table shows command lines.
	for _, oid := range []string{
		".1.3.6.1.2.1.1", ".1.3.6.1.2.1.2", ".1.3.6.1.2.1.4", ".1.3.6.1.2.1.31",
		".1.3.6.1.2.1.25.1", ".1.3.6.1.2.1.25.2", ".1.3.6.1.2.1.25.3", ".1.3.6.1.4.1.2021",
	} {
		b.WriteString("view " + snmpView + " included " + oid + "\n")
	}
	b.WriteString("rocommunity " + s.Community + " " + source + " -V " + snmpView + "\n")
	// an empty line would leave the agent's own default in place
	if s.Location != "" {
		b.WriteString("sysLocation " + s.Location + "\n")
	}
	if s.Contact != "" {
		b.WriteString("sysContact " + s.Contact + "\n")
	}
	// a host: applications and end-to-end services
	b.WriteString("sysServices 72\n")
	return []byte(b.String())
}

func readSNMPSettings() snmpSettings {
	var s snmpSettings
	data, _ := os.ReadFile(snmpConf)
	if !strings.HasPrefix(string(data), marker) {
		return s
	}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, _ := strings.Cut(line, " ")
		switch key {
		case "rocommunity":
			if fields := strings.Fields(value); len(fields) >= 2 {
				s.Community = fields[0]
				if fields[1] != "default" {
					s.Network = fields[1]
				}
			}
		case "sysLocation":
			s.Location = value
		case "sysContact":
			s.Contact = value
		}
	}
	return s
}

func freshSNMP() error {
	community, err := secret(16)
	if err != nil {
		return err
	}
	return writeFile(snmpConf, snmpSettings{Community: community}.file(), 0o600, "")
}

func snmpState(light bool) any {
	status := snmpStatus{Installed: snmp.installed()}
	if !status.Installed {
		return status
	}
	status.Enabled, status.Running, status.Foreign = snmp.state(light)
	status.Settings = readSNMPSettings()
	return status
}

func SNMPStatus(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, snmpState(c.Query("light") == "1"))
}
func SNMPEnabled(c *gin.Context) { snmp.handleEnabled(c, snmpState) }
func SNMPReset(c *gin.Context)   { snmp.handleReset(c, snmpState) }

func SNMPSettings(c *gin.Context) {
	var req snmpSettings
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	req.Location, req.Contact = strings.TrimSpace(req.Location), strings.TrimSpace(req.Contact)
	req.Network = strings.TrimSpace(req.Network)
	if err := req.validate(); err != nil {
		rsp.ErrRsp(c, -4, err.Error())
		return
	}
	req.Network, _ = source(req.Network)
	snmp.handleSettings(c, snmpState, func() error { return writeFile(snmpConf, req.file(), 0o600, "") })
}
