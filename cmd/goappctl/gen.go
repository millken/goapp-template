package main

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/millken/goapp-template/cmd/goappctl/internal/scaffold"
)

// newGenCmd builds the `gen` command tree. Unlike init, gen runs against an
// already-initialized project — including one it has never seen, since project
// shape is detected from the directory layout rather than any metadata file.
func newGenCmd() *cobra.Command {
	var (
		force   bool
		noMount bool
		dryRun  bool
		root    string
	)

	gen := &cobra.Command{
		Use:   "gen",
		Short: "Scaffold resources into an existing project",
	}

	resourceCmd := &cobra.Command{
		Use:     "resource <name>",
		Aliases: []string{"mvc"},
		Short:   "Generate a public CRUD resource (handler + model + Vue pages)",
		Long: "Generates internal/controller/<name>/{handler,model}.go and\n" +
			"frontend/pages/<name>/{index,form}.vue, then registers the area in\n" +
			"internal/controller/mount_gen.go. Re-running for the same resource does not\n" +
			"duplicate the registration.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := scaffold.Detect(root)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			warnNoDB(out, p)

			spec, err := scaffold.NewSpec(args[0])
			if err != nil {
				return err
			}
			if err := scaffold.Resource(args[0], scaffold.Options{Force: force, ModuleRoot: p.Root, Module: p.Module}); err != nil {
				return err
			}
			fmt.Fprintf(out, "generated resource %s\n", spec.Package)

			if noMount {
				fmt.Fprintf(out, "\nnot mounted (--no-mount); add this inside the gen:mounts region of %s:\n    %s.Mount(eng, svc)\n",
					scaffold.MountGenPath, spec.Package)
				return nil
			}
			added, err := scaffold.AddMount(p.Root, p.Module, spec.Package)
			if err != nil {
				return err
			}
			if added {
				fmt.Fprintf(out, "mounted in %s\n", scaffold.MountGenPath)
			} else {
				fmt.Fprintf(out, "already mounted in %s\n", scaffold.MountGenPath)
			}
			return nil
		},
	}

	adminCmd := &cobra.Command{
		Use:   "admin <name>",
		Short: "Generate an admin CRUD resource (auth-guarded, under the admin mount)",
		Long: "Generates internal/controller/admin<name>/ and frontend/pages/admin/<name>/.\n" +
			"Admin areas mount inside serve.go rather than the gen:mounts region, so the\n" +
			"wiring line is printed for you to add.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := scaffold.Detect(root)
			if err != nil {
				return err
			}
			if !p.HasAdmin {
				return fmt.Errorf("this project has no internal/controller/admin/ — it was built without the admin component, so there is no admin area to extend")
			}
			out := cmd.OutOrStdout()
			warnNoDB(out, p)

			spec, err := scaffold.NewSpec(args[0])
			if err != nil {
				return err
			}
			if err := scaffold.Admin(args[0], scaffold.Options{Force: force, ModuleRoot: p.Root, Module: p.Module}); err != nil {
				return err
			}
			pkg := "admin" + spec.Package
			fmt.Fprintf(out, "generated admin resource %s\n", pkg)
			fmt.Fprintf(out, "\nadd this to commands/serve.go, after adm.Mount(eng):\n    %s.Mount(eng, svc, adm)\n", pkg)
			return nil
		},
	}

	uiCmd := &cobra.Command{
		Use:   "ui <component>…",
		Short: "Copy shadcn-vue component source into frontend/src/components/ui/",
		Long: "Fetches components from the shadcn-vue registry and writes their source into\n" +
			"the project, following registryDependencies. Files are yours to edit; nothing\n" +
			"is installed — the pnpm add line for any missing packages is printed.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := scaffold.Detect(root)
			if err != nil {
				return err
			}
			if !p.HasAdmin {
				return fmt.Errorf("this project has no internal/controller/admin/ — the copied components belong to the admin area, so there is nothing here to add them to")
			}
			out := cmd.OutOrStdout()
			return scaffold.UI(args, scaffold.UIOptions{
				ModuleRoot: p.Root,
				Force:      force,
				DryRun:     dryRun,
				Out:        out,
			})
		},
	}

	gen.PersistentFlags().BoolVar(&force, "force", false, "Overwrite existing files")
	gen.PersistentFlags().StringVarP(&root, "dir", "C", ".", "Project root")
	resourceCmd.Flags().BoolVar(&noMount, "no-mount", false,
		"Skip editing "+scaffold.MountGenPath+"; print the Mount line instead")
	uiCmd.Flags().BoolVar(&dryRun, "dry-run", false, "Print what would be written, write nothing")
	gen.AddCommand(resourceCmd, adminCmd, uiCmd)
	return gen
}

// warnNoDB is the honest heads-up for a db-less project: the scaffold compiles
// (its query lines are commented TODOs) but svc.DB is nil, so nothing it
// suggests will work until a database is wired in.
func warnNoDB(out io.Writer, p scaffold.Project) {
	if p.HasDB {
		return
	}
	fmt.Fprintf(out, "warning: %s has no internal/service/db/ — svc.DB is nil at runtime, so the\n"+
		"         generated CRUD handlers cannot query anything until you add a database.\n", p.Root)
}
