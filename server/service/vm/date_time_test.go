package vm

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// useClock replaces the NTP state, the system clock and the RTC; it returns
// the times set and the number of RTC writes.
func useClock(t *testing.T, synced bool, syncErr error) (*[]time.Time, *int) {
	t.Helper()
	prevDir := deviceTime.Dir
	deviceTime.Dir = t.TempDir()
	t.Cleanup(func() { deviceTime.Dir = prevDir })
	prevSynced, prevSet, prevRTC := clockSynchronized, setSystemClock, saveClockToRTC
	t.Cleanup(func() { clockSynchronized, setSystemClock, saveClockToRTC = prevSynced, prevSet, prevRTC })
	set, rtc := &[]time.Time{}, new(int)
	clockSynchronized = func() (bool, error) { return synced, syncErr }
	setSystemClock = func(now time.Time) error { *set = append(*set, now); return nil }
	saveClockToRTC = func() error { *rtc++; return nil }
	return set, rtc
}

func postClock(t *testing.T, body string) (code int, msg string) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("POST", "/api/vm/date-time/clock", strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	(&Service{}).SetClock(c)
	var rsp struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &rsp); err != nil {
		t.Fatalf("reply %q: %v", w.Body.String(), err)
	}
	return rsp.Code, rsp.Msg
}

func TestSetClockSetsTheClockAndTheRTC(t *testing.T) {
	set, rtc := useClock(t, false, nil)
	want := time.Date(2026, 10, 30, 12, 0, 0, 123e6, time.UTC)
	if code, msg := postClock(t, `{"now":`+strconv.FormatInt(want.UnixMilli(), 10)+`}`); code != 0 {
		t.Fatalf("successful clock update: code %d %q", code, msg)
	}
	if len(*set) != 1 || !(*set)[0].Equal(want) {
		t.Fatalf("clock set to %v, want %v", *set, want)
	}
	if *rtc != 1 {
		t.Fatalf("RTC written %d times, want 1", *rtc)
	}
}

func TestSetClockLeavesASynchronisedClock(t *testing.T) {
	set, rtc := useClock(t, true, nil)
	code, msg := postClock(t, `{"now":`+strconv.FormatInt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), 10)+`}`)
	if code == 0 || !strings.Contains(msg, "NTP") || len(*set) != 0 || *rtc != 0 {
		t.Fatalf("synchronised clock: code %d %q, set %v, rtc %d", code, msg, *set, *rtc)
	}
}

func TestSetClockWithoutChronyStatus(t *testing.T) {
	// chronyc not answering is not synchronisation: the clock may be set.
	set, _ := useClock(t, false, errors.New("no chronyd"))
	postClock(t, `{"now":`+strconv.FormatInt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), 10)+`}`)
	if len(*set) != 1 {
		t.Fatalf("clock not set without chrony status")
	}
}

func TestSetClockRejectsBadTimes(t *testing.T) {
	set, _ := useClock(t, false, nil)
	for _, body := range []string{
		`{}`,
		`{"now":"soon"}`,
		`{"now":null}`,
		`{"now":1.5}`,
		`{"now":9223372036854775807}`,
		`{"now":-9223372036854775808}`,
		`{"now":9223372036854775808}`,
		`{`,
		`{"now":` + strconv.FormatInt(time.Date(2024, 12, 31, 0, 0, 0, 0, time.UTC).UnixMilli(), 10) + `}`,
		`{"now":` + strconv.FormatInt(time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli(), 10) + `}`,
	} {
		if code, _ := postClock(t, body); code == 0 {
			t.Errorf("%s accepted", body)
		}
	}
	if len(*set) != 0 {
		t.Fatalf("clock set from a bad request: %v", *set)
	}
}

func TestSetClockReportsSystemClockFailure(t *testing.T) {
	_, rtc := useClock(t, false, nil)
	setSystemClock = func(time.Time) error { return errors.New("denied") }
	code, msg := postClock(t, `{"now":1793361600000}`)
	if code == 0 || !strings.Contains(msg, "Cannot set") || *rtc != 0 {
		t.Fatalf("failed clock: code %d %q, rtc writes %d", code, msg, *rtc)
	}
}

func TestSetClockReportsRTCPersistenceFailure(t *testing.T) {
	set, _ := useClock(t, false, nil)
	saveClockToRTC = func() error { return errors.New("RTC write failed") }
	code, msg := postClock(t, `{"now":1793361600000}`)
	if code == 0 || !strings.Contains(msg, "System clock set") || !strings.Contains(msg, "RTC") || len(*set) != 1 {
		t.Fatalf("partial clock update: code %d %q, clock writes %v", code, msg, *set)
	}
}

func TestSetClockAcceptsRangeEdges(t *testing.T) {
	set, rtc := useClock(t, false, nil)
	for _, now := range []time.Time{clockMin, clockMax.Add(-time.Millisecond)} {
		code, msg := postClock(t, `{"now":`+strconv.FormatInt(now.UnixMilli(), 10)+`}`)
		if code != 0 {
			t.Fatalf("valid edge %v: code %d %q", now, code, msg)
		}
	}
	if len(*set) != 2 || *rtc != 2 {
		t.Fatalf("range edges: clock %v, rtc %d", *set, *rtc)
	}
}
