package vm

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInstalledImageVersion(t *testing.T) {
	oldMetadata, oldLegacy := imageMetadataPath, legacyImageVersionPath
	t.Cleanup(func() { imageMetadataPath, legacyImageVersionPath = oldMetadata, oldLegacy })
	dir := t.TempDir()
	imageMetadataPath, legacyImageVersionPath = filepath.Join(dir, "image.json"), filepath.Join(dir, "ver")
	write := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := getImageVersion(); got != "" {
		t.Fatalf("missing image: %q", got)
	}
	write(legacyImageVersionPath, "2024-07-23-20-18-587710.img\n")
	if got := getImageVersion(); got != "v1.1.0" {
		t.Fatalf("legacy image: %q", got)
	}
	write(legacyImageVersionPath, "NanoKVM OS v2.0-b7\n")
	for _, invalid := range []string{`{`, `{}`, `{"bundled_application_version":"v2.5-a1"}`} {
		write(imageMetadataPath, invalid)
		if got := getImageVersion(); got != "NanoKVM OS v2.0-b7" {
			t.Fatalf("invalid metadata fallback: %q", got)
		}
		if got := readImageMetadata().BundledApplicationVersion; got != "" {
			t.Fatalf("invalid bundle: %q", got)
		}
	}
	write(imageMetadataPath, `{"image_version":"v1.0-a1","bundled_application_version":"v2.5-a1"}`)
	if got := getImageVersion(); got != "v1.0-a1" {
		t.Fatalf("independent image version: %q", got)
	}
	if got := readImageMetadata().BundledApplicationVersion; got != "v2.5-a1" {
		t.Fatalf("bundle version: %q", got)
	}
}
