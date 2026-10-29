package kvmcapture

import (
	"context"
	"time"

	"NanoKVM-Server/pkg/bmc"
	mcpservice "NanoKVM-Server/service/mcp"
	"NanoKVM-Server/service/vm"

	log "github.com/sirupsen/logrus"
)

// power runs MCP's power actions as Redfish and IPMI do: the same buttons,
// LED rules, forced-off hold (System > Redfish and IPMI) and busy lock.
type power struct {
	configPath string
}

func NewPower() mcpservice.PowerControl { return power{configPath: bmc.ConfigPath} }

func (p power) Do(ctx context.Context, action string) error {
	var hold time.Duration
	if config, err := bmc.Load(p.configPath); err != nil {
		log.Errorf("BMC settings: %v", err)
	} else {
		hold = time.Duration(config.HoldSeconds) * time.Second // 0: the long press
	}
	return (&bmc.Power{ATX: vm.ATX{}, Hold: hold}).Do(ctx, action)
}
