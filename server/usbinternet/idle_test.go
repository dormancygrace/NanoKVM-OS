package usbinternet

import (
	"encoding/json"
	"os"
	"regexp"
	"testing"
)

// S30usbnet skips the helper while applied.json holds exactly the empty state
// Apply records when it has nothing to do. Keep the two in step.
func TestIdleAppliedMatchesS30usbnet(t *testing.T) {
	data, err := json.Marshal(applied{Forward: map[string]string{}})
	if err != nil {
		t.Fatal(err)
	}
	script, err := os.ReadFile("../../firmware/alpine/compat/S30usbnet")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^idle_applied='(.*)'$`).FindSubmatch(script)
	if m == nil {
		t.Fatal("idle_applied not found in S30usbnet")
	}
	if string(m[1]) != string(data) {
		t.Fatalf("S30usbnet idle form %s, helper writes %s", m[1], data)
	}
}
