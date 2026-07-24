package app

import "context"

// Module is the only required contract: it attaches routes/middleware to the
// App. Register must not perform IO (so that Use fails fast and stays testable).
//
// Convention: modules do not read configuration from the App — module config is
// constructor-injected. The App only exposes Engine/Logger/middleware surfaces.
type Module interface {
	Register(a *App) error
}

// Booter is an optional lifecycle hook (asserted on demand, like http.Pusher).
// Boot runs before Serve: open connection pools, connect to DB/Redis, run
// migrations… Boot is called in Module registration order.
type Booter interface {
	Module
	Boot(ctx context.Context) error
}

// Shutdowner is an optional lifecycle hook (asserted on demand).
// Shutdown runs after Serve returns, in reverse registration order, releasing
// resources (close pools…).
type Shutdowner interface {
	Module
	Shutdown(ctx context.Context) error
}
