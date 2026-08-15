package queue

import (
	"math"
	"math/rand/v2"
	"time"
)

// Retry backoff: exponential, then jittered.
//
// Split into a pure exponential part and a jitter part on purpose. The
// exponential part is what the tests assert exactly; the jitter part is the only
// place randomness enters, and it is injected so a test can pin it without
// seeding anything global. Nothing here sleeps — the delay is written to
// queue_tasks.run_at and the task is simply not eligible until then.

// jitterFunc returns a value in [0, n). It matches math/rand/v2's Int64N so the
// production value is that function itself.
type jitterFunc func(n int64) int64

// randomJitter is the default. The package-level function in math/rand/v2 is
// safe for concurrent use, which matters because every executor goroutine
// computes its own backoff; a *rand.Rand would need a mutex to be used the same
// way, and a mutex on the retry path buys nothing.
func randomJitter(n int64) int64 { return rand.Int64N(n) }

// maxBackoffShift caps the exponent. 1<<62 nanoseconds is ~146 years, so any
// real base shifted this far is already past every plausible retry_max — the cap
// exists to stop the shift from overflowing int64 into a negative duration, not
// to express a policy.
const maxBackoffShift = 62

// backoffBase is the un-jittered delay before attempt number attempts+1, i.e.
// after `attempts` have already been made:
//
//	min(base * 2^(attempts-1), max)
//
// attempts is 1-based, so the first retry waits one base interval. Values below 1
// are treated as 1 rather than rejected: the caller reads attempts out of a
// database column, and a delay computed from a corrupt row should still be a
// sane delay.
//
// The doubling stops as soon as it reaches max, so it neither overflows nor
// depends on how large attempts got.
func backoffBase(attempts int, base, max time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	if attempts < 1 {
		attempts = 1
	}
	// ceiling is what the doubling saturates at. With no configured max that is
	// the largest representable duration, NOT max itself: returning max (zero)
	// on overflow would make the task immediately eligible forever, which is the
	// same hazard the shift cap exists to prevent, reached through another door.
	ceiling := time.Duration(math.MaxInt64)
	if max > 0 {
		ceiling = max
	}

	d := base
	for range min(attempts-1, maxBackoffShift) {
		if d >= ceiling {
			break
		}
		next := d * 2
		if next < d { // wrapped past MaxInt64
			d = ceiling
			break
		}
		d = next
	}
	return min(d, ceiling)
}

// applyJitter spreads a delay over [d/2, d) — "equal jitter".
//
// Equal rather than full jitter (which would spread over [0, d)) because full
// jitter can follow a one-hour backoff with a one-second retry. That is correct
// for decorrelation and looks like a bug in the admin screen, where the operator
// sees a task marked "retrying in 1h" come back immediately. Equal jitter
// decorrelates a batch of tasks just as well while keeping the delay monotonic in
// the attempt number, which is the property anyone reading the screen assumes.
//
// A d of one nanosecond or less is returned unchanged: there is nothing to spread.
func applyJitter(d time.Duration, jitter jitterFunc) time.Duration {
	half := d / 2
	if half <= 0 {
		return d
	}
	return half + time.Duration(jitter(int64(half)))
}

// backoff is the delay to use before the next attempt of a task that has already
// been tried `attempts` times.
func backoff(attempts int, base, max time.Duration, jitter jitterFunc) time.Duration {
	return applyJitter(backoffBase(attempts, base, max), jitter)
}
