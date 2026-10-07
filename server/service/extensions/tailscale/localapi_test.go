package tailscale

import (
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func serveLocalAPI(t *testing.T, handler http.HandlerFunc) string {
	t.Helper()
	socket := filepath.Join(t.TempDir(), "tailscaled.sock")
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Close() })
	return socket
}

func useLocalAPI(t *testing.T, socket string, cli func() (*TsStatus, error)) {
	t.Helper()
	previousSocket, previousCLI := localAPISocket, cliStatus
	localAPISocket, cliStatus = socket, cli
	t.Cleanup(func() { localAPISocket, cliStatus = previousSocket, previousCLI })
}

func TestStatusReadsTheLocalAPI(t *testing.T) {
	socket := serveLocalAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "local-tailscaled.sock" || r.URL.Path != "/localapi/v0/status" || r.Method != http.MethodGet {
			http.Error(w, "invalid localapi request", http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(`{"Version":"1.90.0","BackendState":"Running","Self":{"HostName":"nanokvm","TailscaleIPs":["100.64.0.9","fd7a::9"]},"CurrentTailnet":{"Name":"example.ts.net"}}`))
	})
	useLocalAPI(t, socket, func() (*TsStatus, error) { t.Fatal("client started"); return nil, nil })
	status, err := NewCli().Status()
	if err != nil || status.BackendState != "Running" || status.Self.HostName != "nanokvm" || len(status.Self.TailscaleIPs) != 2 || status.CurrentTailnet.Name != "example.ts.net" {
		t.Fatalf("%+v, %v", status, err)
	}
}

func TestStatusWithoutDaemonDoesNotStartTheClient(t *testing.T) {
	socket := filepath.Join(t.TempDir(), "tailscaled.sock")
	useLocalAPI(t, socket, func() (*TsStatus, error) { t.Fatal("client started"); return nil, nil })
	if _, err := NewCli().Status(); !errors.Is(err, errDaemonUnreachable) {
		t.Fatalf("missing socket: %v", err)
	}
	// A socket left behind by a stopped daemon refuses connections.
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	listener.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = listener.Close()
	if _, err := os.Stat(socket); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCli().Status(); !errors.Is(err, errDaemonUnreachable) {
		t.Fatalf("stale socket: %v", err)
	}
}

func TestStatusFallsBackToTheClient(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"refused": func(w http.ResponseWriter, r *http.Request) { http.Error(w, "denied", http.StatusForbidden) },
		"invalid": func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("not json")) },
	} {
		t.Run(name, func(t *testing.T) {
			fromClient := &TsStatus{BackendState: "NeedsLogin"}
			useLocalAPI(t, serveLocalAPI(t, handler), func() (*TsStatus, error) { return fromClient, nil })
			if status, err := NewCli().Status(); err != nil || status != fromClient {
				t.Fatalf("%+v, %v", status, err)
			}
		})
	}
}
