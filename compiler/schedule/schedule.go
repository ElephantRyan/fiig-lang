// Package schedule reads a schedule route's `every` duration and `cron`
// expression (ADR 0112 §1) and finds their firing times, in UTC. The
// checker uses Parse to report I5; the runtime uses Next to fire.
package schedule

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule is when a job fires.
type Schedule interface {
	// Next is the first firing strictly after t.
	Next(t time.Time) time.Time
}

const day = 24 * time.Hour

// Parse reads a route's schedule: kind is "every" or "cron".
func Parse(kind, spec string) (Schedule, error) {
	switch kind {
	case "every":
		return parseEvery(spec)
	case "cron":
		return parseCron(spec)
	}
	return nil, fmt.Errorf("a schedule is every or cron, not %s", kind)
}

// every fires at each multiple of period since midnight UTC.
type every struct{ period time.Duration }

func parseEvery(spec string) (Schedule, error) {
	d, err := time.ParseDuration(spec)
	if err != nil {
		return nil, fmt.Errorf("%q isn't a duration; write one such as \"30s\", \"15m\", or \"1h30m\"", spec)
	}
	if d < time.Second {
		return nil, fmt.Errorf("every %q is under 1s", spec)
	}
	if day%d != 0 {
		return nil, fmt.Errorf("every %q doesn't divide a day, so its firings would move from day to day; choose one that does, such as \"10m\" or \"8h\", or write a cron expression", spec)
	}
	return every{d}, nil
}

func (e every) Next(t time.Time) time.Time {
	t = t.UTC()
	midnight := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
	since := t.Sub(midnight)
	return midnight.Add((since/e.period + 1) * e.period)
}

// cron is the five fields, each the set of values it allows.
type cron struct {
	minute, hour, dom, month, dow uint64
	// domAny and dowAny are a day field starting with *: when both day
	// fields are restricted, either matching is enough (Vixie cron).
	domAny, dowAny bool
}

type field struct {
	name     string
	min, max int
}

var fields = []field{{"minute", 0, 59}, {"hour", 0, 23}, {"day of month", 1, 31}, {"month", 1, 12}, {"day of week", 0, 7}}

func parseCron(spec string) (Schedule, error) {
	parts := strings.Fields(spec)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron %q has %d fields; it has five: minute, hour, day of month, month, day of week", spec, len(parts))
	}
	var sets [5]uint64
	for i, p := range parts {
		set, err := parseField(p, fields[i])
		if err != nil {
			return nil, fmt.Errorf("cron %q: %v", spec, err)
		}
		sets[i] = set
	}
	// 7 is Sunday too.
	if sets[4]&(1<<7) != 0 {
		sets[4] = sets[4]&^(1<<7) | 1
	}
	return cron{minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4], domAny: strings.HasPrefix(parts[2], "*"), dowAny: strings.HasPrefix(parts[4], "*")}, nil
}

func parseField(text string, f field) (uint64, error) {
	var set uint64
	for _, item := range strings.Split(text, ",") {
		rangePart, stepPart, stepped := strings.Cut(item, "/")
		step := 1
		if stepped {
			n, err := strconv.Atoi(stepPart)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("the %s step %q isn't a whole number of at least 1", f.name, stepPart)
			}
			step = n
		}
		lo, hi := f.min, f.max
		if rangePart != "*" {
			a, b, isRange := strings.Cut(rangePart, "-")
			var err error
			if lo, err = value(a, f); err != nil {
				return 0, err
			}
			hi = lo
			if isRange {
				if hi, err = value(b, f); err != nil {
					return 0, err
				}
				if hi < lo {
					return 0, fmt.Errorf("the %s range %q runs backwards", f.name, rangePart)
				}
			} else if stepped {
				hi = f.max
			}
		}
		for v := lo; v <= hi; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

func value(text string, f field) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil {
		return 0, fmt.Errorf("the %s %q isn't a number (names such as MON and JAN, and L, W, #, and ?, aren't supported)", f.name, text)
	}
	if n < f.min || n > f.max {
		return 0, fmt.Errorf("the %s %d is outside %d to %d", f.name, n, f.min, f.max)
	}
	return n, nil
}

func (c cron) Next(t time.Time) time.Time {
	t = t.UTC().Truncate(time.Minute).Add(time.Minute)
	// Every schedule matches within five years (29 February included),
	// so this ends; a day that can never match, such as 31 February, is
	// cut off there.
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if c.month&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, time.UTC)
			continue
		}
		if c.hour&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, time.UTC)
			continue
		}
		if c.minute&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

func (c cron) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domAny && c.dowAny:
		return true
	case c.domAny:
		return dow
	case c.dowAny:
		return dom
	}
	return dom || dow
}
