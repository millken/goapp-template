package commands

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"text/tabwriter"
	"time"

	"github.com/millken/goapp-template/internal/app"
	"github.com/millken/goapp-template/internal/service/db"
	"github.com/millken/goapp-template/internal/service/queue"
	//goappctl:storage
	"github.com/millken/goapp-template/internal/service/storage"
	//goappctl:end
	"github.com/millken/goapp-template/internal/tasks"
	"github.com/spf13/cobra"
)

// The queue command tree: a worker process, and enough operational commands to
// diagnose a queue without the admin area.
//
// That last part is the point of `ls` and `retry` existing at all. The queue component
// does not depend on admin — a build with a worker and no management screens is
// deliberately supported — so without these, such a deployment would have no way to
// look at a failure except SQL. They also make the whole feature verifiable under
// `make dev`: enqueue something, watch it run.
//
// Each subcommand is its own small composition root, in the same shape as
// admin_user.go: build the db service, assemble just enough of the container, do the
// work, stop. None of them starts the HTTP engine.

// newQueueCmd builds the `queue` command tree.
func newQueueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Task queue (worker, enqueue, ls, retry, kinds)",
	}
	cmd.AddCommand(
		newQueueWorkerCmd(),
		newQueueEnqueueCmd(),
		newQueueLsCmd(),
		newQueueRetryCmd(),
		newQueueKindsCmd(),
	)
	return cmd
}

// queueEnv is the slice of the application a queue command needs.
//
// The service container itself is deliberately not a field: the handlers reach it
// through the closure tasks.Register built, and a command that reads it directly
// would be reaching around the registry.
type queueEnv struct {
	queue *queue.Service
	stop  func()
}

// openQueue assembles the services a queue command needs and returns a stop function.
//
// Deliberately NOT the session service: a session is a per-request concept and a
// background task has no request. Storage IS started, because a handler that resizes
// an upload needs it — which is the general rule here, "whatever a handler might
// touch".
//
// The registry comes from internal/tasks, the same call serve.go makes. That shared
// call is the whole reason that package exists: a worker with a different registry
// from the web process would silently refuse to claim kinds the web process enqueues.
func openQueue(ctx context.Context, log *slog.Logger, concurrency int) (*queueEnv, error) {
	if appCfg.DB == nil {
		return nil, fmt.Errorf("no [db] config section; the queue is a database queue")
	}
	if appCfg.Queue == nil {
		return nil, fmt.Errorf("no [queue] config section")
	}

	var stops []func()
	stop := func() {
		// Reverse order, as runServe's defers do.
		for i := len(stops) - 1; i >= 0; i-- {
			stops[i]()
		}
	}

	dbSvc := db.New(appCfg.DB)
	if err := dbSvc.Start(ctx); err != nil {
		return nil, err
	}
	stops = append(stops, func() { _ = dbSvc.Stop(context.Background()) })

	svc := app.NewServices(log)
	svc.DB = dbSvc.DB()

	//goappctl:storage
	if appCfg.Storage != nil {
		storSvc := storage.New(appCfg.Storage)
		if err := storSvc.Start(ctx); err != nil {
			stop()
			return nil, err
		}
		stops = append(stops, func() { _ = storSvc.Stop(context.Background()) })
		svc.Storage = storSvc
	}
	//goappctl:end

	// concurrency overrides the config value, so a deployment can set 0 in the file
	// (serve serves, runs nothing) and still start a worker from the command line
	// without a second config.
	cfg := *appCfg.Queue
	if concurrency >= 0 {
		cfg.Concurrency = &concurrency
	}

	reg := queue.NewRegistry()
	var opts []queue.Option
	if dbSvc.MigrationsEnabled() {
		opts = append(opts, queue.WithMigrations(dbSvc.MigrationTable()))
	}
	qsvc := queue.New(&cfg, svc.DB, reg, log, opts...)
	// Before Start, and before Register: a handler closes over svc and reads
	// svc.Queue when it runs. Same ordering requirement as serve.go.
	svc.Queue = qsvc
	tasks.Register(reg, svc)

	if err := qsvc.Start(ctx); err != nil {
		stop()
		return nil, err
	}
	stops = append(stops, func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), queueStopTimeout)
		defer cancel()
		if err := qsvc.Stop(stopCtx); err != nil {
			log.Warn("queue did not stop cleanly", "err", err)
		}
	})

	return &queueEnv{queue: qsvc, stop: stop}, nil
}

