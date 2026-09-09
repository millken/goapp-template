// Command goappctl turns a fresh clone of goapp-template into a project.
//
// It is an in-place transform on the current directory: `init` trims the clone
// to the components you select and rewrites the project identity. There is no
// embedded copy of the template and no sync step — the tool ships inside the
// template it transforms, so the two cannot drift.
//
//	git clone <goapp-template> myapp && cd myapp
//	go run ./cmd/goappctl init --module github.com/me/myapp --with db,session,admin,ssr
//
// `gen` then scaffolds resources into an initialized project, including one it
// did not create — project shape is detected from the directory layout.
package main

import (
	"bufio"
	"fmt"
	"os"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"

	"github.com/millken/goapp-template/cmd/goappctl/internal/components"
	"github.com/millken/goapp-template/cmd/goappctl/internal/initcmd"
)

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:               "goappctl",
		Short:             "Turn a goapp-template clone into a project",
		SilenceUsage:      true,
		SilenceErrors:     true,
		DisableAutoGenTag: true,
	}
	root.CompletionOptions.HiddenDefaultCmd = true
	root.AddCommand(newInitCmd(), newGenCmd(), newVersionCmd())
	return root
}

func newInitCmd() *cobra.Command {
	var o initcmd.Options
	var with string

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Trim the current directory to the selected components",
		Long: "Transforms the current directory in place: deletes the components you did not\n" +
			"select, strips their wiring, rewrites the module path and app name, then verifies\n" +
			"the result builds. Refuses to run unless the directory is a pristine template\n" +
			"clone with a clean worktree (see --force).",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			o.Out = cmd.OutOrStdout()
			if cmd.Flags().Changed("with") {
				o.With = splitList(with)
			} else {
				selected, err := promptComponents(cmd)
				if err != nil {
					return err
				}
				o.With = selected
			}
			return initcmd.Run(o)
		},
	}
	f := cmd.Flags()
	f.StringVar(&o.Module, "module", "", "New module path, e.g. github.com/me/myapp (required)")
	f.StringVar(&o.Name, "name", "", "New app/binary name (default: last segment of --module)")
	f.StringVar(&with, "with", "", "Comma-separated components: "+strings.Join(components.Names(), ",")+
		" (omit for an interactive checklist)")
	f.BoolVar(&o.DryRun, "dry-run", false, "Print the plan and exit without writing")
	f.BoolVar(&o.Force, "force", false, "Proceed even if the git worktree is dirty or absent")
	f.BoolVar(&o.GitReinit, "git-reinit", false, "Replace the template's git history with a fresh repo")
	_ = cmd.MarkFlagRequired("module")
	return cmd
}

func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the goappctl version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, _ []string) {
			v := "(devel)"
			if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" {
				v = bi.Main.Version
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "goappctl", v)
		},
	}
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// promptComponents asks per component when --with was omitted. Dependencies are
// resolved afterwards by initcmd, which reports what it added, rather than being
// auto-checked here — the user sees one clear message instead of the checklist
// mutating under them.
func promptComponents(cmd *cobra.Command) ([]string, error) {
	stat, err := os.Stdin.Stat()
	if err != nil || stat.Mode()&os.ModeCharDevice == 0 {
		return nil, fmt.Errorf("--with is required when stdin is not a terminal, e.g. --with %s",
			strings.Join(components.Names(), ","))
	}
	out := cmd.OutOrStdout()
	_, _ = fmt.Fprintln(out, "Select components (y/N):")
	in := bufio.NewScanner(os.Stdin)
	var selected []string
	for _, c := range components.All {
		suffix := ""
		if len(c.Deps) > 0 {
			suffix = fmt.Sprintf(" [needs %s]", strings.Join(c.Deps, "+"))
		}
		_, _ = fmt.Fprintf(out, "  %s%s? ", c.Name, suffix)
		if !in.Scan() {
			break
		}
		if answer := strings.ToLower(strings.TrimSpace(in.Text())); answer == "y" || answer == "yes" {
			selected = append(selected, c.Name)
		}
	}
	if err := in.Err(); err != nil {
		return nil, err
	}
	return selected, nil
}
