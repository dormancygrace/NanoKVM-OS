package ws

import (
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"NanoKVM-Server/service/hid"
	"NanoKVM-Server/service/inputcontrol"
)

type Manager struct {
	controlMutex    sync.Mutex // Serialize ownership changes and their notifications.
	clients         map[*websocket.Conn]*Client
	controller      *Client
	externalLease   string
	externalRelease func()
	externalCleanup bool
	mutex           sync.RWMutex
}

type Client struct {
	ws                 *websocket.Conn
	hid                *hid.Hid
	manual             *inputcontrol.ManualSession
	keyboard           chan hid.QueuedReport
	mouse              chan hid.QueuedReport
	heartbeatTimeout   time.Duration
	lastHeartbeat      time.Time
	mutex              sync.Mutex
	keyboardLedMutex   sync.Mutex
	keyboardLedStatus  *hid.KeyboardLedStatus
	keyboardLedNotify  chan struct{}
	keyboardLedDone    chan struct{}
	keyboardLedClosed  bool
	keyboardLedOnce    sync.Once
	keyboardLedWorkers sync.WaitGroup
	closeOnce          sync.Once
	workers            sync.WaitGroup
	controlEnabled     bool
	manualViewOnly     bool // Protected by Manager.mutex; explicit opt-out from automatic control.
	// inputLease is a random secret of this socket. While the socket owns
	// input control it is sent only to this browser tab, which presents it
	// on HTTP input routes (paste, ATX) to prove it is the controller.
	inputLease string
}

type Message struct {
	Type string `json:"type"`
	Data string `json:"data"`
}