// openQueueReadOnly is openQueue with the worker off, for commands that only look at
// or edit rows. Starting a worker for `queue ls` would have it claim and run tasks as
// a side effect of listing them.
func openQueueReadOnly(ctx context.Context, log *slog.Logger) (*queueEnv, error) {
	return openQueue(ctx, log, 0)
}

func newQueueWorkerCmd() *cobra.Command {
	concurrency := -1

	cmd := &cobra.Command{
		Use:   "worker",
		Short: "Run a worker process (tasks, cron, lease reaping, retention)",
		Long: "Run a worker process.\n\n" +
			"Runs the same handlers the embedded worker in `serve` does, so the two are " +
			"interchangeable: point this at the same database and it picks up work " +
			"alongside (or instead of) the web process. Cron firing, lease reaping and " +
			"retention pruning all live in the worker, so at least one process must be " +
			"running one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := openQueue(ctx, slog.Default(), concurrency)
			if err != nil {
				return err
			}
			defer env.stop()

			if env.queue.Concurrency() == 0 {
				return fmt.Errorf("concurrency is 0, so this worker would run nothing; " +
					"pass --concurrency or set queue.concurrency")
			}

			slog.Info("queue worker running; press Ctrl-C to stop")
			// Block until the signal context main.go installed is cancelled. The
			// deferred stop then drains: it stops claiming, waits out
			// queue.shutdown_grace for in-flight tasks, and only then cancels them.
			<-ctx.Done()
			slog.Info("queue worker stopping")
			return nil
		},
	}
	cmd.Flags().IntVar(&concurrency, "concurrency", -1,
		"Tasks to run at once (overrides queue.concurrency)")
	return cmd
}

func newQueueEnqueueCmd() *cobra.Command {
	var delay time.Duration
	var maxAttempts int
	var priority int
	var uniqueKey string

	cmd := &cobra.Command{
		Use:   "enqueue <kind> [json-payload]",
		Short: "Enqueue one task",
		Long: "Enqueue one task.\n\n" +
			"The payload is passed through as-is, so it must be valid JSON for whatever " +
			"the handler decodes. An unregistered kind is accepted: the task waits until " +
			"a process that handles it runs, which is what makes deploying a producer " +
			"ahead of its consumer safe.",
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := openQueueReadOnly(ctx, slog.Default())
			if err != nil {
				return err
			}
			defer env.stop()

			payload := "{}"
			if len(args) == 2 {
				payload = args[1]
			}

			var opts []queue.EnqueueOption
			if delay > 0 {
				opts = append(opts, queue.After(delay))
			}
			if maxAttempts > 0 {
				opts = append(opts, queue.MaxAttempts(maxAttempts))
			}
			if priority != 0 {
				opts = append(opts, queue.Priority(priority))
			}
			if uniqueKey != "" {
				opts = append(opts, queue.Unique(uniqueKey))
			}

			id, err := env.queue.Enqueue(ctx, args[0], payload, opts...)
			if err != nil {
				return err
			}
			if !env.queue.KnownKind(args[0]) {
				fmt.Fprintf(os.Stderr,
					"warning: no handler for kind %q in this process; the task will wait\n",
					args[0])
			}
			fmt.Printf("enqueued task %d (%s)\n", id, args[0])
			return nil
		},
	}
	cmd.Flags().DurationVar(&delay, "delay", 0, "Run no earlier than this from now")
	cmd.Flags().IntVar(&maxAttempts, "max-attempts", 0, "Attempt ceiling (0 = the configured default)")
	cmd.Flags().IntVar(&priority, "priority", 0, "Higher runs first among due tasks")
	cmd.Flags().StringVar(&uniqueKey, "unique", "", "Idempotency key; a second task with the same key is refused")
	return cmd
}

func newQueueLsCmd() *cobra.Command {
	var status, kind string
	var limit, page int

	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List tasks",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := openQueueReadOnly(ctx, slog.Default())
			if err != nil {
				return err
			}
			defer env.stop()

			if status != "" && !queue.ValidStatus(status) {
				return fmt.Errorf("unknown status %q (pending, running, succeeded, dead, cancelled)", status)
			}

			result, err := env.queue.ListTasks(ctx, queue.TaskFilter{
				Status: status, Kind: kind, Page: page, PageSize: limit,
			})
			if err != nil {
				return err
			}

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tKIND\tSTATUS\tTRIES\tRUN AT\tERROR")
			for _, it := range result.Items {
				fmt.Fprintf(w, "%d\t%s\t%s\t%d/%d\t%s\t%s\n",
					it.ID, it.Kind, it.Status, it.Attempts, it.MaxAttempts,
					cliTime(it.RunAt), truncateForCLI(it.LastError, 60))
			}
			if err := w.Flush(); err != nil {
				return err
			}
			fmt.Printf("\npage %d of %d, %d task(s) total\n",
				result.Page, max(1, (result.Total+result.PageSize-1)/result.PageSize), result.Total)
			return nil
		},
	}
	cmd.Flags().StringVar(&status, "status", "", "Filter by status")
	cmd.Flags().StringVar(&kind, "kind", "", "Filter by kind")
	cmd.Flags().IntVar(&limit, "limit", 25, "Rows per page")
	cmd.Flags().IntVar(&page, "page", 1, "Page number (1-based)")
	return cmd
}

