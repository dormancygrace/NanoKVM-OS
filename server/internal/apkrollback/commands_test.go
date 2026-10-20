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
	data, err := json.Marshal(map[string]any{"core": CorePackages,
		"query": Query("@REPOSITORY@"), "install": Install(targets),
		"recovery": string(RecoveryService)})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
}
