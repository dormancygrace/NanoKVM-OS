package config

import (
	"bytes"
	"errors"
	"log"
	"os"
	"path/filepath"
	"sync"

	"github.com/spf13/viper"
	"gopkg.in/yaml.v3"

	"NanoKVM-Server/internal/atomicfile"
)

var (
	instance Config
	once     sync.Once
)

func GetInstance() *Config {
	once.Do(initialize)

	return &instance
}

func initialize() {
	if err := readByFile(); err != nil {
		if errors.As(err, &viper.ConfigFileNotFoundError{}) {
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

	if err := viper.Unmarshal(&instance); err != nil {
		log.Fatalf("Failed to parse configuration: %s", err)
	}

	checkDefaultValue()

	if instance.Authentication == "disable" {
		log.Println("NOTICE: Authentication is disabled! Please ensure your service is secure!")
	}

	log.Println("config loaded successfully")
}

func readByFile() error {
	viper.SetConfigName("server")
	viper.SetConfigType("yaml")
	viper.AddConfigPath("/etc/kvm/")

	return viper.ReadInConfig()
}

func readByDefault() error {
	data, err := yaml.Marshal(defaultConfig)
	if err != nil {
		log.Printf("failed to marshal default config: %s", err)
		return err
	}

	return viper.ReadConfig(bytes.NewBuffer(data))
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
	if viper.GetInt("port.http") > 0 && viper.GetInt("port.https") > 0 {
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
