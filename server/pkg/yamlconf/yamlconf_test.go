package yamlconf

import (
	"reflect"
	"testing"
)

func TestParseNormalizesKeysOnly(t *testing.T) {
	got, err := Parse([]byte("Turn: {TurnUser: Alice, turnCred: 'Pa.SS'}\nport.HTTP: 80\nempty: ~\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"turn": map[string]any{"turnuser": "Alice", "turncred": "Pa.SS"},
		"port": map[string]any{"http": 80},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v", got)
	}
}

func TestReadPorts(t *testing.T) {
	for input, want := range map[string]Ports{
		"port: {http: 8080, https: 8443}":    {8080, 8443},
		"PORT: {Http: '80', HTTPS: '0x1BB'}": {80, 443},
		"port.http: 81":                      {HTTP: 81},
		"proto: http":                        {},
	} {
		got, err := ReadPorts([]byte(input))
		if err != nil || got != want {
			t.Errorf("%s: %+v %v, want %+v", input, got, err, want)
		}
	}
	for _, input := range []string{"port: [", "port: {http: web}", "port: 80"} {
		if _, err := ReadPorts([]byte(input)); err == nil {
			t.Errorf("%s: no error", input)
		}
	}
}
