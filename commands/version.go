package commands

import (
	"fmt"

	"github.com/millken/goapp-template/internal/buildinfo"
	"github.com/spf13/cobra"
)

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("%s %s\n", buildinfo.AppName, buildinfo.Version)
			fmt.Printf("  %-10s %s\n", "commit:", buildinfo.Commit)
			fmt.Printf("  %-10s %s\n", "built:", buildinfo.BuildDate)
		},
	}
}
