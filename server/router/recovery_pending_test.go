package router

import "testing"

// While settings recovery waits for its reboot, both login steps stay open:
// a user with a second factor must be able to sign in to retry or reboot.
func TestRecoveryPendingAllowsBothLoginSteps(t *testing.T) {
	for _, path := range []string{"/api/auth/login", "/api/auth/login/totp", "/api/auth/logout", "/api/system/recovery/reboot"} {
		if !allowedDuringRecovery[path] {
			t.Errorf("%s is blocked while recovery is pending", path)
		}
	}
	for _, path := range []string{"/api/auth/totp/disable", "/api/auth/password", "/api/auth/users"} {
		if allowedDuringRecovery[path] {
			t.Errorf("%s is allowed while recovery is pending", path)
		}
	}
}
