// Package cli is the admin's command line. It's for what has to work when nobody can
// sign in, and what no web request should ever be able to do, like moving the admin role.
// Like the admin pages, it shows accounts and counts, never what anyone stores.
package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/admin"
	"github.com/swayam5342/tributary/internal/auth"
	"github.com/swayam5342/tributary/internal/files"
	"github.com/swayam5342/tributary/internal/version"
)

const usage = `usage: tributary-cli <command>

Server
  version                      Which release this is
  status                       How the server, accounts and background jobs are doing
  sync                         Match Tributary to every linked account now

Users
  users                        List every user
  create-user <email>          Add a user and print a temporary password
  delete-user <email> --yes    Delete a user. Leave off --yes to see what that does
  reset-password <email>       Print a temporary password, for someone locked out
  transfer-admin <email>       Hand the admin role to someone else

Settings
  links [on|off]               Show or set whether people can make Google links
  upload-limit [GiB]           Show or set the largest upload
`

const (
	bytesPerGiB = 1 << 30
	// cliActor stands in for a client address in the audit log.
	cliActor = "cli"
)

// errUsage is what a mistyped command gets back.
var errUsage = errors.New(strings.TrimSpace(usage))

// noActor is who the audit log names: whoever is at the server's command line isn't an
// account.
var noActor = uuid.Nil

// Deps is what the commands work with.
type Deps struct {
	Auth  *auth.Service
	Admin *admin.Service
	Files *files.Service
}

// NeedsNoServer says whether arguments only ask what the commands are.
func NeedsNoServer(args []string) bool {
	return len(args) == 0 || (len(args) == 1 && (args[0] == "version" || args[0] == "help" || args[0] == "--help" || args[0] == "-h"))
}

// Run runs one command. An unknown command or the wrong arguments return the usage.
func Run(ctx context.Context, d Deps, args []string, out io.Writer) error {
	if slices.Equal(args, []string{"version"}) {
		_, err := fmt.Fprintln(out, version.Version)
		return err
	}
	if NeedsNoServer(args) {
		_, err := io.WriteString(out, usage)
		return err
	}
	name, rest := args[0], args[1:]
	switch {
	case name == "status" && len(rest) == 0:
		return status(ctx, d, out)
	case name == "sync" && len(rest) == 0:
		return sync(ctx, d, out)
	case name == "users" && len(rest) == 0:
		return users(ctx, d, out)
	case name == "create-user" && len(rest) == 1:
		return createUser(ctx, d, rest[0], out)
	case name == "delete-user" && len(rest) == 1:
		_, err := fmt.Fprintf(out, `This deletes %[1]s, their linked accounts and Tributary's records of their files.
Their files stay in Google Drive, and any Google links they made keep working.
It can't be undone. To go ahead:

  tributary-cli delete-user %[1]s --yes
`, rest[0])
		return err
	case name == "delete-user" && len(rest) == 2 && rest[1] == "--yes":
		return deleteUser(ctx, d, rest[0], out)
	case name == "reset-password" && len(rest) == 1:
		return resetPassword(ctx, d, rest[0], out)
	case name == "transfer-admin" && len(rest) == 1:
		return transferAdmin(ctx, d, rest[0], out)
	case name == "links" && len(rest) <= 1:
		return links(ctx, d, rest, out)
	case name == "upload-limit" && len(rest) <= 1:
		return uploadLimit(ctx, d, rest, out)
	}
	return errUsage
}

// row adds to a table. A table holds everything until it's flushed, and that's where a
// failed write shows up.
func row(table *tabwriter.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(table, format, args...)
}

