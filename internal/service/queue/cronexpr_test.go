package queue

import (
	"errors"
	"testing"
	"time"
)

// utc keeps the table tests free of a location argument. Next works in the
// location of the time it is given, so a test that cared about DST would pass a
// zoned time instead — see TestSchedule_Next_DST.
func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}
	return t
}

func mustParse(t *testing.T, expr string) Schedule {
	t.Helper()
	s, err := ParseCron(expr)
	if err != nil {
		t.Fatalf("ParseCron(%q): %v", expr, err)
	}
	return s
}

func mustNext(t *testing.T, s Schedule, from time.Time) time.Time {
	t.Helper()
	got, err := s.Next(from)
	if err != nil {
		t.Fatalf("Next(%s): %v", from.Format(time.RFC3339), err)
	}
	return got
}

func TestParseCron_Vocabulary(t *testing.T) {
	// from is a Wednesday, so weekday expressions have somewhere to move to.
	from := at("2026-08-12 10:07")

	cases := []struct {
		expr string
		want string
	}{
		{"* * * * *", "2026-08-12 10:08"},
		{"0 * * * *", "2026-08-12 11:00"},
		{"*/15 * * * *", "2026-08-12 10:15"},
		{"7 * * * *", "2026-08-12 11:07"}, // strictly after, so not 10:07 again
		{"0 3 * * *", "2026-08-13 03:00"},
		{"30 9,21 * * *", "2026-08-12 21:30"},
		{"0 0 1 * *", "2026-09-01 00:00"},
		{"0 0 * * 0", "2026-08-16 00:00"},   // next Sunday
		{"0 0 * * SUN", "2026-08-16 00:00"}, // by name
		{"0 0 * * 7", "2026-08-16 00:00"},   // Sunday as 7
		{"0 0 * * 5-7", "2026-08-14 00:00"}, // Fri..Sun: 7 must not invert the range
		{"0 0 * SEP *", "2026-09-01 00:00"},
		{"0 0 * sep *", "2026-09-01 00:00"}, // names are case-insensitive
		{"0 0 1 JAN *", "2027-01-01 00:00"},
		{"0 0-23/6 * * *", "2026-08-12 12:00"},
		{"0 0 * * MON-FRI", "2026-08-13 00:00"},
		{"5 10/6 * * *", "2026-08-12 16:05"}, // open-ended step: 10,16,22
		{"@hourly", "2026-08-12 11:00"},
		{"@daily", "2026-08-13 00:00"},
		{"@midnight", "2026-08-13 00:00"},
		{"@weekly", "2026-08-16 00:00"},
		{"@monthly", "2026-09-01 00:00"},
		{"@yearly", "2027-01-01 00:00"},
		{"@annually", "2027-01-01 00:00"},
		{"@DAILY", "2026-08-13 00:00"}, // descriptors are case-insensitive
		{"  0   3  *  *  *  ", "2026-08-13 03:00"},
	}

	for _, c := range cases {
		t.Run(c.expr, func(t *testing.T) {
			got := mustNext(t, mustParse(t, c.expr), from)
			if want := at(c.want); !got.Equal(want) {
				t.Errorf("Next = %s, want %s", got.Format("2006-01-02 15:04"), c.want)
			}
		})
	}
}

// The single most common bug in a hand-written cron parser: when day-of-month and
// day-of-week are BOTH restricted they are OR-ed, not AND-ed. Vixie cron does
// this, and getting it wrong is silent — the schedule simply fires less often than
// the operator asked for, which nobody notices until the report is missing.
func TestParseCron_DayOfMonthAndDayOfWeekAreOred(t *testing.T) {
	s := mustParse(t, "0 0 13 * FRI")

	// August 2026: Fridays are the 7th, 14th, 21st, 28th; the 13th is a Thursday.
	// An AND reading would skip the 13th entirely and only match a Friday the
	// 13th (November 2026).
	want := []string{
		"2026-08-07 00:00", // Friday
		"2026-08-13 00:00", // the 13th, a Thursday — only an OR reading matches it
		"2026-08-14 00:00", // Friday
		"2026-08-21 00:00",
		"2026-08-28 00:00",
	}

	cur := at("2026-08-01 00:00")
	for _, w := range want {
		cur = mustNext(t, s, cur)
		if got := cur.Format("2006-01-02 15:04"); got != w {
			t.Fatalf("next fire = %s, want %s", got, w)
		}
	}
}

