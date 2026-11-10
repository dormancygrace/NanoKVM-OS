module github.com/pion/dtls/v3

require (
	github.com/pion/logging v0.2.4
	github.com/pion/transport/v5 v5.1.5
	golang.org/x/crypto v0.58.0
)

require (
	go.yaml.in/yaml/v3 v3.0.6 // indirect
	golang.org/x/net v0.62.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
)

go 1.26.0

// Retract version with broken RSA interop with OpenSSL DTLS 1.2.
retract v3.1.0

// Retract version with broken interoperability with firefox.
retract v3.1.3
