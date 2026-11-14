package vm

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

// cronSchedule is a five-field cron expression as BusyBox crond reads it:
// numbers, *, ranges a-b, steps */n and a-b/n, comma lists, and three-letter
// month and weekday names. Weekdays are 0-6 from Sunday.
type cronSchedule struct {
	minute, hour, dom, month, dow uint64
	// As in crond: with both day fields restricted a day matches either;
	// with one, only that one counts.
	domStar, dowStar bool
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
	var err error
	if s.minute, err = cronField(fields[0], 0, 59, nil); err != nil {
		return s, errors.New("minute: " + err.Error())
	}
	if s.hour, err = cronField(fields[1], 0, 23, nil); err != nil {
		return s, errors.New("hour: " + err.Error())
	}
	if s.dom, err = cronField(fields[2], 1, 31, nil); err != nil {
		return s, errors.New("day of month: " + err.Error())
	}
	if s.month, err = cronField(fields[3], 1, 12, cronMonths); err != nil {
		return s, errors.New("month: " + err.Error())
	}
	if s.dow, err = cronField(fields[4], 0, 6, cronWeekdays); err != nil {
		return s, errors.New("weekday: " + err.Error())
	}
	s.domStar = strings.HasPrefix(fields[2], "*")
	s.dowStar = strings.HasPrefix(fields[4], "*")
	return s, nil
}

// cronField returns the set of values as a bit mask.
func cronField(field string, low, high int, names []string) (uint64, error) {
	var set uint64
	for _, part := range strings.Split(field, ",") {
		span, step := part, 1
		if i := strings.IndexByte(part, '/'); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n < 1 || n > high-low+1 {
				return 0, errors.New("invalid step in " + part)
			}
			span, step = part[:i], n
		}
		from, to := low, high
		if span != "*" {
			a, b, isRange := strings.Cut(span, "-")
			var err error
			if from, err = cronValue(a, low, high, names); err != nil {
				return 0, err
			}
			to = from
			if isRange {
				if to, err = cronValue(b, low, high, names); err != nil {
					return 0, err
				}
				if to < from {
					return 0, errors.New("range ends before it starts: " + span)
				}
			}
			// "5/10" is just 5 for BusyBox crond: a step needs * or a range.
		}
		for v := from; v <= to; v += step {
			set |= 1 << uint(v)
		}
	}
	return set, nil
}

func cronValue(text string, low, high int, names []string) (int, error) {
	for i, name := range names {
		if strings.EqualFold(text, name) {
			return i + low, nil
		}
	}
	v, err := strconv.Atoi(text)
	if err != nil || v < low || v > high {
		return 0, errors.New("not a value from " + strconv.Itoa(low) + " to " + strconv.Itoa(high) + ": " + text)
	}
	return v, nil
}

func (s cronSchedule) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<uint(t.Day())) != 0
	dow := s.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case s.domStar && s.dowStar:
		return true
	case s.domStar:
		return dow
	case s.dowStar:
		return dom
	}
	return dom || dow
}

// next returns up to count run times after from, in from's location, looking
// at most five years ahead (a schedule such as "Feb 31" never runs).
func (s cronSchedule) next(from time.Time, count int) []time.Time {
	var runs []time.Time
	t := from.Truncate(time.Minute).Add(time.Minute)
	t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, from.Location())
	limit := from.AddDate(5, 0, 0)
	for len(runs) < count && t.Before(limit) {
		switch {
		case s.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
		case !s.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
		case s.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
		case s.minute&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			runs = append(runs, t)
			t = t.Add(time.Minute)
		}
	}
	return runs
}