// When only one of the two day fields is restricted, only that one constrains the
// day. This is the other half of the OR rule, and the half that a naive
// "always OR" implementation gets wrong: OR-ing a restricted dom with an
// unrestricted (all-bits) dow would match every day.
func TestParseCron_OneRestrictedDayFieldConstrainsAlone(t *testing.T) {
	t.Run("day of month only", func(t *testing.T) {
		s := mustParse(t, "0 0 13 * *")
		got := mustNext(t, s, at("2026-08-01 00:00"))
		if want := at("2026-08-13 00:00"); !got.Equal(want) {
			t.Errorf("Next = %s, want 2026-08-13 00:00", got.Format("2006-01-02 15:04"))
		}
	})
	t.Run("day of week only", func(t *testing.T) {
		s := mustParse(t, "0 0 * * FRI")
		got := mustNext(t, s, at("2026-08-01 00:00"))
		if want := at("2026-08-07 00:00"); !got.Equal(want) {
			t.Errorf("Next = %s, want 2026-08-07 00:00", got.Format("2006-01-02 15:04"))
		}
	})
	t.Run("a full range still counts as restricted", func(t *testing.T) {
		// "0-6" covers every weekday, so it matches daily — but it must take the
		// restricted path, which is why restrictedness is tracked separately from
		// the mask (a full mask is what both "*" and "0-6" produce).
		s := mustParse(t, "0 0 13 * 0-6")
		got := mustNext(t, s, at("2026-08-01 00:00"))
		if want := at("2026-08-02 00:00"); !got.Equal(want) {
			t.Errorf("Next = %s, want 2026-08-02 00:00 (OR with every weekday)",
				got.Format("2006-01-02 15:04"))
		}
	})
}

func TestParseCron_Every(t *testing.T) {
	s := mustParse(t, "@every 30s")
	if s.Every() != 30*time.Second {
		t.Fatalf("Every = %s, want 30s", s.Every())
	}
	from := at("2026-08-12 10:07")
	if got := mustNext(t, s, from); !got.Equal(from.Add(30 * time.Second)) {
		t.Errorf("Next = %s, want +30s", got.Format(time.RFC3339))
	}

	// @every is not truncated to the minute the way a cron expression is: its
	// whole point is sub-minute intervals.
	odd := at("2026-08-12 10:07").Add(23 * time.Second)
	if got := mustNext(t, s, odd); !got.Equal(odd.Add(30 * time.Second)) {
		t.Errorf("Next from a mid-minute instant = %s, want +30s", got.Format(time.RFC3339))
	}

	if _, err := ParseCron("@EVERY 5m"); err != nil {
		t.Errorf("@EVERY should be accepted case-insensitively: %v", err)
	}
	if s := mustParse(t, "0 3 * * *"); s.Every() != 0 {
		t.Errorf("a cron expression reported Every = %s, want 0", s.Every())
	}
}

// An expression that parses but can never match must report, not spin and not
// silently never fire. February 30th is the canonical example.
func TestSchedule_Next_Unsatisfiable(t *testing.T) {
	s := mustParse(t, "30 2 30 2 *")
	if _, err := s.Next(at("2026-08-12 10:07")); !errors.Is(err, ErrCronUnsatisfiable) {
		t.Fatalf("Next error = %v, want ErrCronUnsatisfiable", err)
	}
}

// The far-apart matches: a leap day is four years out, which is inside the
// five-year bound on purpose.
func TestSchedule_Next_LeapDay(t *testing.T) {
	s := mustParse(t, "0 0 29 2 *")
	got := mustNext(t, s, at("2026-08-12 10:07"))
	if want := at("2028-02-29 00:00"); !got.Equal(want) {
		t.Errorf("Next = %s, want 2028-02-29 00:00", got.Format("2006-01-02 15:04"))
	}
}

func TestSchedule_Next_CrossesMonthAndYear(t *testing.T) {
	cases := []struct{ expr, from, want string }{
		{"0 0 1 * *", "2026-08-31 23:59", "2026-09-01 00:00"},
		{"59 23 31 12 *", "2026-12-31 23:58", "2026-12-31 23:59"},
		{"0 0 1 1 *", "2026-12-31 23:59", "2027-01-01 00:00"},
		{"0 0 31 * *", "2026-04-15 00:00", "2026-05-31 00:00"}, // April has no 31st
	}
	for _, c := range cases {
		t.Run(c.expr+" from "+c.from, func(t *testing.T) {
			got := mustNext(t, mustParse(t, c.expr), at(c.from))
			if want := at(c.want); !got.Equal(want) {
				t.Errorf("Next = %s, want %s", got.Format("2006-01-02 15:04"), c.want)
			}
		})
	}
}

// Next is strictly after its argument. Returning t itself would make the
// scheduler fire the same minute forever: it advances next_run_at to whatever
// this returns, then compares it against now again.
func TestSchedule_Next_IsStrictlyAfter(t *testing.T) {
	s := mustParse(t, "* * * * *")
	exact := at("2026-08-12 10:00")
	got := mustNext(t, s, exact)
	if !got.After(exact) {
		t.Fatalf("Next(%s) = %s, want strictly later", exact, got)
	}
	if want := exact.Add(time.Minute); !got.Equal(want) {
		t.Errorf("Next = %s, want %s", got, want)
	}
}

