//go:build ignore

// QEMU-only harness. It does not add a production bypass for preflight checks.
package main

import (
	"NanoKVM-Server/standby"
	"bytes"
	"fmt"
	"golang.org/x/sys/unix"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"
)

func main() {
	if _, e := os.Stat("/test-once"); e != nil {
		for _, path := range []string{"/writer.log", "/boot/writer.log", "/data/writer.log", "/m/writer.log"} {
			b, e := os.ReadFile(path)
			if e != nil || !bytes.Contains(b, []byte("stopped")) {
				fmt.Println("STANDBY_TEST_ERROR lost final writer data", path, e)
				return
			}
		}
		b, e := os.ReadFile("/mapped")
		if e != nil || !bytes.Equal(b, bytes.Repeat([]byte{0x5a}, 1<<20)) {
			fmt.Println("STANDBY_TEST_ERROR lost mmap data", e)
			return
		}
		fmt.Println("STANDBY_TEST_NORMAL_BOOT")
		http.ListenAndServe(":80", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "normal boot") }))
		return
	}
	b, _ := os.ReadFile("/proc/mounts")
	if !strings.Contains(string(b), " /boot vfat ") || !strings.Contains(string(b), " /data exfat ") {
		out, _ := exec.Command("dmesg").CombinedOutput()
		fmt.Println("STANDBY_TEST_ERROR missing volume mounts", string(b), string(out))
		return
	}
	os.Remove("/test-once")
	c := standby.Config{Address: "10.0.2.15", ClientAddress: "10.0.2.2", Port: 80, MagicWake: true}
	os.Remove("/kvmapp/.os-update/lock")
	os.Remove("/kvmapp/.os-update")
	fresh, e := standby.Prepare(c)
	if e != nil {
		fmt.Println("STANDBY_TEST_ERROR first-use preparation", e)
		return
	}
	fresh.Close()
	// Refusal must leave the running system usable and remove only our
	// staging directory; verify all existing cross-process ownership locks.
	for _, path := range []string{"/kvmapp/.os-update/lock", "/run/nanokvm-apk.lock", "/lib/apk/db/lock"} {
		f, e := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
		if e != nil {
			fmt.Println("STANDBY_TEST_ERROR", e)
			return
		}
		if e = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); e != nil {
			fmt.Println("STANDBY_TEST_ERROR", e)
			return
		}
		refused, e := standby.Prepare(c)
		f.Close()
		if e == nil {
			refused.Close()
			fmt.Println("STANDBY_TEST_ERROR ignored busy lock", path)
			return
		}
		if _, e = os.Stat(standby.Directory); !os.IsNotExist(e) {
			fmt.Println("STANDBY_TEST_ERROR preparation leaked", e)
			return
		}
	}
	if e := os.WriteFile("/run/nanokvm-transfer-in-progress", []byte("owned by transfer"), 0600); e != nil {
		fmt.Println("STANDBY_TEST_ERROR", e)
		return
	}
	refused, err := standby.Prepare(c)
	b, readErr := os.ReadFile("/run/nanokvm-transfer-in-progress")
	if err == nil || readErr != nil || string(b) != "owned by transfer" {
		if refused != nil {
			refused.Close()
		}
		fmt.Println("STANDBY_TEST_ERROR ignored/damaged transfer lock")
		return
	}
	os.Remove("/run/nanokvm-transfer-in-progress")
	fmt.Println("STANDBY_TEST_PREFLIGHT_REFUSALS")
	f, e := os.OpenFile("/mapped", os.O_CREATE|os.O_RDWR, 0600)
	if e != nil {
		fmt.Println("STANDBY_TEST_ERROR", e)
		return
	}
	if e = f.Truncate(1 << 20); e != nil {
		fmt.Println("STANDBY_TEST_ERROR", e)
		return
	}
	mapped, e := unix.Mmap(int(f.Fd()), 0, 1<<20, unix.PROT_READ|unix.PROT_WRITE, unix.MAP_SHARED)
	if e != nil {
		fmt.Println("STANDBY_TEST_ERROR", e)
		return
	}
	copy(mapped, bytes.Repeat([]byte{0x5a}, len(mapped)))
	p, e := standby.Prepare(c)
	if e != nil {
		fmt.Println("STANDBY_TEST_ERROR", e)
		return
	}
	defer p.Close()
	fmt.Println("STANDBY_TEST_TOKEN", p.Config.Token)
	if b, _ := os.ReadFile("/test-mode"); string(b) == "held-mount" || string(b) == "hidden-mount" {
		point := "/dev/held"
		if string(b) == "hidden-mount" {
			point = "/run/held"
		}
		os.Mkdir(point, 0700)
		if out, e := exec.Command("/bin/busybox", "mount", "--bind", "/", point).CombinedOutput(); e != nil {
			fmt.Println("STANDBY_TEST_ERROR", e, string(out))
			return
		}
	}
	if e = p.Arm(); e != nil {
		fmt.Println("STANDBY_TEST_ERROR", e)
		return
	}
	for {
		time.Sleep(time.Hour)
	}
}
