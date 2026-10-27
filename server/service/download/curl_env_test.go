package download

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestCurlCryptoEnvironment(t *testing.T) {
	const name = "NANOKVM_OPENSSL_CHACHA20"
	for _, value := range []string{"<unset>", "generic", "unknown", "", "auto"} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(name, value)
			want := value
			if value == "<unset>" {
				if err := os.Unsetenv(name); err != nil {
					t.Fatal(err)
				}
				want = "auto"
			}
			cmd := exec.Command("/bin/sh", "-c", `printf '%s' "$NANOKVM_OPENSSL_CHACHA20"`)
			cmd.Env = imageCurlEnv()
			output, err := cmd.Output()
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(output)) != want {
				t.Fatalf("child policy = %q, want %q", output, want)
			}
		})
	}
}
