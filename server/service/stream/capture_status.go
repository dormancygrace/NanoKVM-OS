package stream

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"NanoKVM-Server/common"
)

const (
	CaptureStatusEvent     = "capture-status"
	CaptureModeDirect      = "direct"
	CaptureModeH264        = "h264"
	CaptureModeMJPEG       = "mjpeg"
	CaptureSeverityError   = "error"
	CaptureSeverityWarning = "warning"
)

type CaptureStatus struct {
	Ok        bool      `json:"ok"`
	Result    int       `json:"result"`
	Message   string    `json:"message"`
	Mode      string    `json:"mode"`
	Severity  string    `json:"severity"`
	UpdatedAt time.Time `json:"updatedAt"`
	// With -6, an input refused for the video memory of this boot and the
	// video memory mode it needs.
	InputWidth  int    `json:"inputWidth,omitempty"`
	InputHeight int    `json:"inputHeight,omitempty"`
	VideoMemory string `json:"videoMemory,omitempty"`
}

// rejectedInput is replaced in tests.
var rejectedInput = common.RejectedInput

type CaptureStatusSubscriber func(CaptureStatus)

var defaultStore = newStore(time.Now)

func SubscribeCaptureStatus(subscriber CaptureStatusSubscriber) func() {
	return defaultStore.Subscribe(subscriber)
}

func UpdateCaptureStatus(mode string, result int) {
	defaultStore.UpdateCaptureStatus(mode, result)
}

func LatestCaptureStatuses() []CaptureStatus {
	return defaultStore.LatestCaptureStatuses()
}

type store struct {
	mutex            sync.Mutex
	latestByMode     map[string]CaptureStatus
	now              func() time.Time
	subscribers      map[int]CaptureStatusSubscriber
	nextSubscriberID int
}

func newStore(now func() time.Time) *store {
	return &store{
		now:          now,
		latestByMode: make(map[string]CaptureStatus),
		subscribers:  make(map[int]CaptureStatusSubscriber),
	}
}

func (s *store) Subscribe(subscriber CaptureStatusSubscriber) func() {
	s.mutex.Lock()
	id := s.nextSubscriberID
	s.nextSubscriberID++
	s.subscribers[id] = subscriber
	s.mutex.Unlock()

	return func() {
		s.mutex.Lock()
		delete(s.subscribers, id)
		s.mutex.Unlock()
	}
}

func (s *store) UpdateCaptureStatus(mode string, result int) {
	next := newCaptureStatus(mode, result, s.now())
	subscribers := s.update(next)
	for _, subscriber := range subscribers {
		subscriber(next)
	}
}

func (s *store) update(next CaptureStatus) []CaptureStatusSubscriber {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	last, ok := s.latestByMode[next.Mode]
	if ok && samePublicStatus(last, next) {
		return nil
	}

	s.latestByMode[next.Mode] = next

	subscribers := make([]CaptureStatusSubscriber, 0, len(s.subscribers))
	for _, subscriber := range s.subscribers {
		subscribers = append(subscribers, subscriber)
	}
	return subscribers
}

func (s *store) LatestCaptureStatuses() []CaptureStatus {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	modes := make([]string, 0, len(s.latestByMode))
	for mode := range s.latestByMode {
		modes = append(modes, mode)
	}
	sort.Strings(modes)

	statuses := make([]CaptureStatus, 0, len(modes))
	for _, mode := range modes {
		statuses = append(statuses, s.latestByMode[mode])
	}

	return statuses
}

func samePublicStatus(a CaptureStatus, b CaptureStatus) bool {
	if a.Ok && b.Ok {
		return true
	}

	return a.Ok == b.Ok && a.Result == b.Result && a.Mode == b.Mode &&
		a.InputWidth == b.InputWidth && a.InputHeight == b.InputHeight && a.VideoMemory == b.VideoMemory
}

func newCaptureStatus(mode string, result int, updatedAt time.Time) CaptureStatus {
	message, severity := captureResultMessage(result)
	status := CaptureStatus{
		Ok:        result >= 0,
		Result:    result,
		Message:   message,
		Mode:      mode,
		Severity:  severity,
		UpdatedAt: updatedAt,
	}
	if result == -6 {
		status.InputWidth, status.InputHeight, status.VideoMemory = rejectedInput()
		if status.VideoMemory != "" {
			status.Message = fmt.Sprintf("The HDMI input %dx%d needs the %s video memory mode",
				status.InputWidth, status.InputHeight, strings.ToUpper(status.VideoMemory))
		}
	}
	return status
}

func captureResultMessage(result int) (string, string) {
	switch result {
	case common.MjpegBlockedResult:
		return common.MjpegBlockedMessage, CaptureSeverityError
	case -7:
		return "HDMI input resolution error", CaptureSeverityError
	case -6:
		return "Unsupported HDMI resolution", CaptureSeverityError
	case -5:
		return "Retrieving image", CaptureSeverityWarning
	case -4:
		return "Changing image resolution", CaptureSeverityWarning
	case -3:
		return "Image buffer full", CaptureSeverityError
	case -2:
		return "Encoder error", CaptureSeverityError
	case -1:
		return "No image captured", CaptureSeverityError
	default:
		if result < 0 {
			return "Capture failed", CaptureSeverityError
		}
		return "", ""
	}
}
