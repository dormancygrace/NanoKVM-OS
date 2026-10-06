//go:build nanokvm_profile

package webrtc

import (
	"os"

	pionwebrtc "github.com/pion/webrtc/v4"
)

const diagnosticICEInterfaceEnv = "NANOKVM_WEBRTC_ICE_INTERFACE"

func diagnosticICEInterfaceFilter(name string) func(string) bool {
	switch name {
	case "wlan0", "eth0":
		return func(interfaceName string) bool {
			return interfaceName == name
		}
	default:
		return nil
	}
}

func configureDiagnosticICEInterface(settingEngine *pionwebrtc.SettingEngine) {
	if settingEngine == nil {
		return
	}

	name := os.Getenv(diagnosticICEInterfaceEnv)
	if filter := diagnosticICEInterfaceFilter(name); filter != nil {
		settingEngine.SetInterfaceFilter(filter)
	}
}
