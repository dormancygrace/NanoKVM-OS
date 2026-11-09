package network

import (
	"reflect"
	"testing"
)

func TestWolCommandUsesSeparateArguments(t *testing.T) {
	cmd := wolCommand("wlan0", "AA:BB:CC:DD:EE:FF")
	want := []string{"ether-wake", "-i", "wlan0", "-b", "AA:BB:CC:DD:EE:FF"}
	if !reflect.DeepEqual(cmd.Args, want) {
		t.Fatalf("command argv = %#v", cmd.Args)
	}
}
