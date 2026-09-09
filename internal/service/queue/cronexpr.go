package queue

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A five-field cron parser, written here rather than taken from a library.
//
// The obvious candidate is github.com/robfig/cron/v3, and the reason not to use
// it is that its substance is its scheduler — the entry heap, its own goroutine
// and timers. This queue must not use that: which process fires a plan is
// arbitrated in the database (see fireDueSchedules), because two instances each
// running their own in-memory scheduler would each fire every plan. So a
// dependency would be carried for ParseStandard and Schedule.Next alone, and
// every non-queue build would need a line in initcmd_test.go's wantAbsent matrix
// to prove it disappeared. A parser inside the directory the queue owns goes away
// with the component and needs no such proof.
//
// What that buys has to be paid for in tests instead, and the two things worth
// paying for are named at their implementations: the OR between day-of-month and
// day-of-week, and the bound on next().
//
// Supported, matching the Vixie/standard vocabulary people actually paste:
//
//	minute hour day-of-month month day-of-week
//	*  n  a-b  a-b/s  */s  and comma lists of any of those
//	names: JAN..DEC and SUN..SAT (case-insensitive), also in ranges
//	@yearly @annually @monthly @weekly @daily @midnight @hourly
//	@every <duration>   — parsed by time.ParseDuration
//
// Not supported, deliberately: seconds (a sixth field), `?`, `L`, `W`, `#`, and
// step values on a bare name list. Those come from Quartz, not cron; a plan
// needing them is better expressed as @every.

// ErrCronUnsatisfiable is returned by Schedule.Next for an expression that
// parses but can never come true — 30 2 30 2 * asks for February 30th. It is a
// distinct error because the alternative behaviours are both silent: looping
// forever, or reporting "no next run" for something the operator believes is
// scheduled.
var ErrCronUnsatisfiable = errors.New("queue: cron expression has no future match")

// maxCronSearchYears bounds the forward scan in Next. Five years is far past any
// legitimate schedule (February 29th on a non-leap-year cycle is the widest real
// gap, at four) and short enough that an unsatisfiable expression reports rather
// than hangs.
const maxCronSearchYears = 5

// Schedule is a parsed cron expression. Fields are bitmasks so a match is one
// shift and a mask test; every is set instead for @every, which is not a cron
// expression at all.
type Schedule struct {
	// every is non-zero for "@every <duration>", in which case the masks are
	// unused. Kept as a separate mode rather than approximated by a mask because
	// "@every 30s" cannot be expressed in minute-granularity fields at all.
	every time.Duration

	minute uint64 // bits 0..59
	hour   uint64 // bits 0..23
	dom    uint64 // bits 1..31
	month  uint64 // bits 1..12
	dow    uint64 // bits 0..6, Sunday = 0

	// domRestricted and dowRestricted record whether the field was written as
	// something other than "*". They cannot be derived from the masks — a full
	// mask is also what "0-6" produces — and the OR rule in Next needs exactly
	// this distinction.
	domRestricted bool
	dowRestricted bool
}

// cronField describes one field's legal range and names, so parsing is one
// routine rather than five.
type cronField struct {
	name  string
	min   uint
	max   uint
	names map[string]uint
}

var (
	monthNames = map[string]uint{
		"jan": 1, "feb": 2, "mar": 3, "apr": 4, "may": 5, "jun": 6,
		"jul": 7, "aug": 8, "sep": 9, "oct": 10, "nov": 11, "dec": 12,
	}
	dowNames = map[string]uint{
		"sun": 0, "mon": 1, "tue": 2, "wed": 3, "thu": 4, "fri": 5, "sat": 6,
	}

	fieldMinute = cronField{name: "minute", min: 0, max: 59}
	fieldHour   = cronField{name: "hour", min: 0, max: 23}
	fieldDOM    = cronField{name: "day of month", min: 1, max: 31}
	fieldMonth  = cronField{name: "month", min: 1, max: 12, names: monthNames}
	// max is 7, not 6: cron accepts Sunday as either 0 or 7, and "5-7" has to mean
	// Fri-Sun. Normalising 7 to 0 at the value level would make that range
	// inverted (5 > 0) and reject a perfectly ordinary expression, so bit 7 is
	// folded into bit 0 after the whole mask is built. See ParseCron.
	fieldDOW = cronField{name: "day of week", min: 0, max: 7, names: dowNames}
)

// descriptors are the @-shorthands, expanded to the five-field form so there is
// one code path for matching. @midnight is @daily's documented alias.
var descriptors = map[string]string{
	"@yearly":   "0 0 1 1 *",
	"@annually": "0 0 1 1 *",
	"@monthly":  "0 0 1 * *",
	"@weekly":   "0 0 * * 0",
	"@daily":    "0 0 * * *",
	"@midnight": "0 0 * * *",
	"@hourly":   "0 * * * *",
}

