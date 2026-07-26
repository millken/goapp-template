package commands

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/millken/goapp-template/internal/controller/admin"
	"github.com/millken/goapp-template/internal/service/db"
	"github.com/spf13/cobra"
)

// tableNameRe mirrors the admin module's validation: the table name is
// interpolated into SQL, so it must be a safe identifier.
var tableNameRe = regexp.MustCompile(`^[A-Za-z_]\w*$`)

// newAdminCmd builds the `goapp admin` command tree (admin utilities that need
// config + DB, so they run through AppInit).
func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Admin utilities (create-user …)",
	}
	cmd.AddCommand(newAdminCreateUserCmd())
	return cmd
}

func newAdminCreateUserCmd() *cobra.Command {
	var password string
	var groupName string

	cmd := &cobra.Command{
		Use:   "create-user <username>",
		Short: "Create an admin user (bcrypt-hashed) in the users table",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			username := args[0]

			if appCfg.DB == nil {
				return fmt.Errorf("no [db] config section; admin users require a database")
			}
			table := "users"
			if appCfg.Admin != nil && appCfg.Admin.UsersTable != "" {
				table = appCfg.Admin.UsersTable
			}
			if !tableNameRe.MatchString(table) {
				return fmt.Errorf("illegal users table name %q", table)
			}

			ctx := cmd.Context()
			dbSvc := db.New(appCfg.DB)
			if err := dbSvc.Start(ctx); err != nil { // runs migrations → users table
				return err
			}
			defer func() { _ = dbSvc.Stop(context.Background()) }()

			// The group is resolved before the password is asked for: a name that
			// does not exist is the likeliest mistake here, and finding out after
			// typing a password is a needless second attempt.
			groupID, err := admin.FindGroupID(ctx, dbSvc.DB(), groupName)
			if err != nil {
				return err
			}

			if password == "" {
				// Read one line from stdin (pipe it for non-interactive use):
				//   echo "s3cret" | myapp admin create-user alice
				fmt.Fprint(os.Stderr, "Password: ")
				line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
				password = strings.TrimRight(line, "\r\n")
			}
			if password == "" {
				return fmt.Errorf("password must not be empty")
			}
			hash, err := admin.HashPassword(password)
			if err != nil {
				return err
			}
			q := fmt.Sprintf(`INSERT INTO %s (username, password_hash, created_at, group_id)
				VALUES (?, ?, ?, ?)`, table)
			if _, err := dbSvc.DB().ExecContext(ctx, q, username, hash, time.Now().UnixNano(), groupID); err != nil {
				return fmt.Errorf("create user %q: %w", username, err)
			}
			fmt.Printf("created admin user %q\n", username)
			return nil
		},
	}
	cmd.Flags().StringVar(&password, "password", "", "Password (if empty, read from stdin)")
	cmd.Flags().StringVar(&groupName, "group", "Administrators",
		"Permission group to place the user in")
	return cmd
}
