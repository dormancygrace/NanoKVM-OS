package config

import "log"

var defaultConfig = &Config{
	Proto: "https",
	Host:  "",
	Port: Port{
		Http:  80,
		Https: 443,
	},
	Cert: Cert{
		Crt: "/etc/kvm/server.crt",
		Key: "/etc/kvm/server.key",
	},
	Logger: Logger{
		Level: "info",
		File:  "stdout",
	},
	JWT: JWT{
		SecretKey:            "",
		RefreshTokenDuration: 2678400,
		RevokeTokensOnLogout: true,
	},
	Stun: "stun.l.google.com:19302",
	Turn: Turn{
		TurnAddr: "",
		TurnUser: "",
		TurnCred: "",
	},
	Authentication: "enable",
	Security: Security{
		LoginLockoutDuration: 0,
		LoginMaxFailures:     5,
		TrustedProxies:       []string{"127.0.0.1/32", "::1/128"},
	},
}

func checkDefaultValue() {
	if instance.JWT.SecretKey == "" {
		secret, err := loadOrCreateJWTSecret(jwtSecretFile)
		if err != nil {
			log.Printf("failed to persist JWT secret: %s", err)
			secret = generateRandomSecretKey()
		}
		instance.JWT.SecretKey = secret
		instance.JWT.RevokeTokensOnLogout = true
	}

	if instance.JWT.RefreshTokenDuration == 0 {
		instance.JWT.RefreshTokenDuration = 2678400
	}

	if instance.Stun == "" {
		instance.Stun = "stun.l.google.com:19302"
	}

	if instance.Authentication == "" {
		instance.Authentication = "enable"
	}

	// Preserve loopback reverse-proxy support for configurations written by
	// older versions, without trusting an entire private network by default.
	if instance.Security.TrustedProxies == nil {
		instance.Security.TrustedProxies = []string{"127.0.0.1/32", "::1/128"}
	}

	instance.Hardware = getHardware()
}
