# goapp-template

A minimal Go CLI application template using [Cobra](https://github.com/spf13/cobra).

## Structure

```
.
├── main.go                  # Entry point
├── commands/
│   ├── paths.go             # App home directory helpers (~/.myapp/)
│   ├── root.go              # Root command + logging setup
│   └── version.go           # version subcommand
└── internal/
    └── buildinfo/
        └── buildinfo.go     # Version variables injected via ldflags
```

## Getting started

1. Clone the template and rename the module:
   ```bash
   git clone https://github.com/millken/goapp-template myapp
   cd myapp
   ```

2. Replace all occurrences of the module path and app name:
   - `go.mod`: `module github.com/millken/goapp-template` → your module
   - `commands/paths.go`: `AppName = "myapp"` → your binary name
   - `commands/root.go`: `Short` description

3. Install dependencies:
   ```bash
   go mod tidy
   ```

## Build

```bash
make build        # produces bin/myapp with version info
make test         # run tests
make lint         # run golangci-lint
```

## Configuration

At startup the app loads `~/.myapp/.env` for environment variables (optional).
The config file path is `~/.myapp/config.yaml`.

## Adding subcommands

Create a new file under `commands/` and register it in `New()`:

```go
root.AddCommand(newServeCmd())
```
