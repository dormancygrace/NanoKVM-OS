package netsvc

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"NanoKVM-Server/proto"

	"github.com/gin-gonic/gin"
)

// LLDP: lldpd announces the NanoKVM to the switch it is plugged into and
// lists what the neighbours announce. With CDP on it also answers Cisco
// devices that speak only their own protocol.

const lldpConfD = "/etc/conf.d/lldpd"

var lldp = &unit{
	name:    "LLDP",
	binary:  "/usr/sbin/lldpd",
	service: "lldpd",
	pidfile: "/run/lldpd/lldpd.pid",
	configs: []string{lldpConfD},
	fresh:   func() error { return writeLLDPSettings(lldpSettings{}) },
}

type lldpSettings struct {
	// also receive CDP and answer devices that sent it
	CDP bool `json:"cdp"`
}

type lldpNeighbor struct {
	Interface       string `json:"interface"`
	Protocol        string `json:"protocol"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	Port            string `json:"port"`
	PortDescription string `json:"portDescription"`
	Address         string `json:"address"`
	Age             string `json:"age"`
}

type lldpStatus struct {
	Installed bool           `json:"installed"`
	Enabled   bool           `json:"enabled"`
	Running   bool           `json:"running"`
	Foreign   bool           `json:"foreign"`
	Settings  lldpSettings   `json:"settings"`
	Neighbors []lldpNeighbor `json:"neighbors"`
}

// The daemon's options are one line of its conf.d file.
func readLLDPSettings() lldpSettings {
	data, _ := os.ReadFile(lldpConfD)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if value, ok := strings.CutPrefix(line, "LLDPD_OPTS="); ok {
			for _, option := range strings.Fields(strings.Trim(value, `"'`)) {
				if option == "-c" {
					return lldpSettings{CDP: true}
				}
			}
		}
	}
	return lldpSettings{}
}

func writeLLDPSettings(s lldpSettings) error {
	options := ""
	if s.CDP {
		options = "-c"
	}
	return writeFile(lldpConfD, []byte(marker+"LLDPD_OPTS=\""+options+"\"\n"), 0o644, "")
}

// lldpcli's json0 format puts every value in a list of objects, whatever
// their number.
type lldpValue struct {
	Value string `json:"value"`
}

func first(values []lldpValue) string {
	if len(values) == 0 {
		return ""
	}
	return values[0].Value
}

func parseLLDPNeighbors(data []byte) []lldpNeighbor {
	var report struct {
		LLDP []struct {
			Interface []struct {
				Name    string `json:"name"`
				Via     string `json:"via"`
				Age     string `json:"age"`
				Chassis []struct {
					Name   []lldpValue `json:"name"`
					Descr  []lldpValue `json:"descr"`
					MgmtIP []lldpValue `json:"mgmt-ip"`
				} `json:"chassis"`
				Port []struct {
					ID    []lldpValue `json:"id"`
					Descr []lldpValue `json:"descr"`
				} `json:"port"`
			} `json:"interface"`
		} `json:"lldp"`
	}
	neighbors := []lldpNeighbor{}
	if json.Unmarshal(data, &report) != nil {
		return neighbors
	}
	for _, block := range report.LLDP {
		for _, entry := range block.Interface {
			neighbor := lldpNeighbor{Interface: entry.Name, Protocol: entry.Via, Age: entry.Age}
			if len(entry.Chassis) > 0 {
				chassis := entry.Chassis[0]
				neighbor.Name, neighbor.Description = first(chassis.Name), first(chassis.Descr)
				neighbor.Address = first(chassis.MgmtIP)
			}
			if len(entry.Port) > 0 {
				neighbor.Port, neighbor.PortDescription = first(entry.Port[0].ID), first(entry.Port[0].Descr)
			}
			neighbors = append(neighbors, neighbor)
		}
	}
	return neighbors
}

func lldpState(light bool) any {
	status := lldpStatus{Installed: lldp.installed(), Neighbors: []lldpNeighbor{}}
	if !status.Installed {
		return status
	}
	status.Enabled, status.Running, status.Foreign = lldp.state(light)
	status.Settings = readLLDPSettings()
	if status.Running {
		if out, err := output(5*time.Second, "lldpcli", "-f", "json0", "show", "neighbors"); err == nil {
			status.Neighbors = parseLLDPNeighbors(out)
		}
	}
	return status
}

func LLDPStatus(c *gin.Context) {
	var rsp proto.Response
	rsp.OkRspWithData(c, lldpState(c.Query("light") == "1"))
}
func LLDPEnabled(c *gin.Context) { lldp.handleEnabled(c, lldpState) }
func LLDPReset(c *gin.Context)   { lldp.handleReset(c, lldpState) }

func LLDPSettings(c *gin.Context) {
	var req lldpSettings
	var rsp proto.Response
	if err := proto.ParseFormRequest(c, &req); err != nil {
		rsp.ErrRsp(c, -1, "invalid arguments")
		return
	}
	lldp.handleSettings(c, lldpState, func() error { return writeLLDPSettings(req) })
}
