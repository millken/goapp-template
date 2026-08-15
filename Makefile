BINARY     := myapp
MODULE     := github.com/millken/goapp-template
BUILD_PKG  := $(MODULE)/internal/buildinfo

VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")

LDFLAGS := -s -w \
	-X $(BUILD_PKG).AppName=$(BINARY) \
	-X $(BUILD_PKG).Version=$(VERSION) \
	-X $(BUILD_PKG).Commit=$(COMMIT) \
	-X $(BUILD_PKG).BuildDate=$(BUILD_DATE)

.PHONY: build build-prod frontend-build test lint clean tidy dev dev-config dev-admin update update-go update-npm

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

build-prod: frontend-build
	rm -rf server/embedded/dist
	cp -r frontend/dist server/embedded/dist
	trap 'rm -rf server/embedded/dist' EXIT; \
	go build -tags prod -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

frontend-build:
	cd frontend && pnpm build

DEV_PORT ?= 5173

# Throwaway credentials so `make dev` is usable without a manual seeding step.
# DEV ONLY — app.db is gitignored precisely so this never reaches a deployment.
ADMIN_USER ?= admin
ADMIN_PASS ?= admin

# Create config.yaml from the sample, idempotently — the same bargain dev-admin
# makes for credentials: `make dev` should work on a fresh clone without a manual
# step, and README's `cp config.example.yaml config.yaml` is now only needed if you
# want to edit it first.
#
# It never overwrites: an existing config.yaml is yours, and clobbering a tuned DSN
# to fix a missing section would be a much worse surprise than the error it avoids.
#
# That leaves one case this cannot fix, and it is worth knowing about: an existing
# config.yaml that predates a newly added component has no section for it, and
# serve refuses to start rather than degrade silently. The error names the missing
# section and where to copy it from — see the accessors in internal/service/*.
dev-config:
	@if [ ! -f config.yaml ]; then \
	  cp config.example.yaml config.yaml; \
	  echo "created config.yaml from config.example.yaml"; \
	fi

# Seed the admin login, idempotently: an existing username is reported and
# tolerated, while any other failure still stops the build.
dev-admin: dev-config
	@out=$$(MYAPP_HOME=. go run -ldflags "$(LDFLAGS)" . admin create-user $(ADMIN_USER) --password '$(ADMIN_PASS)' 2>&1); \
	case "$$out" in \
	  *"created admin user"*) echo "$$out";; \
	  *UNIQUE*|*Duplicate*|*duplicate*) echo "admin user $(ADMIN_USER) already present";; \
	  *) echo "$$out"; exit 1;; \
	esac

# Vite runs in its own process group so cleanup can signal the whole tree.
# Killing just the backgrounded job's PID reaches pnpm but not the vite node
# process it spawns, which then reparents to init and keeps DEV_PORT bound —
# including when `serve` exits non-zero, e.g. on a missing config section.
#
# `set -m` is what puts it in that group: with job control enabled, a POSIX
# shell makes each background job a process-group leader, so $! is the PGID.
# This used to be setsid(1), which does not exist on macOS — where the fallback
# was to print "kill it by hand", i.e. a stale Vite holding DEV_PORT and
# breaking the next `make dev`. `set -m` needs nothing installed and behaves the
# same on both, so there is no longer a second path to keep working.
#
# stdin is /dev/null because a background process group that reads the
# controlling terminal is stopped with SIGTTIN. Vite checks isTTY and skips its
# keyboard shortcuts, which the setsid version had already given up by
# detaching the terminal — so this costs nothing that was working before.
#
# `set +m` before `serve` so the foreground half keeps sharing the recipe
# shell's group: Ctrl-C then reaches both, and the INT trap runs cleanup.
dev: dev-admin
	@PGID=""; \
	cleanup() { \
	  if [ -n "$$PGID" ]; then kill -TERM "-$$PGID" 2>/dev/null || true; fi; \
	}; \
	trap cleanup EXIT INT TERM; \
	set -m; \
	(cd frontend && DEV_PORT=$(DEV_PORT) exec pnpm dev) </dev/null & PGID=$$!; \
	set +m; \
	VITE_DEV_ADDR=http://localhost:$(DEV_PORT) MYAPP_HOME=. go run -ldflags "$(LDFLAGS)" . serve; \
	cleanup

run:
	go run -ldflags "$(LDFLAGS)" . serve

test:
	go test ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf bin/ server/embedded/dist/

update: update-go update-npm

update-go:
	go get -u ./...
	go mod tidy

update-npm:
	cd frontend && pnpm update --latest
