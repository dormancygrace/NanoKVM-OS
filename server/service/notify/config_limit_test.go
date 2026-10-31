package notify

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"NanoKVM-Server/recovery"
)

// All fields satisfy the individual limits; JSON escaping can still make
// their combined file too large for a settings backup.
func TestConfigureKeepsSettingsWhenEncodedConfigExceedsRecoveryLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newService(path)
	if err := s.Configure(Settings{Language: "en"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := Settings{Language: "ru"}
	for i := 0; i < maxTemplates; i++ {
		messages := map[string]Message{}
		for _, spec := range MessageSpecs {
			messages[spec.Key] = Message{Body: strings.Repeat("&", maxBodyBytes)}
		}
		settings.Templates = append(settings.Templates, Template{
			ID: fmt.Sprintf("t%d", i), Name: fmt.Sprintf("Template %d", i), Messages: messages,
		})
	}
	if err := s.Configure(settings); err == nil {
		after, _ := os.ReadFile(path)
		t.Fatalf("accepted encoded config of %d bytes (recovery limit %d)", len(after), recovery.MaxFile)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) || s.Status().Language != "en" || len(s.Status().Templates) != 0 {
		t.Fatal("rejected configuration replaced stored or active settings")
	}
}

// The cap applies to encoded bytes, not the number of template fields.
func TestMaximumPlainTemplatesRemainRecoverable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newService(path)
	settings := Settings{Language: "en"}
	for i := 0; i < maxTemplates; i++ {
		messages := map[string]Message{}
		for _, spec := range MessageSpecs {
			messages[spec.Key] = Message{
				Title: strings.Repeat("T", maxTitleBytes),
				Body:  strings.Repeat("b", maxBodyBytes),
			}
		}
		settings.Templates = append(settings.Templates, Template{
			ID: fmt.Sprintf("t%d", i), Name: fmt.Sprintf("Template %d", i), Messages: messages,
		})
	}
	if err := s.Configure(settings); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) > recovery.MaxFile {
		t.Fatalf("config has %d bytes, recovery limit %d", len(data), recovery.MaxFile)
	}
}
