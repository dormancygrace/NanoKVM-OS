// Package yamlconf reads server.yaml the way the server does, for the server
// and for small daemons (IPMI) that read the same file. It depends only on
// yaml and mapstructure, so a daemon importing it stays small.
package yamlconf

import (
	"strings"

	"github.com/go-viper/mapstructure/v2"
	"gopkg.in/yaml.v3"
)

// Parse reads YAML into a map with Viper's key rules, which the server's
// configuration has always followed: keys are case-insensitive and dotted
// keys are nested paths. Only keys change; values (credentials, origins) are
// kept as written. Null values are dropped.
func Parse(data []byte) (map[string]any, error) {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	return normalize(raw), nil
}

func normalize(raw map[string]any) map[string]any {
	out := make(map[string]any)
	for key, value := range raw {
		if child, ok := value.(map[string]any); ok {
			value = normalize(child)
		}
		if value != nil {
			out[strings.ToLower(key)] = value
		}
	}
	for key, value := range out {
		if !strings.Contains(key, ".") {
			continue
		}
		delete(out, key)
		parts := strings.Split(key, ".")
		node := out
		for _, part := range parts[:len(parts)-1] {
			child, ok := node[part].(map[string]any)
			if !ok {
				child = make(map[string]any)
				node[part] = child
			}
			node = child
		}
		node[parts[len(parts)-1]] = value
	}
	return out
}

// Decode stores parsed values in target with Viper's weak conversions:
// quoted numbers, numeric booleans and comma-separated lists.
func Decode(raw any, target any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result: target, WeaklyTypedInput: true,
		DecodeHook: mapstructure.StringToSliceHookFunc(","),
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
}

// Ports are the server's listening ports as server.yaml sets them.
type Ports struct {
	HTTP  int `mapstructure:"http"`
	HTTPS int `mapstructure:"https"`
}

// ReadPorts reads the ports of server.yaml content as the server does.
func ReadPorts(data []byte) (Ports, error) {
	values, err := Parse(data)
	if err != nil {
		return Ports{}, err
	}
	var ports Ports
	err = Decode(values["port"], &ports)
	return ports, err
}
