package hostsensors

import (
	"strings"
	"testing"
)

func TestParseView(t *testing.T) {
	key := Key(Sensor{Hardware: "CPU", Type: "cpu", Name: "Core", Kind: "temperature"})
	v, err := ParseView([]byte(`{"overrides":{"` + strings.ReplaceAll(key, "\t", `\t`) + `":false}}`))
	if err != nil || len(v.Overrides) != 1 || v.Overrides[key] {
		t.Fatalf("view %+v, %v", v, err)
	}
	if v, err = ParseView([]byte(`{}`)); err != nil || v.Overrides == nil {
		t.Fatalf("empty view: %+v, %v", v, err)
	}
	for name, bad := range map[string]string{
		"not json":   `x`,
		"not a key":  `{"overrides":{"CPU":true}}`,
		"long key":   `{"overrides":{"a\tb\tc\t` + strings.Repeat("x", maxViewKey) + `":true}}`,
		"not a bool": `{"overrides":{"a\tb\tc\td":"yes"}}`,
	} {
		if _, err := ParseView([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
