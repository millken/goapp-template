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

.PHONY: build build-prod frontend-build test lint clean tidy dev update update-go update-npm

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

# Seed the admin login, idempotently: an existing username is reported and
# tolerated, while any other failure still stops the build.
dev-admin:
	@out=$$(MYAPP_HOME=. go run -ldflags "$(LDFLAGS)" . admin create-user $(ADMIN_USER) --password '$(ADMIN_PASS)' 2>&1); \
	case "$$out" in \
	  *"created admin user"*) echo "$$out";; \
	  *UNIQUE*|*Duplicate*|*duplicate*) echo "admin user $(ADMIN_USER) already present";; \
	  *) echo "$$out"; exit 1;; \
	esac

# Vite runs in its own process group (setsid) so cleanup can signal the whole
# tree. Killing just the backgrounded job's PID reaches pnpm but not the vite
# node process it spawns, which then reparents to init and keeps DEV_PORT bound —
# including when `serve` exits non-zero, e.g. on a missing config section.
# `exec` makes pnpm the group leader, so $! is the PGID.
dev: dev-admin
	@PGID=""; PID=""; \
	cleanup() { \
	  if [ -n "$$PGID" ]; then kill -TERM "-$$PGID" 2>/dev/null || true; \
	  elif [ -n "$$PID" ]; then kill "$$PID" 2>/dev/null || true; fi; \
	}; \
	trap cleanup EXIT INT TERM; \
	if command -v setsid >/dev/null 2>&1; then \
	  setsid sh -c 'cd frontend && DEV_PORT=$(DEV_PORT) exec pnpm dev' & PGID=$$!; \
	else \
	  echo ">>> setsid not found: Vite may outlive make; kill it by hand if $(DEV_PORT) stays bound"; \
	  (cd frontend && DEV_PORT=$(DEV_PORT) exec pnpm dev) & PID=$$!; \
	fi; \
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
