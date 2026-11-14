package hostsensors

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// Which readings the page shows is the administrator's choice, kept on the
// NanoKVM so every browser shows the same. A reading is shown by default
// when its kind is a main one (the page decides: temperatures, fans,
// voltages, power); View holds only the exceptions, by the reading's key.
// The agent keeps sending everything: hiding is not a setting of the agent.
type View struct {
	// Overrides: true shows a reading that is hidden by default, false
	// hides one that is shown by default.
	Overrides map[string]bool `json:"overrides"`
}

const (
	// MaxViewEntries bounds the exceptions: more than a report has readings,
	// as a part that was unplugged keeps its choices.
	MaxViewEntries = 4096
	maxViewKey     = 4*maxText*utf8.UTFMax + 3
)

// Key names a reading across reports: the part's class and name, the kind
// and the reading's name. The texts are printable (Parse), so a tab cannot
// be part of them.
func Key(s Sensor) string {
	return strings.Join([]string{s.Type, s.Hardware, s.Kind, s.Name}, "\t")
}

// ParseView reads and checks a view: from the settings file or from the
// page.
func ParseView(data []byte) (View, error) {
	var v View
	if err := json.Unmarshal(data, &v); err != nil {
		return View{}, fmt.Errorf("not a view: %w", err)
	}
	if len(v.Overrides) > MaxViewEntries {
		return View{}, fmt.Errorf("more than %d readings", MaxViewEntries)
	}
	for key := range v.Overrides {
		if len(key) > maxViewKey || !utf8.ValidString(key) || strings.Count(key, "\t") != 3 {
			return View{}, errors.New("a key that is not a reading's")
		}
	}
	if v.Overrides == nil {
		v.Overrides = map[string]bool{}
	}
	return v, nil
}