// ParseCron parses a cron expression or @-shorthand.
//
// It is exported through Service.ParseCron and is the single parser in the
// system: startup validates the code's registrations with it, the scheduler
// computes next_run_at with it, and the admin form both validates and previews
// with it. A second implementation anywhere is how "saved fine, never fires"
// happens.
func ParseCron(expr string) (Schedule, error) {
	expr = strings.TrimSpace(expr)
	if expr == "" {
		return Schedule{}, errors.New("queue: empty cron expression")
	}

	if rest, ok := cutPrefixFold(expr, "@every "); ok {
		d, err := time.ParseDuration(strings.TrimSpace(rest))
		if err != nil {
			return Schedule{}, fmt.Errorf("queue: @every: %w", err)
		}
		if d <= 0 {
			return Schedule{}, fmt.Errorf("queue: @every: duration must be positive, got %s", d)
		}
		return Schedule{every: d}, nil
	}

	if strings.HasPrefix(expr, "@") {
		expanded, ok := descriptors[strings.ToLower(expr)]
		if !ok {
			return Schedule{}, fmt.Errorf("queue: unknown descriptor %q", expr)
		}
		expr = expanded
	}

	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("queue: cron expression needs 5 fields, got %d in %q", len(parts), expr)
	}

	var s Schedule
	var err error
	if s.minute, _, err = parseCronField(parts[0], fieldMinute); err != nil {
		return Schedule{}, err
	}
	if s.hour, _, err = parseCronField(parts[1], fieldHour); err != nil {
		return Schedule{}, err
	}
	if s.dom, s.domRestricted, err = parseCronField(parts[2], fieldDOM); err != nil {
		return Schedule{}, err
	}
	if s.month, _, err = parseCronField(parts[3], fieldMonth); err != nil {
		return Schedule{}, err
	}
	if s.dow, s.dowRestricted, err = parseCronField(parts[4], fieldDOW); err != nil {
		return Schedule{}, err
	}
	// Fold Sunday-as-7 onto Sunday-as-0, so matchesDay can test
	// time.Weekday directly. Done here, on the finished mask, rather than per
	// value: at value level it would turn "5-7" into an inverted range.
	if s.dow&(1<<7) != 0 {
		s.dow = (s.dow | 1) &^ (1 << 7)
	}
	return s, nil
}

// parseCronField turns one field into a bitmask, and reports whether it was
// restricted (anything other than a bare "*"). A step on "*" still counts as
// restricted: "*/2" in the day-of-week field does narrow the days.
func parseCronField(spec string, f cronField) (mask uint64, restricted bool, err error) {
	if spec == "*" {
		return fullMask(f.min, f.max), false, nil
	}
	for _, part := range strings.Split(spec, ",") {
		if part == "" {
			return 0, false, fmt.Errorf("queue: %s: empty item in %q", f.name, spec)
		}
		m, err := parseCronItem(part, f)
		if err != nil {
			return 0, false, err
		}
		mask |= m
	}
	return mask, true, nil
}

// parseCronItem handles a single comma-free item: n, a-b, */s, a-b/s, a/s.
func parseCronItem(item string, f cronField) (uint64, error) {
	rangePart, stepPart, hasStep := strings.Cut(item, "/")
	step := uint(1)
	if hasStep {
		n, err := strconv.ParseUint(stepPart, 10, 32)
		if err != nil || n == 0 {
			return 0, fmt.Errorf("queue: %s: bad step %q in %q", f.name, stepPart, item)
		}
		step = uint(n)
	}

	var lo, hi uint
	switch rangePart {
	case "*":
		lo, hi = f.min, f.max
	default:
		loStr, hiStr, isRange := strings.Cut(rangePart, "-")
		var err error
		if lo, err = parseCronValue(loStr, f); err != nil {
			return 0, err
		}
		switch {
		case isRange:
			if hi, err = parseCronValue(hiStr, f); err != nil {
				return 0, err
			}
		case hasStep:
			// "5/15" means "from 5 to the end of the field, every 15" — the same
			// open-ended reading cron gives it. Without a step, "5" is just 5.
			hi = f.max
		default:
			hi = lo
		}
	}
	if lo > hi {
		return 0, fmt.Errorf("queue: %s: range %d-%d is inverted in %q", f.name, lo, hi, item)
	}

	var mask uint64
	for v := lo; v <= hi; v += step {
		mask |= 1 << v
	}
	return mask, nil
}

