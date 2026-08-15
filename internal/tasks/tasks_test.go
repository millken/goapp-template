package tasks

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/service/queue"
)

// registered builds the real registry from the real Register, so every assertion below
// is about what this application actually declares.
//
// svc holds only Log, which is all Register may touch: it is called before
// queue.Service.Start, so no other field is populated yet. Passing a container with
// nothing else set is therefore not a shortcut — it is the contract, and this is where
// a handler that dereferenced svc.DB at registration time would be caught.
func registered(t *testing.T) *queue.Registry {
	t.Helper()
	r := queue.NewRegistry()
	Register(r, app.NewServices(slog.Default()))
	return r
}

// The cheapest possible enforcement of the rule the whole cron design rests on: a plan
// may only fire a kind that has a handler.
//
// queue.Service.Start refuses this too, but that is a startup failure — someone has to
// deploy to find it. Here it is a build failure, which is where a wiring mistake in a
// wiring file belongs. It also covers the expression: every registered spec has to
// parse, and Register itself panics if one does not, so reaching this line at all is
// already most of the assertion.
func TestRegister_EveryCronHasAHandlerAndAValidExpression(t *testing.T) {
	if err := registered(t).Validate(); err != nil {
		t.Fatalf("the application's task registrations are inconsistent: %v", err)
	}
}

// Register must be callable more than once on fresh registries — `serve` and
// `queue worker` are separate processes, but a test suite or a future in-process
// second worker would do it twice in one program.
func TestRegister_IsCallableRepeatedly(t *testing.T) {
	first := registered(t)
	second := registered(t)

	if len(first.Kinds()) == 0 {
		t.Fatal("no kinds registered")
	}
	if strings.Join(first.Kinds(), ",") != strings.Join(second.Kinds(), ",") {
		t.Errorf("two calls produced different registries: %v vs %v",
			first.Kinds(), second.Kinds())
	}
}

// Both composition roots must see the same list, which is the reason this package
// exists. Pinning the example kinds is not the point — the point is that a kind added
// to Register shows up in Kinds(), because Kinds() is what the claim query filters on
// and what the admin screens offer as a filter.
func TestRegister_KindsAreDiscoverable(t *testing.T) {
	kinds := registered(t).Kinds()
	for _, want := range []string{"example:echo", "example:heartbeat"} {
		if !contains(kinds, want) {
			t.Errorf("kind %q is registered but not reported by Kinds(): %v", want, kinds)
		}
	}
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