func gib(bytes int64) string {
	return fmt.Sprintf("%.1f GiB", float64(bytes)/bytesPerGiB)
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func status(ctx context.Context, d Deps, out io.Writer) error {
	everyone, err := d.Auth.ListUsers(ctx)
	if err != nil {
		return err
	}
	report, err := d.Admin.Status(ctx)
	if err != nil {
		return err
	}
	settings, err := d.Admin.Settings(ctx)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	row(table, "Version\t%s\n", version.Version)
	row(table, "Users\t%d\n", len(everyone))
	row(table, "Linked accounts\t%d, %s used\n", report.Health.Accounts, gib(report.Health.UsedBytes))
	row(table, "Google links\t%s\n", onOff(settings.GoogleLinks))
	row(table, "Largest upload\t%s\n", gib(settings.MaxUploadBytes))
	row(table, "\n")
	if len(report.Health.Unreachable) > 0 {
		row(table, "Accounts that can't be reached\n")
		for _, account := range report.Health.Unreachable {
			row(table, "%s\tlinked by %s\n", account.Summary, account.OwnerEmail)
		}
		row(table, "\n")
	}
	row(table, "Background jobs\n")
	for _, job := range report.Jobs {
		state := "failed"
		if job.Succeeded {
			state = "working"
		}
		row(table, "%s\t%s\t%s\n", job.Name, state, job.FinishedAt.Format("2006-01-02 15:04 MST"))
	}
	return table.Flush()
}

func sync(ctx context.Context, d Deps, out io.Writer) error {
	result, err := d.Files.SyncAll(ctx)
	if err != nil {
		return fmt.Errorf("sync: %w", err)
	}
	if _, err := fmt.Fprintf(out, "%d found, %d gone from Drive, %d accounts skipped\n", result.Added, result.Removed, result.Skipped); err != nil {
		return err
	}
	for _, failure := range result.Failures {
		if _, err := fmt.Fprintf(out, "  %v\n", failure); err != nil {
			return err
		}
	}
	return nil
}

func users(ctx context.Context, d Deps, out io.Writer) error {
	everyone, err := d.Auth.ListUsers(ctx)
	if err != nil {
		return err
	}
	table := tabwriter.NewWriter(out, 0, 0, 3, ' ', 0)
	row(table, "EMAIL\tROLE\tACCOUNTS\tNOTE\n")
	for _, user := range everyone {
		note := ""
		if user.MustChangePassword {
			note = "has a temporary password"
		}
		row(table, "%s\t%s\t%d\t%s\n", user.Email, user.Role, user.LinkedAccounts, note)
	}
	return table.Flush()
}

func createUser(ctx context.Context, d Deps, email string, out io.Writer) error {
	password, err := d.Auth.CreateUserFromCLI(ctx, email)
	if err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	_, err = fmt.Fprintf(out, "Temporary password for %s: %s\nIt must be changed at the first sign-in.\n", email, password)
	return err
}

func deleteUser(ctx context.Context, d Deps, email string, out io.Writer) error {
	if err := d.Auth.DeleteUserFromCLI(ctx, email); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	_, err := fmt.Fprintf(out, "Deleted %s\n", email)
	return err
}

func resetPassword(ctx context.Context, d Deps, email string, out io.Writer) error {
	password, err := d.Auth.ResetPasswordFromCLI(ctx, email)
	if err != nil {
		return fmt.Errorf("reset password: %w", err)
	}
	_, err = fmt.Fprintf(out, "Temporary password for %s: %s\nIt must be changed at the next sign-in.\n", email, password)
	return err
}

func transferAdmin(ctx context.Context, d Deps, email string, out io.Writer) error {
	if err := d.Auth.TransferAdmin(ctx, email); err != nil {
		return fmt.Errorf("transfer admin: %w", err)
	}
	_, err := fmt.Fprintf(out, "%s is now the admin\n", email)
	return err
}

func links(ctx context.Context, d Deps, args []string, out io.Writer) error {
	if len(args) == 1 {
		if args[0] != "on" && args[0] != "off" {
			return errUsage
		}
		on := args[0] == "on"
		if _, err := d.Admin.UpdateSettings(ctx, noActor, cliActor, admin.Update{GoogleLinks: &on}); err != nil {
			return fmt.Errorf("save setting: %w", err)
		}
	}
	settings, err := d.Admin.Settings(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "Google links are %s\n", onOff(settings.GoogleLinks))
	return err
}

func uploadLimit(ctx context.Context, d Deps, args []string, out io.Writer) error {
	if len(args) == 1 {
		size, err := strconv.ParseFloat(args[0], 64)
		if err != nil {
			return errUsage
		}
		limit := int64(size * bytesPerGiB)
		if _, err := d.Admin.UpdateSettings(ctx, noActor, cliActor, admin.Update{MaxUploadBytes: &limit}); err != nil {
			return fmt.Errorf("save setting: %w", err)
		}
	}
	settings, err := d.Admin.Settings(ctx)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out, "The largest upload is %s\n", gib(settings.MaxUploadBytes))
	return err
}
