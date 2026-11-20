package schedule

import (
	"strings"
	"testing"
	"time"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04:05", s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestNext(t *testing.T) {
	cases := []struct{ kind, spec, from, want string }{
		{"every", "15m", "2026-11-20 10:07:30", "2026-11-20 10:15:00"},
		{"every", "15m", "2026-11-20 10:15:00", "2026-11-20 10:30:00"},
		{"every", "8h", "2026-11-20 17:00:00", "2026-11-21 00:00:00"},
		{"every", "30s", "2026-11-20 23:59:45", "2026-11-21 00:00:00"},
		{"cron", "0 3 * * *", "2026-11-20 03:00:00", "2026-11-21 03:00:00"},
		{"cron", "*/20 9-17 * * 1-5", "2026-11-20 17:45:00", "2026-11-23 09:00:00"},
		{"cron", "0 0 1 * *", "2026-12-15 12:00:00", "2027-01-01 00:00:00"},
		{"cron", "0 12 * * 7", "2026-11-20 00:00:00", "2026-11-22 12:00:00"},
		{"cron", "0 0 29 2 *", "2026-03-01 00:00:00", "2028-02-29 00:00:00"},
		// Both day fields restricted: the 13th or any Friday.
		{"cron", "0 0 13 * 5", "2026-11-14 00:00:00", "2026-11-20 00:00:00"},
		// A day field starting with * counts as unrestricted, as in Vixie
		// cron, so this is every Friday, not also the 1st, 11th, ...
		{"cron", "0 0 */10 * 5", "2026-11-12 00:00:00", "2026-11-13 00:00:00"},
		{"cron", "5,35 1 * * *", "2026-11-20 01:06:00", "2026-11-20 01:35:00"},
	}
	for _, c := range cases {
		s, err := Parse(c.kind, c.spec)
		if err != nil {
			t.Fatalf("%s %q: %v", c.kind, c.spec, err)
		}
		if got := s.Next(at(c.from)); !got.Equal(at(c.want)) {
			t.Errorf("%s %q after %s = %s, want %s", c.kind, c.spec, c.from, got.Format("2006-01-02 15:04:05"), c.want)
		}
	}
}

func TestNeverFiring(t *testing.T) {
	s, err := Parse("cron", "0 0 31 2 *")
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Next(at("2026-01-01 00:00:00")); !got.IsZero() {
		t.Errorf("31 February fired at %s", got)
	}
}

func TestRefusals(t *testing.T) {
	cases := []struct{ kind, spec, fragment string }{
		{"every", "15", "isn't a duration"},
		{"every", "500ms", "under 1s"},
		{"every", "7m", "doesn't divide a day"},
		{"cron", "0 3 * *", "has 4 fields"},
		{"cron", "60 * * * *", "minute 60 is outside 0 to 59"},
		{"cron", "0 0 * JAN *", "names such as MON and JAN"},
		{"cron", "0 0 L * *", "isn't a number"},
		{"cron", "*/0 * * * *", `step "0"`},
		{"cron", "0 9-5 * * *", "runs backwards"},
	}
	for _, c := range cases {
		_, err := Parse(c.kind, c.spec)
		if err == nil || !strings.Contains(err.Error(), c.fragment) {
			t.Errorf("%s %q: %v, want %q", c.kind, c.spec, err, c.fragment)
		}
	}
}
