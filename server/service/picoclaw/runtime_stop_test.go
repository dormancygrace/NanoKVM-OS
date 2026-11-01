package picoclaw

import (
	"testing"
)

func TestIsPicoclawGatewayCmdlineOnlyMatchesGatewaySubcommand(t *testing.T) {
	if !isPicoclawGatewayCmdline([]byte("/usr/bin/picoclaw\x00gateway\x00")) {
		t.Fatal("gateway command was not recognized")
	}
	if isPicoclawGatewayCmdline([]byte("/usr/bin/picoclaw\x00agent\x00")) {
		t.Fatal("agent command was recognized as gateway")
	}
	if isPicoclawGatewayCmdline([]byte("/usr/bin/other\x00gateway\x00")) {
		t.Fatal("other binary was recognized as picoclaw gateway")
	}
}