func newQueueRetryCmd() *cobra.Command {
	var extra int

	cmd := &cobra.Command{
		Use:   "retry <id>...",
		Short: "Requeue finished or cancelled tasks",
		Long: "Requeue finished or cancelled tasks.\n\n" +
			"The attempt count is not reset — it is the audit trail, and the attempt log " +
			"is numbered from it. The ceiling is raised instead, which is also the more " +
			"accurate reading of the request: give it another few goes.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := openQueueReadOnly(ctx, slog.Default())
			if err != nil {
				return err
			}
			defer env.stop()

			var failed int
			for _, arg := range args {
				id, err := strconv.ParseInt(arg, 10, 64)
				if err != nil {
					return fmt.Errorf("%q is not a task id", arg)
				}
				ok, err := env.queue.RetryTask(ctx, id, extra)
				if err != nil {
					return err
				}
				if !ok {
					// Not an error: a task that is pending or running is simply not in a
					// state where "retry" means anything, and one id in a list failing
					// should not abandon the rest.
					fmt.Fprintf(os.Stderr,
						"task %d was not requeued (missing, or still pending/running)\n", id)
					failed++
					continue
				}
				fmt.Printf("requeued task %d\n", id)
			}
			if failed == len(args) {
				return fmt.Errorf("no tasks were requeued")
			}
			return nil
		},
	}
	cmd.Flags().IntVar(&extra, "attempts", 1, "How many further attempts to allow")
	return cmd
}

func newQueueKindsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "kinds",
		Short: "List the handlers and cron plans this build registers",
		Long: "List the handlers and cron plans this build registers.\n\n" +
			"The first thing to check when tasks are not moving: a worker only claims " +
			"kinds it has a handler for, so a kind missing from this list explains a " +
			"queue that is filling up and not draining.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			env, err := openQueueReadOnly(ctx, slog.Default())
			if err != nil {
				return err
			}
			defer env.stop()

			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "KIND")
			for _, kind := range env.queue.Kinds() {
				fmt.Fprintf(w, "%s\n", kind)
			}
			if err := w.Flush(); err != nil {
				return err
			}

			plans, err := env.queue.Schedules(ctx)
			if err != nil {
				return err
			}
			if len(plans) > 0 {
				fmt.Println()
				w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "SCHEDULE\tKIND\tSPEC\tENABLED\tNEXT RUN\tNOTE")
				for _, p := range plans {
					note := ""
					switch {
					case !p.Present:
						note = "removed from the code"
					case !p.KnownKind:
						note = "no handler here"
					case p.Drifted:
						note = "overridden (code: " + p.CodeSpec + ")"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%t\t%s\t%s\n",
						p.Name, p.Kind, p.Spec, p.Enabled, cliTime(p.NextRunAt), note)
				}
				if err := w.Flush(); err != nil {
					return err
				}
			}

			// Orphans are the other half of the same diagnosis: kinds sitting in the
			// queue that nothing here can run.
			orphans, err := env.queue.OrphanCounts(ctx)
			if err != nil {
				return err
			}
			if len(orphans) > 0 {
				fmt.Println("\npending tasks with no handler in this process:")
				for kind, n := range orphans {
					fmt.Printf("  %s: %d\n", kind, n)
				}
			}
			return nil
		},
	}
}

// cliTime renders a UnixNano stamp for a terminal. Local time, unlike the admin
// screens' fixed format, because a person reading a terminal is reading it now.
// Zero renders as a dash rather than 1970: a task that has never run must not look
// like it ran at the epoch.
func cliTime(ns int64) string {
	if ns == 0 {
		return "-"
	}
	return time.Unix(0, ns).Format("2006-01-02 15:04:05")
}

// truncateForCLI keeps a table row on one line. Rune-aware so a multi-byte error
// message is not cut into invalid UTF-8 mid-character.
func truncateForCLI(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}
