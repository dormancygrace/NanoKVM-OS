package hostsensors

import (
	"strings"
	"testing"
	"time"
)

const good = `{"v":1,"host":"PC","os":"Windows 11","agent":"nanokvm-sensors/1","interval":5,"sensors":[` +
	`{"hw":"CPU","type":"cpu","name":"Core","kind":"temperature","value":51.5},` +
	`{"hw":"Board","type":"mystery","name":"Fan 1","kind":"fan","value":900},` +
	`{"hw":"Board","type":"motherboard","name":"New","kind":"from-a-newer-agent","value":1}]}`

// What the computer sends is not trusted: the checks keep what is shown.
func TestParseKeepsOnlyCheckedReadings(t *testing.T) {
	r, err := Parse([]byte(good))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Sensors) != 2 || r.Sensors[1].Type != "other" || r.Sensors[0].Value != 51.5 {
		t.Fatalf("sensors: %+v", r.Sensors)
	}
	for name, bad := range map[string]string{
		"version":   strings.Replace(good, `"v":1`, `"v":2`, 1),
		"control":   strings.Replace(good, `"Core"`, `"Co\u0007re"`, 1),
		"long name": strings.Replace(good, `"Core"`, `"`+strings.Repeat("x", maxText+1)+`"`, 1),
		"no name":   strings.Replace(good, `"Core"`, `" "`, 1),
		"interval":  strings.Replace(good, `"interval":5`, `"interval":0`, 1),
		"trailing":  good + `{}`,
		"not json":  `login: `,
		"too long":  good + strings.Repeat(" ", MaxReportBytes),
	} {
		if _, err := Parse([]byte(bad)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestStoreStaleAndRejected(t *testing.T) {
	var s Store
	now := time.Unix(1000, 0)
	if err := s.Accept([]byte(good), "serial", now); err != nil {
		t.Fatal(err)
	}
	if s.Accept([]byte("x"), "serial", now) == nil {
		t.Fatal("accepted garbage")
	}
	st := s.Status(now.Add(14 * time.Second))
	if st.Report == nil || st.Stale || st.Transport != "serial" || st.Rejected != 1 || st.Age != 14000 {
		t.Fatalf("fresh: %+v", st)
	}
	if st = s.Status(now.Add(16 * time.Second)); !st.Stale {
		t.Fatalf("not stale after three intervals: %+v", st)
	}
	s.Clear()
	if st = s.Status(now); st.Report != nil || st.Transport != "" {
		t.Fatalf("after clear: %+v", st)
	}
}
