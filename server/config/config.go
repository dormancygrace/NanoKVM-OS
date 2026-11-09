package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-viper/mapstructure/v2"
	"gopkg.in/yaml.v3"

	"NanoKVM-Server/internal/atomicfile"
)

var (
	instance Config
	once     sync.Once
	values   map[string]any
)

func GetInstance() *Config {
	once.Do(initialize)

	return &instance
}

func initialize() {
	if err := readByFile(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			create()
		}

		if err = readByDefault(); err != nil {
			log.Fatalf("Failed to read default configuration!")
		}

		log.Println("using default configuration")
	}

	if err := validate(); err != nil {
		log.Fatalf("Failed to validate configuration!")
	}

	if err := decodeValues(values, &instance); err != nil {
		log.Fatalf("Failed to parse configuration: %s", err)
	}

	checkDefaultValue()

	if instance.Authentication == "disable" {
		log.Println("NOTICE: Authentication is disabled! Please ensure your service is secure!")
	}

	log.Println("config loaded successfully")
}

// Read the same YAML input and historical filename candidates as the old
// loader. SetConfigType("yaml") made even alternate extensions YAML input.
func readByFile() error {
	dir := filepath.Dir(configurationFile)
	stem := strings.TrimSuffix(filepath.Base(configurationFile), filepath.Ext(configurationFile))
	for _, suffix := range []string{".json", ".toml", ".yaml", ".yml", ".properties", ".props", ".prop", ".hcl", ".tfvars", ".dotenv", ".env", ".ini", ""} {
		path := filepath.Join(dir, stem+suffix)
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return readValues(data)
	}
	return fmt.Errorf("%s: %w", configurationFile, os.ErrNotExist)
}

func readValues(data []byte) error {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return err
	}
	values = normalizeValues(raw)
	return nil
}

// Viper treated keys case-insensitively and interpreted dots as nested paths.
// Normalize only keys; credential and origin values must remain untouched.
func normalizeValues(raw map[string]any) map[string]any {
	out := make(map[string]any)
	for key, value := range raw {
		if child, ok := value.(map[string]any); ok {
			value = normalizeValues(child)
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

// Retain Viper's weak scalar conversions (quoted ports, booleans and comma
// separated lists) without its file watching, environment and format registry.
func decodeValues(raw any, target any) error {
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result: target, WeaklyTypedInput: true,
		DecodeHook: mapstructure.StringToSliceHookFunc(","),
	})
	if err != nil {
		return err
	}
	return decoder.Decode(raw)
}

func readByDefault() error {
	data, err := yaml.Marshal(defaultConfig)
	if err != nil {
		return err
	}
	return readValues(data)
}

// Create configuration file.
func create() {
	_ = os.MkdirAll(filepath.Dir(configurationFile), 0o755)

	data, err := yaml.Marshal(defaultConfig)
	if err != nil {
		log.Printf("failed to marshal default config: %s", err)
		return
	}

	if err = atomicfile.Write(configurationFile, data, 0o600); err != nil {
		log.Printf("failed to save config: %s", err)
		return
	}

	log.Printf("create file %s with default configuration", configurationFile)
}

// Validate the configuration. This is to ensure compatibility with earlier versions.
func validate() error {
	// The ports decode as the whole file will: a value the decoder rejects
	// makes the file invalid here instead of failing the full decode later.
	var ports Port
	if decodeValues(values["port"], &ports) == nil && ports.Http > 0 && ports.Https > 0 {
		return nil
	}

	// Keep the unusable file for inspection instead of silently discarding
	// the owner's settings (authentication, ports, proxies, STUN/TURN).
	if _, err := os.Stat(configurationFile); err == nil {
		bad := configurationFile + ".bad"
		if err = os.Rename(configurationFile, bad); err != nil {
			log.Printf("failed to set aside invalid configuration: %s", err)
		} else {
			log.Printf("invalid configuration moved to %s; using defaults", bad)
		}
	}

	create()

	return readByDefault()
}
