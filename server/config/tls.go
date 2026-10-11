package config

// The certificate pair the server generates on first boot. A configuration
// that names another pair uses a custom certificate.
const (
	DefaultCertFile = "/etc/kvm/server.crt"
	DefaultKeyFile  = "/etc/kvm/server.key"

	// The pair uploaded from the settings page.
	CustomCertFile = "/etc/kvm/tls/custom.crt"
	CustomKeyFile  = "/etc/kvm/tls/custom.key"

	// The pair acme.sh issues and renews.
	AcmeCertFile = "/etc/kvm/tls/acme.crt"
	AcmeKeyFile  = "/etc/kvm/tls/acme.key"
)

// WithDefaults fills unset paths with the generated pair.
func (c Cert) WithDefaults() Cert {
	if c.Crt == "" {
		c.Crt = DefaultCertFile
	}
	if c.Key == "" {
		c.Key = DefaultKeyFile
	}
	return c
}