// parseCronValue resolves a number or a three-letter name, and rejects anything
// outside the field's range.
func parseCronValue(s string, f cronField) (uint, error) {
	if f.names != nil {
		if v, ok := f.names[strings.ToLower(s)]; ok {
			return v, nil
		}
	}
	n, err := strconv.ParseUint(s, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("queue: %s: %q is not a number or a known name", f.name, s)
	}
	v := uint(n)
	if v < f.min || v > f.max {
		return 0, fmt.Errorf("queue: %s: %d is out of range %d-%d", f.name, v, f.min, f.max)
	}
	return v, nil
}

func fullMask(min, max uint) uint64 {
	var m uint64
	for v := min; v <= max; v++ {
		m |= 1 << v
	}
	return m
}

// Every reports the interval of an "@every" schedule, or 0 for a cron
// expression — the one way to tell the two kinds apart without re-parsing the
// text. Nothing in the admin area needs the distinction today (the preview goes
// through Next either way), so this is here for application code that wants to
// treat an interval plan differently, and for the parser's own tests.
func (s Schedule) Every() time.Duration { return s.every }

// Next returns the first activation strictly after t, in t's location.
//
// Strictly after: the caller's t is the moment a firing was just handled, and
// returning t itself would fire the same minute twice.
//
// The search is two-level — skip whole days whose month/day/weekday do not
// match, then scan the minutes of a matching day — which is what keeps an
// expression like "0 0 29 2 *" (February 29th) from costing 2.6 million
// iterations to resolve. It is bounded at maxCronSearchYears and returns
// ErrCronUnsatisfiable past that: an expression that can never match must say
// so, not spin and not quietly never fire.
//
// Daylight saving, since scanning wall-clock minutes in a location has two
// consequences worth stating rather than discovering:
//
//   - Spring forward: a plan inside the skipped hour does not run that day. The
//     minute never exists, so nothing matches it.
//   - Fall back: a plan inside the repeated hour runs twice, an hour of real time
//     apart. Both are genuine matches at the wall-clock minute asked for.
//
// Set queue.timezone to a zone without DST (UTC) if either matters. China, the
// default audience here, has no DST at all.
func (s Schedule) Next(t time.Time) (time.Time, error) {
	if s.every > 0 {
		return t.Add(s.every), nil
	}

	// Truncate to the minute and step one forward: this parser has no seconds
	// field, so every activation is at :00 and "strictly after t" means "at the
	// next minute boundary at the earliest".
	cur := t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(maxCronSearchYears, 0, 0)

	// Day loop, then minute loop. day is midnight of the candidate day; the first
	// candidate day starts at cur so the current day is not skipped.
	day := time.Date(cur.Year(), cur.Month(), cur.Day(), 0, 0, 0, 0, cur.Location())
	for day.Before(limit) {
		if s.matchesDay(day) {
			for m := day; m.Day() == day.Day(); m = m.Add(time.Minute) {
				if m.Before(cur) {
					continue
				}
				if s.minute&(1<<uint(m.Minute())) != 0 && s.hour&(1<<uint(m.Hour())) != 0 {
					return m, nil
				}
			}
		}
		// One advance for both paths. AddDate lands on the same wall-clock hour,
		// which a DST transition can move off midnight, so re-normalise: the next
		// minute scan has to start where it thinks it does.
		day = day.AddDate(0, 0, 1)
		day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, day.Location())
	}
	return time.Time{}, ErrCronUnsatisfiable
}

// matchesDay applies the month, day-of-month and day-of-week fields.
//
// The rule that catches everyone: when BOTH day-of-month and day-of-week are
// restricted, they are OR-ed, not AND-ed. "0 0 13 * FRI" fires on the 13th of
// every month AND on every Friday — this is Vixie cron's documented behaviour
// and the single most common bug in a hand-written cron parser. When only one of
// them is restricted, only that one constrains the day.
func (s Schedule) matchesDay(day time.Time) bool {
	if s.month&(1<<uint(day.Month())) == 0 {
		return false
	}
	domOK := s.dom&(1<<uint(day.Day())) != 0
	dowOK := s.dow&(1<<uint(day.Weekday())) != 0

	switch {
	case s.domRestricted && s.dowRestricted:
		return domOK || dowOK
	case s.domRestricted:
		return domOK
	case s.dowRestricted:
		return dowOK
	default:
		return true
	}
}

// cutPrefixFold is strings.CutPrefix with an ASCII-case-insensitive prefix, so
// "@EVERY 5m" is accepted alongside "@every 5m" — the descriptors are matched
// case-insensitively too, and one of the two being picky would be a trap.
func cutPrefixFold(s, prefix string) (rest string, ok bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return "", false
	}
	return s[len(prefix):], true
}
