package commands

import (
	"fmt"

	"github.com/millken/goapp-template/internal/scaffold"
	"github.com/spf13/cobra"
)

// newGenCmd builds the `goapp gen` command tree. gen is a dev-time scaffolder,
// so its PersistentPreRunE skips AppInit (the generators only write files).
func newGenCmd() *cobra.Command {
	var force bool

	gen := &cobra.Command{
		Use:               "gen",
		Short:             "Scaffold resources (resource, admin)",
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	}

	resourceCmd := &cobra.Command{
		Use:     "resource <name>",
		Aliases: []string{"mvc"},
		Short:   "Generate a public CRUD resource (handler + model + Vue pages)",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := scaffold.Resource(args[0], scaffold.Options{Force: force}); err != nil {
				return err
			}
			fmt.Printf("generated resource: %s\n", args[0])
			return nil
		},
	}

	adminCmd := &cobra.Command{
		Use:   "admin <name>",
		Short: "Generate an admin CRUD resource (auth-guarded, under the admin mount)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := scaffold.Admin(args[0], scaffold.Options{Force: force}); err != nil {
				return err
			}
			fmt.Printf("generated admin resource: %s\n", args[0])
			return nil
		},
	}

	gen.PersistentFlags().BoolVar(&force, "force", false, "Overwrite existing files")
	gen.AddCommand(resourceCmd, adminCmd)
	return gen
}
