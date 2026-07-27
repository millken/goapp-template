package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// The three properties worth pinning: the boundary, that a success forgives, and
// that a storage failure lets you in rather than locking everyone out.
func TestLoginThrottle(t *testing.T) {
	_, adm := loginStack(t)
	ctx := context.Background()
	const ip = "203.0.113.7"

	for range loginMaxAttempts - 1 {
		adm.recordLoginFailure(ctx, ip)
	}
	if blocked, _ := adm.loginBlocked(ctx, ip); blocked {
		t.Fatalf("blocked at %d failures, the limit is %d", loginMaxAttempts-1, loginMaxAttempts)
	}

	adm.recordLoginFailure(ctx, ip)
	blocked, retry := adm.loginBlocked(ctx, ip)
	if !blocked {
		t.Error("not blocked at the limit")
	}
	if retry <= 0 || retry > loginWindow {
		t.Errorf("retryAfter = %v, want a wait inside the window", retry)
	}

	// Counting per address is the whole design — a neighbour must be unaffected.
	if blocked, _ := adm.loginBlocked(ctx, "198.51.100.1"); blocked {
		t.Error("a different address was blocked")
	}

	adm.clearLoginFailures(ctx, ip)
	if blocked, _ := adm.loginBlocked(ctx, ip); blocked {
		t.Error("a successful sign-in did not forgive the address")
	}

	// Older than the window is not a recent attempt; without this the throttle
	// would be a permanent ban rather than a rate limit.
	stale := time.Now().Add(-2 * loginWindow).UnixNano()
	for range loginMaxAttempts + 5 {
		if _, err := adm.DB.ExecContext(ctx,
			`INSERT INTO login_attempts (ip, at) VALUES (?, ?)`, ip, stale); err != nil {
			t.Fatal(err)
		}
	}
	if blocked, _ := adm.loginBlocked(ctx, ip); blocked {
		t.Error("attempts older than the window still count")
	}
}

func TestLoginThrottle_FailsOpen(t *testing.T) {
	_, adm := loginStack(t)
	if err := adm.DB.Close(); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := adm.loginBlocked(context.Background(), "203.0.113.9"); blocked {
		t.Error("a storage failure blocked the attempt; a rate limit must fail open")
	}
}

// Believing X-Forwarded-For from an untrusted peer would let anyone pick the
// bucket they are counted in, which is the one way to make this throttle
// worthless while it still looks like protection.
func TestClientIP(t *testing.T) {
	for _, c := range []struct {
		name    string
		trusted []string
		remote  string
		xff     string
		want    string
	}{
		{"no trust configured ignores the header", nil, "203.0.113.7:1234", "1.2.3.4", "203.0.113.7"},
		{"untrusted peer ignores it too", []string{"10.0.0.0/8"}, "198.51.100.5:9999", "1.2.3.4", "198.51.100.5"},
		{"trusted peer yields the client", []string{"10.0.0.0/8"}, "10.0.0.1:1234", "203.0.113.7", "203.0.113.7"},
		{"walks left past trusted hops", []string{"10.0.0.0/8"}, "10.0.0.1:1234", "203.0.113.7, 10.0.0.9, 10.0.0.8", "203.0.113.7"},
		{"a malformed hop falls back to the peer", []string{"10.0.0.0/8"}, "10.0.0.1:1234", "not-an-ip", "10.0.0.1"},
	} {
		t.Run(c.name, func(t *testing.T) {
			a := New(nil, &Config{Mount: "/admin", TrustedProxies: c.trusted})
			if err := a.Validate(); err != nil {
				t.Fatalf("validate: %v", err)
			}
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.RemoteAddr = c.remote
			r.Header.Set("X-Forwarded-For", c.xff)
			if got := a.clientIP(r); got != c.want {
				t.Errorf("clientIP = %q, want %q", got, c.want)
			}
		})
	}
}

// A typo that silently emptied the trust list would silently disable the
// throttle's only defence, so it stops startup instead.
func TestValidate_RejectsABadPrefix(t *testing.T) {
	a := New(nil, &Config{Mount: "/admin", TrustedProxies: []string{"10.0.0.0/8", "nonsense"}})
	if err := a.Validate(); err == nil {
		t.Error("an unparseable trusted_proxies entry must fail startup")
	}
}

// Through the real handler, since the gate sits ahead of everything else in
// LoginSubmit and an ordering mistake there would leave it unreachable.
func TestLoginThrottle_RefusesThroughTheHandler(t *testing.T) {
	eng, adm := loginStack(t)
	ctx := context.Background()

	// httptest gives every request the same RemoteAddr, which is the address the
	// throttle counts.
	ip := adm.clientIP(httptest.NewRequest(http.MethodGet, "/", nil))
	for range loginMaxAttempts {
		adm.recordLoginFailure(ctx, ip)
	}

	r, _ := postForm(t, eng, nil, "/admin/login",
		url.Values{"username": {"alice"}, "password": {"pw"}})
	w := httptest.NewRecorder()
	eng.ServeHTTP(w, r)

	if w.Code != http.StatusTooManyRequests {
		t.Errorf("status = %d, want 429 — even the right password waits", w.Code)
	}
	if !strings.Contains(w.Body.String(), "尝试次数过多") {
		t.Error("the page does not say why, so there is nothing to act on")
	}
}
