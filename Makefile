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

.PHONY: build build-prod frontend-build test lint clean tidy dev

build:
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .

build-prod: frontend-build
	cp -r frontend/dist server/embedded/dist
	go build -tags prod -ldflags "$(LDFLAGS)" -o bin/$(BINARY) .
	rm -rf server/embedded/dist

frontend-build:
	cd frontend && pnpm build:pages

dev:
	cd frontend && pnpm generate && pnpm dev &
	go run -ldflags "$(LDFLAGS)" . serve

test:
	go test ./...

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

clean:
	rm -rf bin/ server/embedded/dist/
