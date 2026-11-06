package bmc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	log "github.com/sirupsen/logrus"

	"NanoKVM-Server/service/addons"
	"NanoKVM-Server/service/notify"
)

// ipmiEventsInterval is how often the add-on's events are read while its
// socket exists; without the add-on running, only the socket is checked.
const ipmiEventsInterval = 5 * time.Second

// ipmiEvent is one event of the add-on's GET /events
// (addons/ipmi/internal/ipmi/events.go).
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

// ipmiEventReader follows the IPMI add-on's logins and lockouts and sends
// the security notifications the web and Redfish logins send.
type ipmiEventReader struct {
	socket string
	client *http.Client
	// epoch and after: the daemon run and the last event already handled.
	epoch string
	after uint64
	// emit sends one event (notify by default; tests replace it).
	emit func(ipmiEvent)
}

func newIPMIEventReader(socket string) *ipmiEventReader {
	return &ipmiEventReader{
		socket: socket,
		client: &http.Client{
			Timeout: 5 * time.Second,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
				DisableKeepAlives: true,
			},
		},
		emit: emitIPMIEvent,
	}
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

// WatchIPMIEvents reads the IPMI add-on's events every ipmiEventsInterval, for
// good; the router starts it once.
func WatchIPMIEvents() {
	r := newIPMIEventReader(filepath.Join(addons.SocketDir, "ipmi.sock"))
	for {
		if _, err := os.Stat(r.socket); err == nil {
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"http://addon/events?after="+strconv.FormatUint(r.after, 10), nil)
	if err != nil {
		return ipmiEvents{}, err
	}
	rsp, err := r.client.Do(req)
	if err != nil {
		return ipmiEvents{}, err
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != http.StatusOK {
		// An add-on from before GET /events answers 404.
		return ipmiEvents{}, errors.New(rsp.Status)
	}
	var body struct {
		Code int        `json:"code"`
		Data ipmiEvents `json:"data"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(nil, rsp.Body, 1<<20)).Decode(&body); err != nil {
		return ipmiEvents{}, err
	}
	if body.Code != 0 || body.Data.Epoch == "" {
		return ipmiEvents{}, errors.New("bad events reply")
	}
	return body.Data, nil
}
