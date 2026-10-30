package standby

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/net/netutil"
	"golang.org/x/sys/unix"
)

// Takeover is only entered by BusyBox init's restart action.
func Takeover() error {
	if os.Getpid() != 1 {
		return errors.New("takeover requires PID 1")
	}
	c, err := LoadConfig(Root + "/config.json")
	if err != nil {
		return err
	}
	state := NewState(c)
	if e := shutdownResult(); e != nil {
		state.Fail(e)
	}
	signal.Ignore(syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP, syscall.SIGQUIT)
	err = leaveStorage()
	if err != nil {
		state.Fail(err)
	}
	// Even on failure, isolate the page from storage. A failed release must
	// never become ready later.
	root := Root
	if _, e := os.Stat("/nkos-standby/root/config.json"); e == nil {
		root = "/nkos-standby/root"
	}
	// A partial transition may not yet have moved the virtual filesystems.
	// devtmpfs has TMPFS_MAGIC too, so statfs cannot distinguish it here.
	if err != nil {
		for name, sentinel := range map[string]string{"proc": "self/mountinfo", "sys": "class/net", "dev": "null"} {
			if _, e := os.Stat(root + "/" + name + "/" + sentinel); e == nil {
				continue
			}
			source := "/" + name
			if root != Root {
				source = "/nkos-oldroot/" + name
			}
			if e := unix.Mount(source, root+"/"+name, "", unix.MS_BIND|unix.MS_REC, ""); e != nil {
				state.Fail(fmt.Errorf("RAM %s: %w", name, e))
			}
		}
	}
	if e := unix.Chroot(root); e != nil {
		return fmt.Errorf("RAM chroot: %w", e)
	}
	if e := os.Chdir("/"); e != nil {
		return e
	}
	var disks []Disk
	if err == nil {
		disks, err = protectDisks()
		if err != nil {
			state.Fail(fmt.Errorf("protect storage: %w", err))
		}
	}
	server := &http.Server{Handler: Handler(c, state, func() error { unix.Sync(); return unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART) }),
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 15 * time.Second, MaxHeaderBytes: 8192}
	lc := net.ListenConfig{Control: func(_, _ string, raw syscall.RawConn) error {
		var err error
		e := raw.Control(func(fd uintptr) {
			err = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, c.Interface)
		})
		if e != nil {
			return e
		}
		return err
	}}
	listener, e := lc.Listen(context.Background(), "tcp4", fmt.Sprintf(":%d", c.Port))
	if e != nil {
		return e
	}
	if len(c.Certificate) > 0 {
		cert, e := tls.X509KeyPair(c.Certificate, c.PrivateKey)
		if e != nil {
			return e
		}
		listener = tls.NewListener(listener, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}
	listener = netutil.LimitListener(listener, 32)
	go func() {
		if e := server.Serve(listener); e != nil {
			state.Fail(e)
		}
	}()
	if e = startNetwork(c, state.Fail); e != nil {
		state.Fail(e)
	}
	if c.MagicWake {
		_, e = ListenMagic(c, func(received time.Time) {
			first, e := state.MagicRestart(received)
			if e == nil && first {
				if e = unix.Reboot(unix.LINUX_REBOOT_CMD_RESTART); e != nil {
					state.Fail(e)
				}
			}
		}, state.Fail)
		if e != nil {
			state.Fail(e)
		}
	}
	if state.Get().State == "preparing" {
		for i := 0; i < 3; i++ {
			time.Sleep(time.Second)
			if e = verifyStorage(disks); e != nil {
				state.Fail(e)
				break
			}
		}
		state.Ready()
	}
	// Observe the entire session, not only a point-in-time sync.
	for {
		time.Sleep(time.Second)
		if state.Get().State == "ready" {
			if e = verifyStorage(disks); e != nil {
				state.Fail(e)
			}
		}
	}
}
