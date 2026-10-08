// Package routines describes things a Seed does on a schedule: agent
// routines (a prompt the Seed runs) and jobs (a request to its organism).
// This package parses schedules and the organism's declared jobs; the
// runtime runs them.
package routines

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata" // timezones work in any container
)

// Schedule says when a routine runs.
type Schedule interface {
	// Next returns the first run strictly after t.
	Next(t time.Time) time.Time
	// String is how the owner reads it ("every 15 minutes").
	String() string
}

var everyRe = regexp.MustCompile(`^every\s+(\d+)\s*(m|min|mins|minutes?|h|hours?|d|days?)$`)

// Parse reads "every 15m", "every 2h", "every 1d" or a 5-field cron
// expression ("0 8 * * 1"), in the IANA timezone tz ("" = UTC).
func Parse(spec, tz string) (Schedule, error) {
	loc := time.UTC
	if tz != "" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return nil, fmt.Errorf("unknown timezone %q", tz)
		}
		loc = l
	}
	spec = strings.Join(strings.Fields(strings.ToLower(spec)), " ")
	if m := everyRe.FindStringSubmatch(spec); m != nil {
		n, _ := strconv.Atoi(m[1])
		unit := time.Minute
		switch m[2][0] {
		case 'h':
			unit = time.Hour
		case 'd':
			unit = 24 * time.Hour
		}
		if n <= 0 || n > 10000 {
			return nil, fmt.Errorf("%q: the interval must be a positive number", spec)
		}
		return every{d: time.Duration(n) * unit, loc: loc}, nil
	}
	c, err := parseCron(spec)
	if err != nil {
		return nil, fmt.Errorf("%q is not a schedule: use \"every 15m\", \"every 2h\", \"every 1d\" or cron like \"0 8 * * *\" (%v)", spec, err)
	}
	c.loc = loc
	return c, nil
}

// MinGap is the shortest time between two runs (over the next few runs).
func MinGap(s Schedule, from time.Time) time.Duration {
	t := s.Next(from)
	gap := time.Duration(1<<63 - 1)
	for i := 0; i < 50; i++ {
		n := s.Next(t)
		if n.IsZero() {
			break
		}
		if d := n.Sub(t); d < gap {
			gap = d
		}
		t = n
	}
	return gap
}

type every struct {
	d   time.Duration
	loc *time.Location
}

// Next aligns runs to the interval (from midnight in the timezone for
// daily-or-longer intervals), so "every 1h" runs on the hour.
func (e every) Next(t time.Time) time.Time {
	t = t.In(e.loc)
	day := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, e.loc)
	if e.d >= 24*time.Hour {
		return day.AddDate(0, 0, int(e.d/(24*time.Hour))) // midnight, N days on (DST-safe)
	}
	return day.Add((t.Sub(day)/e.d + 1) * e.d) // aligned within the day: "every 1h" on the hour
}

func (e every) String() string {
	switch {
	case e.d%(24*time.Hour) == 0:
		if n := e.d / (24 * time.Hour); n > 1 {
			return fmt.Sprintf("every %d days at midnight", n)
		}
		return "every day at midnight"
	case e.d%time.Hour == 0:
		if n := e.d / time.Hour; n > 1 {
			return fmt.Sprintf("every %d hours", n)
		}
		return "every hour"
	default:
		if n := e.d / time.Minute; n > 1 {
			return fmt.Sprintf("every %d minutes", n)
		}
		return "every minute"
	}
}

// ---- cron

type cron struct {
	src                        string
	min, hour, dom, month, dow uint64 // bit sets
	domStar, dowStar           bool
	loc                        *time.Location
}

