package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
)

const usage = "usage: dextro transfer-admin <email>\n       dextro reset-password <email>"

// RunCommand runs an administrative command given on the command line. The admin
// role moves here rather than in the API so that no web request can ever do it,
// and a password reset lives here too because it has to work when nobody can
// sign in.
func RunCommand(ctx context.Context, s *Service, args []string, out io.Writer) error {
	if len(args) != 2 {
		return errors.New(usage)
	}
	command, email := args[0], args[1]
	switch command {
	case "transfer-admin":
		if err := s.TransferAdmin(ctx, email); err != nil {
			return fmt.Errorf("transfer admin: %w", err)
		}
		_, err := fmt.Fprintf(out, "%s is now the admin\n", email)
		return err
	case "reset-password":
		password, err := s.ResetPasswordFromCLI(ctx, email)
		if err != nil {
			return fmt.Errorf("reset password: %w", err)
		}
		_, err = fmt.Fprintf(out, "Temporary password for %s: %s\nIt must be changed at the next sign-in.\n", email, password)
		return err
	}
	return errors.New(usage)
}
