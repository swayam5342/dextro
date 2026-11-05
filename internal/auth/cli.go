package auth

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// RunCommand runs an administrative command given on the command line. Roles are
// changed here rather than in the API so that no web request can ever do it.
func RunCommand(ctx context.Context, s *Service, args []string, out io.Writer) error {
	if len(args) != 3 || args[0] != "set-role" {
		return errors.New("usage: dextro set-role <email> <user|admin>")
	}
	email, role := args[1], args[2]
	if err := s.SetRole(ctx, email, role); err != nil {
		return fmt.Errorf("set role: %w", err)
	}
	_, err := fmt.Fprintf(out, "%s is now %s\n", email, role)
	return err
}