func parseCron(spec string) (*cron, error) {
	f := strings.Fields(spec)
	if len(f) != 5 {
		return nil, errors.New("cron needs 5 fields: minute hour day-of-month month day-of-week")
	}
	c := &cron{src: spec}
	var err error
	if c.min, err = field(f[0], 0, 59); err != nil {
		return nil, fmt.Errorf("minute: %w", err)
	}
	if c.hour, err = field(f[1], 0, 23); err != nil {
		return nil, fmt.Errorf("hour: %w", err)
	}
	if c.dom, err = field(f[2], 1, 31); err != nil {
		return nil, fmt.Errorf("day of month: %w", err)
	}
	if c.month, err = field(f[3], 1, 12); err != nil {
		return nil, fmt.Errorf("month: %w", err)
	}
	if c.dow, err = field(strings.ReplaceAll(f[4], "7", "0"), 0, 6); err != nil {
		return nil, fmt.Errorf("day of week: %w", err)
	}
	c.domStar, c.dowStar = f[2] == "*", f[4] == "*"
	return c, nil
}

func field(s string, lo, hi int) (uint64, error) {
	var bits uint64
	for _, part := range strings.Split(s, ",") {
		step := 1
		if i := strings.Index(part, "/"); i >= 0 {
			n, err := strconv.Atoi(part[i+1:])
			if err != nil || n <= 0 {
				return 0, fmt.Errorf("bad step in %q", part)
			}
			step, part = n, part[:i]
		}
		a, b := lo, hi
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			x, y, _ := strings.Cut(part, "-")
			var err1, err2 error
			a, err1 = strconv.Atoi(x)
			b, err2 = strconv.Atoi(y)
			if err1 != nil || err2 != nil || a > b {
				return 0, fmt.Errorf("bad range %q", part)
			}
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return 0, fmt.Errorf("bad value %q", part)
			}
			a, b = n, n
			if step > 1 {
				b = hi
			}
		}
		if a < lo || b > hi {
			return 0, fmt.Errorf("%q is outside %d-%d", part, lo, hi)
		}
		for v := a; v <= b; v += step {
			bits |= 1 << uint(v)
		}
	}
	return bits, nil
}

func (c *cron) dayMatches(t time.Time) bool {
	dom := c.dom&(1<<uint(t.Day())) != 0
	dow := c.dow&(1<<uint(t.Weekday())) != 0
	switch {
	case c.domStar && c.dowStar:
		return true
	case c.domStar:
		return dow
	case c.dowStar:
		return dom
	default:
		return dom || dow // standard cron: either restricted field matches
	}
}

func (c *cron) Next(t time.Time) time.Time {
	t = t.In(c.loc).Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(5, 0, 0)
	for t.Before(limit) {
		if c.month&(1<<uint(t.Month())) == 0 {
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, c.loc)
			continue
		}
		if !c.dayMatches(t) {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, c.loc)
			continue
		}
		if c.hour&(1<<uint(t.Hour())) == 0 {
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, c.loc)
			continue
		}
		if c.min&(1<<uint(t.Minute())) == 0 {
			t = t.Add(time.Minute)
			continue
		}
		return t
	}
	return time.Time{}
}

var weekdays = []string{"Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"}

func single(bits uint64, lo, hi int) (int, bool) {
	found := -1
	for v := lo; v <= hi; v++ {
		if bits&(1<<uint(v)) != 0 {
			if found >= 0 {
				return 0, false
			}
			found = v
		}
	}
	return found, found >= 0
}

// String describes common cron shapes in words; anything else as cron.
func (c *cron) String() string {
	h, oneH := single(c.hour, 0, 23)
	m, oneM := single(c.min, 0, 59)
	allMonths := c.month == (1<<13)-2
	if oneH && oneM && allMonths {
		at := fmt.Sprintf("%02d:%02d", h, m)
		switch {
		case c.domStar && c.dowStar:
			return "every day at " + at
		case c.domStar:
			if d, ok := single(c.dow, 0, 6); ok {
				return "every " + weekdays[d] + " at " + at
			}
			if c.dow == 0b0111110 {
				return "every weekday at " + at
			}
		case c.dowStar:
			if d, ok := single(c.dom, 1, 31); ok {
				return fmt.Sprintf("on day %d of every month at %s", d, at)
			}
		}
	}
	if oneM && c.hour == (1<<24)-1 && c.domStar && c.dowStar && allMonths {
		return fmt.Sprintf("every hour at :%02d", m)
	}
	return "cron " + c.src
}
