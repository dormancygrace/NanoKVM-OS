package osupdate

import (
	"os"

	"testing"
)

func TestChannelRejectsCustomAndAmbiguousRepositories(t *testing.T) {
	for _, data := range []string{"https://example.test/nanokvm/repo\n", communityOff + communityOff,
		communityOff + "https://example.test/nanokvm/repo\n", "@testing " + communityOff} {
		t.Run(data, func(t *testing.T) {
			communityFixture(t, data)
			old := apkStateDir
			apkStateDir = t.TempDir()
			t.Cleanup(func() { apkStateDir = old })
			if got := GetChannel(); got.Available || got.Channel != "custom" {
				t.Fatal("custom repository accepted")
			}
			if SetChannel("beta") == nil {
				t.Fatal("changed custom repository")
			}
			got, _ := os.ReadFile(repositoriesPath)
			if string(got) != data {
				t.Fatal("modified")
			}
		})
	}
}

// Neither name: refused, the kernel is never just removed.
