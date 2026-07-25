// Package buildinfo holds variables set at build time via -ldflags (see Makefile).
package buildinfo

var (
	AppName   = "myapp"
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)
