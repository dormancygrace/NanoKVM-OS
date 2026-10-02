package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v3"

	"NanoKVM-Server/internal/atomicfile"
)

const ConfigurationFile = "/etc/kvm/server.yaml"

var configurationFile = ConfigurationFile

func Read() (*Config, error) {
	data, err := os.ReadFile(configurationFile)
	if err != nil {
		log.Errorf("failed to read config: %v", err)
		return nil, err
	}

	var conf Config

	if err := yaml.Unmarshal(data, &conf); err != nil {
		log.Errorf("failed to unmarshal config: %v", err)
		return nil, err
	}

	log.Debugf("read %s successfully", configurationFile)
	return &conf, nil
}

func Write(conf *Config) error {
	data, err := yaml.Marshal(&conf)
	if err != nil {
		log.Errorf("failed to marshal config: %v", err)
		return err
	}

	if err = atomicfile.Write(configurationFile, data, 0o600); err != nil {
		log.Errorf("failed to write config: %v", err)
		return err
	}

	log.Debugf("write to %s successfully", configurationFile)
	return nil
}

// UpdateTLS sets proto and the certificate paths in server.yaml and keeps
// every other key, comment and unknown field exactly as written. Rewriting
// the whole struct would, for example, turn an absent trustedProxies into an
// explicit empty list and drop the loopback proxy default.
func UpdateTLS(proto string, cert Cert) error {
	data, err := os.ReadFile(configurationFile)
	if err != nil {
		return err
	}
	var document yaml.Node
	if err = yaml.Unmarshal(data, &document); err != nil {
		return fmt.Errorf("parse %s: %w", configurationFile, err)
	}
	if document.Kind != yaml.DocumentNode || len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return errors.New("configuration root is not a mapping")
	}
	root := document.Content[0]
	setScalar(root, "proto", proto)
	certNode := mappingChild(root, "cert")
	setScalar(certNode, "crt", cert.Crt)
	setScalar(certNode, "key", cert.Key)

	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(4)
	if err = encoder.Encode(&document); err != nil {
		return err
	}
	if err = encoder.Close(); err != nil {
		return err
	}
	return atomicfile.Write(configurationFile, out.Bytes(), 0o600)
}

func mappingChild(mapping *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			if mapping.Content[i+1].Kind != yaml.MappingNode {
				mapping.Content[i+1] = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			return mapping.Content[i+1]
		}
	}
	child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mapping.Content = append(mapping.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, child)
	return child
}

func setScalar(mapping *yaml.Node, key, value string) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
			return
		}
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
}
