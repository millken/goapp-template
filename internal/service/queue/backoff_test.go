package queue

import (
	"math/rand/v2"
	"testing"
	"time"
)

// The exponential part is exact, so it is asserted exactly. No sleeping anywhere
// in this file: backoff produces a duration that gets written to run_at, and a
// test that waited for it would be testing time.Sleep.
func TestBackoffBase(t *testing.T) {
	const base = 15 * time.Second
	const max = time.Hour

	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, 15 * time.Second},
		{2, 30 * time.Second},
		{3, time.Minute},
		{4, 2 * time.Minute},
		{5, 4 * time.Minute},
		{6, 8 * time.Minute},
		{7, 16 * time.Minute},
		{8, 32 * time.Minute},
		{9, time.Hour},  // 64m would exceed max
		{10, time.Hour}, // and it stays there
		{50, time.Hour},
		// A corrupt or unset attempts column still yields the first delay rather
		// than zero, which would busy-retry.
		{0, 15 * time.Second},
		{-3, 15 * time.Second},
	}
	for _, c := range cases {
		if got := backoffBase(c.attempts, base, max); got != c.want {
			t.Errorf("backoffBase(%d) = %s, want %s", c.attempts, got, c.want)
		}
	}
}

// Growth must never reverse: the admin screen shows the delay, and a retry that
// waits less than the previous one reads as a bug.
func TestBackoffBase_IsMonotonic(t *testing.T) {
	const base = 15 * time.Second
	const max = time.Hour

	prev := time.Duration(-1)
	for n := 1; n <= 40; n++ {
		got := backoffBase(n, base, max)
		if got < prev {
			t.Fatalf("backoffBase(%d) = %s, less than backoffBase(%d) = %s", n, got, n-1, prev)
		}
		if got > max {
			t.Fatalf("backoffBase(%d) = %s, above max %s", n, got, max)
		}
		prev = got
	}
}

// The cap exists so the doubling cannot overflow int64 into a negative duration —
// a negative run_at offset would make the task immediately eligible forever.
func TestBackoffBase_NeverOverflowsNegative(t *testing.T) {
	for _, max := range []time.Duration{0, time.Hour, time.Duration(1) << 62} {
		for _, n := range []int{62, 63, 64, 1000, 1 << 20} {
			if got := backoffBase(n, time.Hour, max); got < 0 {
				t.Errorf("backoffBase(%d, 1h, %s) = %s, want a non-negative duration", n, max, got)
			}
		}
	}
}

// max == 0 means "no ceiling". It still must not overflow, which the shift cap
// handles.
func TestBackoffBase_ZeroMaxMeansUncapped(t *testing.T) {
	if got := backoffBase(5, time.Second, 0); got != 16*time.Second {
		t.Errorf("backoffBase(5, 1s, no max) = %s, want 16s", got)
	}
	// Across the overflow boundary the delay must saturate, not wrap. An earlier
	// version returned max on overflow, which for an uncapped policy is zero —
	// i.e. a task retrying with no delay at all, forever.
	prev := time.Duration(-1)
	for n := 55; n <= 70; n++ {
		got := backoffBase(n, time.Second, 0)
		if got <= 0 {
			t.Fatalf("backoffBase(%d, 1s, no max) = %s, want a positive duration", n, got)
		}
		if got < prev {
			t.Fatalf("backoffBase(%d) = %s, less than backoffBase(%d) = %s", n, got, n-1, prev)
		}
		prev = got
	}
}

// A base of zero disables backoff entirely rather than producing a negative or
// nonsense delay. Reached only through a misconfiguration, so it just has to be
// harmless.
func TestBackoffBase_ZeroBase(t *testing.T) {
	if got := backoffBase(3, 0, time.Hour); got != 0 {
		t.Errorf("backoffBase with a zero base = %s, want 0", got)
	}
}

// Equal jitter: the result lands in [d/2, d). The bounds are the contract — the
// lower one is what keeps the delay monotonic across attempts, the upper one is
// what keeps it from exceeding retry_max.
func TestApplyJitter_StaysInTheEqualJitterBand(t *testing.T) {
	const d = time.Hour
	// Exercise the extremes of the injected source rather than sampling: 0 and
	// half-1 are the only two values that can fall outside the band if the
	// arithmetic is wrong.
	for _, n := range []int64{0, int64(d/2) - 1} {
		got := applyJitter(d, func(int64) int64 { return n })
		if got < d/2 || got >= d {
			t.Errorf("applyJitter with jitter=%d = %s, want [%s, %s)", n, got, d/2, d)
		}
	}

	// And a real source, to catch an off-by-one the two extremes would miss.
	src := rand.New(rand.NewPCG(1, 2))
	for range 1000 {
		got := applyJitter(d, src.Int64N)
		if got < d/2 || got >= d {
			t.Fatalf("applyJitter = %s, want [%s, %s)", got, d/2, d)
		}
	}
}

// Durations too small to split are returned unchanged. Without this, half == 0
// would reach math/rand/v2's Int64N, which panics for n <= 0.
func TestApplyJitter_TinyDurations(t *testing.T) {
	panicking := func(int64) int64 { panic("jitter must not be consulted for a duration this small") }
	for _, d := range []time.Duration{0, 1, -time.Second} {
		if got := applyJitter(d, panicking); got != d {
			t.Errorf("applyJitter(%s) = %s, want it unchanged", d, got)
		}
	}
}

// The production jitter source is the one that matters for concurrency: every
// executor goroutine computes its own backoff, so it has to be safe to call from
// several at once. math/rand/v2's package-level Int64N is; a *rand.Rand would
// have needed a mutex.
func TestRandomJitter_IsUsableConcurrently(t *testing.T) {
	const goroutines = 8
	done := make(chan struct{}, goroutines)
	for range goroutines {
		go func() {
			defer func() { done <- struct{}{} }()
			for range 500 {
				if n := randomJitter(1000); n < 0 || n >= 1000 {
					t.Errorf("randomJitter(1000) = %d, out of range", n)
					return
				}
			}
		}()
	}
	for range goroutines {
		<-done
	}
}

// backoff composes the two halves. Pinned with a fixed jitter so the assertion is
// exact rather than a range.
func TestBackoff_Composes(t *testing.T) {
	zero := func(int64) int64 { return 0 }
	// attempts=3 → base 60s → equal jitter with 0 → the floor of the band, 30s.
	if got := backoff(3, 15*time.Second, time.Hour, zero); got != 30*time.Second {
		t.Errorf("backoff(3) with zero jitter = %s, want 30s", got)
	}
	// And the ceiling: max is 1h, so no attempt count can produce more.
	almostAll := func(n int64) int64 { return n - 1 }
	if got := backoff(99, 15*time.Second, time.Hour, almostAll); got >= time.Hour {
		t.Errorf("backoff(99) = %s, want strictly below retry_max 1h", got)
	}
}
