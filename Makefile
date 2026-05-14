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
	cd frontend && pnpm build:pages

DEV_PORT ?= 5173

dev:
	cd frontend && pnpm generate && pnpm build:ssr
	@VITE_PID=""; \
	cleanup() { [ -n "$$VITE_PID" ] && kill "$$VITE_PID" 2>/dev/null || true; }; \
	trap cleanup EXIT INT TERM; \
	(cd frontend && DEV_PORT=$(DEV_PORT) pnpm dev) & VITE_PID=$$!; \
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
