package config

type Config struct {
	Proto          string `yaml:"proto"`
	Host           string `yaml:"host"`
	Port           Port   `yaml:"port"`
	Cert           Cert   `yaml:"cert"`
	Logger         Logger `yaml:"logger"`
	Authentication string `yaml:"authentication"`
	JWT            JWT    `yaml:"jwt"`
	// Stun lists STUN servers (host:port) separated by commas or spaces;
	// "disable" turns STUN off.
	Stun string `yaml:"stun"`
	Turn Turn   `yaml:"turn"`
	// DTLSCipher is the DTLS suite the WebRTC peers offer first: chacha20
	// (default) or aes-gcm.
	DTLSCipher string   `yaml:"dtlsCipher"`
	Security   Security `yaml:"security"`

	Hardware Hardware `yaml:"-"`
}

type Logger struct {
	Level string `yaml:"level"`
	File  string `yaml:"file"`
}

type Port struct {
	Http  int `yaml:"http"`
	Https int `yaml:"https"`
}

type Cert struct {
	Crt string `yaml:"crt"`
	Key string `yaml:"key"`
}

type JWT struct {
	SecretKey            string `yaml:"secretKey"`
	RefreshTokenDuration uint64 `yaml:"refreshTokenDuration"`
	RevokeTokensOnLogout bool   `yaml:"revokeTokensOnLogout"`
}

type Turn struct {
	TurnAddr string `yaml:"turnAddr"`
	TurnUser string `yaml:"turnUser"`
	TurnCred string `yaml:"turnCred"`
}

type Security struct {
	LoginLockoutDuration int `yaml:"loginLockoutDuration"`
	LoginMaxFailures     int `yaml:"loginMaxFailures"`
	// TrustedProxies contains IP addresses or CIDR ranges of reverse proxies
	// permitted to supply X-Forwarded-Host and X-Forwarded-Proto.
	TrustedProxies []string `yaml:"trustedProxies"`
	// AllowedOrigins permits additional browser origins for deployments whose
	// proxy cannot preserve the original Host header.
	AllowedOrigins []string `yaml:"allowedOrigins"`
}

type Hardware struct {
	Version HWVersion `yaml:"-"`
	// Board is the first-boot profile: alpha, beta, pcie, lite, or "" when
	// unknown (no ATX lines then). The ATX switch's default follows it.
	Board        string    `yaml:"-"`
	GPIOReset    string    `yaml:"-"`
	GPIOPower    string    `yaml:"-"`
	GPIOPowerLED string    `yaml:"-"`
	GPIOHDDLed   string    `yaml:"-"`
}
