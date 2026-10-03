//go:build nanokvm_profile

package webrtc

import (
	"testing"
	"time"

	"github.com/pion/ice/v4"
	pionwebrtc "github.com/pion/webrtc/v4"
)

func TestDiagnosticICEInterfaceWhitelist(t *testing.T) {
	for _, name := range []string{"wlan0", "eth0"} {
		t.Run(name, func(t *testing.T) {
			filter := diagnosticICEInterfaceFilter(name)
			if filter == nil {
				t.Fatal("expected a filter")
			}
			if !filter(name) {
				t.Fatalf("filter rejected %q", name)
			}
			for _, other := range []string{"", "lo", "wlan1", "eth1", "eth0", "wlan0"} {
				if other != name && filter(other) {
					t.Fatalf("filter accepted unexpected interface %q", other)
				}
			}
		})
	}
}

func TestDiagnosticICEInvalidInterfaceDoesNotBlackholeGathering(t *testing.T) {
	for _, value := range []string{"", "invalid", "WLAN0"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(diagnosticICEInterfaceEnv, value)

			settingEngine := pionwebrtc.SettingEngine{}
			settingEngine.SetNetworkTypes([]pionwebrtc.NetworkType{pionwebrtc.NetworkTypeUDP4})
			settingEngine.SetIncludeLoopbackCandidate(true)
			settingEngine.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
			configureDiagnosticICEInterface(&settingEngine)

			gatherer, err := pionwebrtc.NewAPI(
				pionwebrtc.WithSettingEngine(settingEngine),
			).NewICEGatherer(pionwebrtc.ICEGatherOptions{})
			if err != nil {
				t.Fatalf("NewICEGatherer: %v", err)
			}
			defer gatherer.Close()

			done := make(chan struct{})
			candidates := make(chan *pionwebrtc.ICECandidate, 4)
			gatherer.OnLocalCandidate(func(candidate *pionwebrtc.ICECandidate) {
				if candidate == nil {
					close(done)
					return
				}
				select {
				case candidates <- candidate:
				default:
				}
			})

			if err := gatherer.Gather(); err != nil {
				t.Fatalf("Gather: %v", err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("ICE gathering did not complete")
			}
			select {
			case <-candidates:
			default:
				t.Fatal("invalid interface setting filtered every local candidate")
			}
		})
	}
}
