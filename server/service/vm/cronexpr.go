package vm

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// cronSchedule is a five-field cron line as BusyBox crond (1.37/1.38,
// miscutils/crond.c) reads it: the masks ParseField fills and FixDayDow
// adjusts, matched as flag_starting_jobs does. Where crond itself misbehaves
// (a step of 0, a month 0 written outside its array) the expression is
// refused instead.
type cronSchedule struct {
	minute [60]bool
	hour   [24]bool
	// Indexed by day of month; index 0 exists as in crond, never matches,
	// and makes "*" differ from "1-31" in FixDayDow.
	dom   [32]bool
	month [12]bool // 0 = January
	dow   [7]bool  // 0 = Sunday
}

var (
	cronMonths   = []string{"jan", "feb", "mar", "apr", "may", "jun", "jul", "aug", "sep", "oct", "nov", "dec"}
	cronWeekdays = []string{"sun", "mon", "tue", "wed", "thu", "fri", "sat"}
)

func parseCron(expr string) (cronSchedule, error) {
	var s cronSchedule
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return s, errors.New("a schedule has five fields: minute, hour, day of month, month, weekday")
	}
	for i, f := range []struct {
		name  string
		mask  []bool
		off   int
		names []string
	}{
		{"minute", s.minute[:], 0, nil},
		{"hour", s.hour[:], 0, nil},
		{"day of month", s.dom[:], 0, nil},
		{"month", s.month[:], -1, cronMonths},
		{"weekday", s.dow[:], 0, cronWeekdays},
	} {
		if err := cronField(fields[i], f.mask, f.off, f.names); err != nil {
			return s, errors.New(f.name + ": crond cannot read " + strconv.Quote(fields[i]))
		}
	}
	// FixDayDow: a day field that selects everything (the whole mask, index
	// 0 of the day of month included) gives way to a restricted other one.
	weekUsed, daysUsed := !all(s.dow[:]), !all(s.dom[:])
	if weekUsed != daysUsed {
		if weekUsed {
			s.dom = [32]bool{}
		} else {
			s.dow = [7]bool{}
		}
	}
	return s, nil
}

func all(mask []bool) bool {
	for _, v := range mask {
		if !v {
			return false
		}
	}
	return true
}

// cronField fills mask as crond's ParseField does: comma lists of *, a
// number, a three-letter name, a range a-b (wrapping past the end, as
// 22-2 for hours), each with an optional step /n counted from its start.
// Numbers are digits only: crond reads no sign.
func cronField(text string, mask []bool, off int, names []string) error {
	mod, p := len(mask), 0
	number := func() (int, bool) {
		start := p
		for p < len(text) && text[p] >= '0' && text[p] <= '9' {
			p++
		}
		v, err := strconv.Atoi(text[start:p])
		return v, p > start && err == nil
	}
	for {
		n1, n2 := -1, -1
		for {
			switch {
			case p < len(text) && text[p] == '*':
				n1, n2 = 0, mod-1
				p++
			case p < len(text) && text[p] >= '0' && text[p] <= '9':
				v, _ := number()
				if n1 < 0 {
					n1 = v + off
				} else {
					n2 = v + off
				}
			default:
				found := false
				for i, name := range names {
					if len(text)-p >= 3 && strings.EqualFold(text[p:p+3], name) {
						p += 3
						if n1 < 0 {
							n1 = i
						} else {
							n2 = i
						}
						found = true
						break
					}
				}
				if !found {
					return errors.New("parse error")
				}
			}
			if p < len(text) && text[p] == '-' && n2 < 0 {
				p++
				continue
			}
			break
		}
		if n2 < 0 {
			n2 = n1
		}
		step := 1
		if p < len(text) && text[p] == '/' {
			p++
			v, ok := number()
			if !ok || v < 1 {
				return errors.New("parse error")
			}
			step = v
		}
		if n1 < 0 || n1 >= mod || n2 < 0 || n2 >= mod {
			return errors.New("out of range")
		}
		// From n1 to n2, wrapping, every step-th value from n1 on.
		for n, s0 := n1-1, 1; ; {
			n = (n + 1) % mod
			if s0--; s0 == 0 {
				mask[n] = true
				s0 = step
			}
			if n == n2 {
				break
			}
		}
		if p < len(text) && text[p] == ',' {
			p++
			continue
		}
		break
	}
	if p != len(text) {
		return errors.New("parse error")
	}
	return nil
}

func (s cronSchedule) matches(t time.Time) bool {
	return s.minute[t.Minute()] && s.hour[t.Hour()] && s.month[t.Month()-1] &&
		(s.dom[t.Day()] || s.dow[t.Weekday()])
}

// next returns up to count run times after from, in from's location, looking
// at most five years ahead (a schedule such as "Feb 31" never runs). It walks
// elapsed time, as crond checks each minute's local time: an hour that cannot
// match (by its month, day or hour) is passed to its local end in one step,
// a matching one minute by minute. A step stops at the next change of the
// zone's offset, which need not fall on the hour (Pacific/Chatham changes
// at :45). No instant is jumped over: a DST gap is crossed and an hour
// repeated in autumn is seen twice, as crond sees them.
func (s cronSchedule) next(from time.Time, count int) []time.Time {
	var runs []time.Time
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.AddDate(5, 0, 0)
	for len(runs) < count && t.Before(limit) {
		if !s.month[t.Month()-1] || (!s.dom[t.Day()] && !s.dow[t.Weekday()]) || !s.hour[t.Hour()] {
			step := t.Add(time.Duration(60-t.Minute()) * time.Minute)
			if _, change := t.ZoneBounds(); !change.IsZero() && change.After(t) && change.Before(step) {
				step = change
			}
			t = step
			continue
		}
		if s.minute[t.Minute()] {
			runs = append(runs, t)
		}
		t = t.Add(time.Minute)
	}
	return runs
}

// localtimePath is where crond's time zone comes from (test seam).
var localtimePath = "/etc/localtime"

// deviceLocation is the time zone crond uses now: /etc/localtime as it is,
// not Go's time.Local, which is loaded once and misses a change made in
// System > Date and time.
func deviceLocation() *time.Location {
	data, err := os.ReadFile(localtimePath)
	if err != nil {
		return time.UTC
	}
	loc, err := time.LoadLocationFromTZData("Local", data)
	if err != nil {
		return time.UTC
	}
	return loc
}
