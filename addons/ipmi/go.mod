module NanoKVM-IPMI

go 1.26.10

require NanoKVM-Server v0.0.0

require (
	github.com/go-viper/mapstructure/v2 v2.5.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace NanoKVM-Server => ../../server
