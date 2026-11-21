package bmc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/pkg/bmc"
	"NanoKVM-Server/service/notify"
)

// ipmiEventsInterval is how often the daemon's events are read while its
// socket exists; while IPMI is off, only the socket is checked.
const ipmiEventsInterval = 5 * time.Second

// ipmiEvent is one event of the daemon's GET /events
// (server/internal/ipmi/events.go).
type ipmiEvent struct {
	Seq      uint64 `json:"seq"`
	Kind     string `json:"kind"`
	User     string `json:"user"`
	Address  string `json:"address"`
	Attempts int    `json:"attempts"`
	Minutes  int    `json:"minutes"`
}

type ipmiEvents struct {
	Epoch  string      `json:"epoch"`
	Latest uint64      `json:"latest"`
	Events []ipmiEvent `json:"events"`
}

// ipmiDaemon is an HTTP client of the IPMI daemon's socket.
type ipmiDaemon struct {
	socket string
	client *http.Client
}

func newIPMIDaemon(socket string, timeout time.Duration) ipmiDaemon {
	return ipmiDaemon{
		socket: socket,
		client: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
				DisableKeepAlives: true,
			},
		},
	}
}

// get decodes the data of the daemon's reply to GET path into out.
func (d ipmiDaemon) get(ctx context.Context, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://ipmi"+path, nil)
	if err != nil {
		return err
	}
	rsp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		return errors.New(rsp.Status)
	}
	body := struct {
		Code int `json:"code"`
		Data any `json:"data"`
	}{Data: out}
	if err := json.NewDecoder(http.MaxBytesReader(nil, rsp.Body, 1<<20)).Decode(&body); err != nil {
		return err
	}
	if body.Code != 0 {
		return errors.New("bad reply")
	}
	return nil
}

// ipmiEventReader follows the IPMI daemon's logins and lockouts and sends
// the security notifications the web and Redfish logins send.
type ipmiEventReader struct {
	daemon ipmiDaemon
	// epoch and after: the daemon run and the last event already handled.
	epoch string
	after uint64
	// emit sends one event (notify by default; tests replace it).
	emit func(ipmiEvent)
}

func newIPMIEventReader(socket string) *ipmiEventReader {
	return &ipmiEventReader{daemon: newIPMIDaemon(socket, 5*time.Second), emit: emitIPMIEvent}
}

func emitIPMIEvent(e ipmiEvent) {
	switch e.Kind {
	case "lockout":
		// IPMI counts every login, failed or not: its own message.
		notify.LoginAttemptsLimited(e.Address, e.Attempts, e.Minutes)
	case "login":
		// Its own list, apart from a web or Redfish user of the same name.
		notify.LoginSucceeded(e.User+" (IPMI)", e.Address)
	}
}

// WatchIPMIEvents reads the IPMI daemon's events every ipmiEventsInterval,
// for good; the router starts it once.
func WatchIPMIEvents() {
	r := newIPMIEventReader(bmc.IPMISocket)
	for {
		if _, err := os.Stat(r.daemon.socket); err == nil {
			if err := r.poll(context.Background()); err != nil {
				log.Debugf("IPMI events: %v", err)
			}
		}
		time.Sleep(ipmiEventsInterval)
	}
}

// poll handles the events since the last call. The events of a daemon run
// first seen here happened while the server was not reading them (it just
// started) and are not reported; a new run of the daemon is read from its
// start.
func (r *ipmiEventReader) poll(ctx context.Context) error {
	data, err := r.read(ctx)
	if err != nil {
		return err
	}
	switch {
	case r.epoch == "":
		r.epoch, r.after = data.Epoch, data.Latest
		return nil
	case data.Epoch != r.epoch:
		// The daemon restarted: read its events from the first one.
		r.epoch, r.after = data.Epoch, 0
		if data, err = r.read(ctx); err != nil {
			return err
		}
		if data.Epoch != r.epoch {
			return errors.New("IPMI daemon restarted again")
		}
	}
	for _, e := range data.Events {
		if e.Seq <= r.after {
			continue
		}
		r.after = e.Seq
		r.emit(e)
	}
	return nil
}

func (r *ipmiEventReader) read(ctx context.Context) (ipmiEvents, error) {
	var data ipmiEvents
	if err := r.daemon.get(ctx, "/events?after="+strconv.FormatUint(r.after, 10), &data); err != nil {
		return ipmiEvents{}, err
	}
	if data.Epoch == "" {
		return ipmiEvents{}, errors.New("bad events reply")
	}
	return data, nil
}