// Next works in the location of the time it is handed, which is how
// queue.timezone reaches the scan. Asia/Shanghai has no DST, so this also pins
// that a zoned schedule fires at the local wall-clock hour rather than UTC's.
func TestSchedule_Next_UsesTheArgumentsLocation(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skipf("no tzdata for Asia/Shanghai: %v", err)
	}
	s := mustParse(t, "0 3 * * *")
	from := time.Date(2026, 8, 12, 10, 7, 0, 0, loc)
	got := mustNext(t, s, from)

	if got.Location() != loc {
		t.Errorf("Next lost the location: %s", got.Location())
	}
	if h, m := got.Hour(), got.Minute(); h != 3 || m != 0 {
		t.Errorf("Next = %02d:%02d local, want 03:00", h, m)
	}
	if want := time.Date(2026, 8, 13, 3, 0, 0, 0, loc); !got.Equal(want) {
		t.Errorf("Next = %s, want %s", got, want)
	}
}

// The two DST outcomes, asserted rather than left to be discovered. A plan inside
// the skipped hour does not run that day; a plan inside the repeated hour runs
// twice, an hour of real time apart. Both follow from scanning wall-clock minutes,
// and both are documented on Next.
func TestSchedule_Next_DST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("no tzdata for America/New_York: %v", err)
	}

	t.Run("spring forward skips the day", func(t *testing.T) {
		// 2026-03-08: 02:00 EST jumps to 03:00 EDT, so 02:30 does not exist.
		s := mustParse(t, "30 2 * * *")
		got := mustNext(t, s, time.Date(2026, 3, 8, 0, 0, 0, 0, loc))
		want := time.Date(2026, 3, 9, 2, 30, 0, 0, loc)
		if !got.Equal(want) {
			t.Errorf("Next = %s, want %s (the 8th has no 02:30)", got, want)
		}
	})

	t.Run("fall back fires twice", func(t *testing.T) {
		// 2026-11-01: 02:00 EDT falls back to 01:00 EST, so 01:30 happens twice.
		s := mustParse(t, "30 1 * * *")
		first := mustNext(t, s, time.Date(2026, 11, 1, 0, 0, 0, 0, loc))
		second := mustNext(t, s, first)
		if second.Sub(first) != time.Hour {
			t.Errorf("second fire is %s after the first, want 1h apart on the same wall clock",
				second.Sub(first))
		}
		if h, m := second.Hour(), second.Minute(); h != 1 || m != 30 {
			t.Errorf("second fire = %02d:%02d, want 01:30", h, m)
		}
	})
}

func TestParseCron_Errors(t *testing.T) {
	cases := []struct{ name, expr string }{
		{"empty", ""},
		{"blank", "   "},
		{"too few fields", "0 3 * *"},
		{"too many fields", "0 0 3 * * *"},
		{"unknown descriptor", "@fortnightly"},
		{"every without a duration", "@every"},
		{"every with a bad duration", "@every soon"},
		{"every with a zero duration", "@every 0s"},
		{"every with a negative duration", "@every -5m"},
		{"minute out of range", "60 * * * *"},
		{"hour out of range", "0 24 * * *"},
		{"day of month zero", "0 0 0 * *"},
		{"day of month out of range", "0 0 32 * *"},
		{"month out of range", "0 0 * 13 *"},
		{"day of week out of range", "0 0 * * 8"},
		{"unknown name", "0 0 * * FUNDAY"},
		{"month name in the hour field", "0 JAN * * *"},
		{"inverted range", "0 0 * * 5-1"},
		{"zero step", "*/0 * * * *"},
		{"non-numeric step", "*/x * * * *"},
		{"empty list item", "1,,2 * * * *"},
		{"trailing comma", "1,2, * * * *"},
		{"not a number", "abc * * * *"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if s, err := ParseCron(c.expr); err == nil {
				t.Errorf("ParseCron(%q) accepted it as %+v, want an error", c.expr, s)
			}
		})
	}
}

// Parsing is pure and repeated on every admin form submission and every startup
// sync, so it must not be quadratic in anything. A guard against a rewrite that
// reaches for a per-minute-of-year loop.
func BenchmarkParseCronAndNext(b *testing.B) {
	from := at("2026-08-12 10:07")
	for range b.N {
		s, err := ParseCron("0 0 29 2 *")
		if err != nil {
			b.Fatal(err)
		}
		if _, err := s.Next(from); err != nil {
			b.Fatal(err)
		}
	}
}
