package usbinternet

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

func TestIdleShellRetriesFailedCleanup(t *testing.T) {
	for _, haveApplied := range []bool{false, true} {
		t.Run(fmt.Sprint(haveApplied), func(t *testing.T) {
			r := activeTestRuntime(t)
			if err := os.Remove(filepath.Join(r.Gadget, "configs/c.1/ncm.usb0")); err != nil {
				t.Fatal(err)
			}
			bin := t.TempDir()
			nft := filepath.Join(bin, "nft")
			if err := os.WriteFile(nft, []byte("#!/bin/sh\nexit 1\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
			if haveApplied {
				if err := r.save("applied.json", applied{Forward: map[string]string{}}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Apply("", true); err == nil {
				t.Fatal("expected initial nft failure")
			}
			if s := r.ReadStatus(); s.State != "error" {
				t.Fatal(s)
			}
			if err := os.WriteFile(nft, []byte("#!/bin/sh\nprintf '%s\\n' '{\"nftables\":[]}'\n"), 0700); err != nil {
				t.Fatal(err)
			}
			// Record whether the shell requests cleanup; the test process then
			// executes the real Apply call on behalf of the command stub.
			called := filepath.Join(bin, "called")
			helper := filepath.Join(bin, "helper")
			if err := os.WriteFile(helper, []byte("#!/bin/sh\ntouch "+called+"\n"), 0700); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("sh", "../../firmware/alpine/compat/S30usbnet", "stop")
			cmd.Env = append(os.Environ(), "NANOKVM_USB_NET_RUN="+r.Run, "NANOKVM_USB_NET_GADGET="+r.Gadget,
				"NANOKVM_USB_NET_PID="+filepath.Join(bin, "legacy.pid"), "NANOKVM_USB_WATCH_SERVICE=/nonexistent",
				"NANOKVM_USB_INTERNET_HELPER="+helper)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v", out, err)
			}
			if _, err := os.Stat(called); err != nil {
				t.Fatal("shell skipped retry after failed cleanup")
			}
			if err := r.Apply("", true); err != nil {
				t.Fatal(err)
			}
			if s := r.ReadStatus(); s.State != "disabled" {
				t.Fatal(s)
			}
			if err := os.Remove(called); err != nil {
				t.Fatal(err)
			}
			env := cmd.Env
			cmd = exec.Command("sh", "../../firmware/alpine/compat/S30usbnet", "stop")
			cmd.Env = env
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("%s: %v", out, err)
			}
			if _, err := os.Stat(called); !os.IsNotExist(err) {
				t.Fatal("successful cleanup did not restore idle shortcut")
			}

		})
	}
}
