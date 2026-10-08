package routines

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(s string, loc *time.Location) time.Time {
	t, err := time.ParseInLocation("2006-01-02 15:04", s, loc)
	if err != nil {
		panic(err)
	}
	return t
}

func TestSchedules(t *testing.T) {
	lisbon, _ := time.LoadLocation("Europe/Lisbon")
	cases := []struct {
		spec, tz, from, want, words string
	}{
		{"every 15m", "", "2026-10-08 10:07", "2026-10-08 10:15", "every 15 minutes"},
		{"every 1h", "", "2026-10-08 10:00", "2026-10-08 11:00", "every hour"},
		{"every 2 hours", "", "2026-10-08 23:30", "2026-10-09 00:00", "every 2 hours"},
		{"every 1d", "Europe/Lisbon", "2026-10-08 10:07", "2026-10-09 00:00", "every day at midnight"},
		{"0 8 * * *", "Europe/Lisbon", "2026-10-08 08:00", "2026-10-09 08:00", "every day at 08:00"},
		{"30 9 * * 1", "Europe/Lisbon", "2026-10-08 10:00", "2026-10-12 09:30", "every Monday at 09:30"},
		{"0 18 * * 1-5", "Europe/Lisbon", "2026-10-09 19:00", "2026-10-12 18:00", "every weekday at 18:00"},
		{"*/10 * * * *", "", "2026-10-08 10:01", "2026-10-08 10:10", "cron */10 * * * *"},
		{"0 0 1 * *", "", "2026-10-08 10:00", "2026-11-01 00:00", "on day 1 of every month at 00:00"},
		{"5 * * * *", "", "2026-10-08 10:06", "2026-10-08 11:05", "every hour at :05"},
	}
	for _, c := range cases {
		s, err := Parse(c.spec, c.tz)
		if err != nil {
			t.Fatalf("%s: %v", c.spec, err)
		}
		loc := time.UTC
		if c.tz != "" {
			loc = lisbon
		}
		if got := s.Next(at(c.from, loc)); !got.Equal(at(c.want, loc)) {
			t.Errorf("%s from %s: got %s, want %s", c.spec, c.from, got.In(loc).Format("2006-01-02 15:04"), c.want)
		}
		if s.String() != c.words {
			t.Errorf("%s reads %q, want %q", c.spec, s.String(), c.words)
		}
	}
	for _, bad := range []string{"", "sometimes", "every 0m", "60 * * * *", "* * * *", "0 8 * * 8", "every -1h"} {
		if _, err := Parse(bad, ""); err == nil {
			t.Errorf("%q should not parse", bad)
		}
	}
	if _, err := Parse("0 8 * * *", "Mars/Olympus"); err == nil {
		t.Error("unknown timezone")
	}
}

func TestMinimumGaps(t *testing.T) {
	if _, err := ValidateSchedule(KindAgent, "every 5m", ""); err == nil {
		t.Error("agent routines run at most every 15 minutes")
	}
	if _, err := ValidateSchedule(KindAgent, "*/5 * * * *", ""); err == nil {
		t.Error("…also as cron")
	}
	if _, err := ValidateSchedule(KindAgent, "every 15m", ""); err != nil {
		t.Error(err)
	}
	if _, err := ValidateSchedule(KindJob, "every 1m", ""); err != nil {
		t.Error(err)
	}
}

func TestJobFile(t *testing.T) {
	root := t.TempDir()
	write := func(s string) {
		os.MkdirAll(filepath.Join(root, "organism"), 0o755)
		os.WriteFile(filepath.Join(root, File), []byte(s), 0o644)
	}
	if jobs, err := LoadFile(root); err != nil || jobs != nil {
		t.Fatal("no file, no jobs")
	}
	write("jobs:\n  - name: send-reminders\n    schedule: every 1h\n    path: /internal/jobs/send-reminders\n")
	jobs, err := LoadFile(root)
	if err != nil || len(jobs) != 1 || jobs[0].Method != "POST" {
		t.Fatalf("%v %v", jobs, err)
	}
	for _, bad := range []string{
		"jobs:\n  - name: x\n    schedule: every 1h\n    path: /_seed/api/approvals\n",
		"jobs:\n  - name: x\n    schedule: every 1h\n    path: http://evil.example/\n",
		"jobs:\n  - name: Send Reminders\n    schedule: every 1h\n    path: /a\n",
		"jobs:\n  - name: x\n    schedule: often\n    path: /a\n",
		"jobs:\n  - name: x\n    schedule: every 1h\n    path: /a\n    command: rm -rf /\n",
		"jobs:\n  - name: x\n    schedule: every 1h\n    path: /a\n  - name: x\n    schedule: every 1h\n    path: /b\n",
	} {
		write(bad)
		if _, err := LoadFile(root); err == nil || !strings.Contains(err.Error(), File) {
			t.Errorf("should be refused with the file named: %q (%v)", bad, err)
		}
	}
}
