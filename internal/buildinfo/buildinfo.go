// Package buildinfo holds variables set at build time via -ldflags.
package buildinfo

// These variables are set by the linker at build time.
// See the Makefile for the -ldflags usage.
var (
	AppName   = "myapp"
	Version   = "dev"
	Commit    = "none"
	BuildDate = "unknown"
)
