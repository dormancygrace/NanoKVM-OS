package apkrollback

import (
	"encoding/json"
	"os"
	"testing"
)

// Export production commands for the real APK fixture (run by CI in Alpine).
// Python must not maintain its own slightly different rollback command line.
func TestExportIntegrationCommands(t *testing.T) {
	path := os.Getenv("NKOS_ROLLBACK_COMMANDS")
	if path == "" {
		t.Skip("integration fixture export not requested")
	}
	var targets []string
	for _, n := range CorePackages {
		targets = append(targets, n+"=3.3.4-r0")
	}
	// A stable release from before a core package (3.3.5 here, in the
	// fixture) has the other packages and the legacy targets for that one.
	var legacy []string
	for _, n := range CorePackages {
		if n == "nanokvm-tpu" {
			legacy = append(legacy, LegacyTargets(n)...)
		} else {
			legacy = append(legacy, n+"=3.3.5-r0")
		}
	}
	data, err := json.Marshal(map[string]any{"core": CorePackages,
		"query": Query("@REPOSITORY@"), "install": Install(targets), "install_without_tpu": Install(legacy),
		"recovery": string(RecoveryService)})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
