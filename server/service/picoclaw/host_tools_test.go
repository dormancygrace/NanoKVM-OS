package picoclaw

import (
	"testing"
)

func toolSetting(raw map[string]any, path ...string) any {
	var current any = raw
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil
		}
		current = object[key]
	}
	return current
}

// The agent's tools on the NanoKVM itself are off unless the administrator
// turns them on: the shell, file writes and scheduled commands, and the
// agent stays in its workspace with the shell's deny patterns on.
func TestHostToolsAreOffByDefault(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		editor := &picoclawConfigEditor{raw: map[string]any{
			// An earlier NanoKVM wrote the permissive values.
			"tools":  map[string]any{"exec": map[string]any{"enable_deny_patterns": false}},
			"agents": map[string]any{"defaults": map[string]any{"restrict_to_workspace": false}},
		}}
		for _, entry := range picoclawHostToolDefaults(enabled) {
			editor.setValue(entry.value, entry.path...)
		}
		for _, path := range [][]string{
			{"tools", "exec", "enabled"}, {"tools", "exec", "allow_remote"},
			{"tools", "write_file", "enabled"}, {"tools", "edit_file", "enabled"},
			{"tools", "append_file", "enabled"}, {"tools", "cron", "allow_command"},
			{"agents", "defaults", "allow_read_outside_workspace"},
		} {
			if got := toolSetting(editor.raw, path...); got != enabled {
				t.Errorf("host tools %v: %v = %v", enabled, path, got)
			}
		}
		for _, path := range [][]string{{"tools", "exec", "enable_deny_patterns"}, {"agents", "defaults", "restrict_to_workspace"}} {
			if got := toolSetting(editor.raw, path...); got != !enabled {
				t.Errorf("host tools %v: %v = %v", enabled, path, got)
			}
		}
	}
}
