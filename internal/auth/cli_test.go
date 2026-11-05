package auth

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunCommandRejectsAnythingButSetRole(t *testing.T) {
	for _, args := range [][]string{
		{"bogus"},
		{"set-role"},
		{"set-role", "a@example.org"},
		{"set-role", "a@example.org", "admin", "extra"},
	} {
		// A nil service proves the arguments are refused before anything runs.
		err := RunCommand(context.Background(), nil, args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("RunCommand(%v) error = %v, want usage", args, err)
		}
	}
}

func testRunCommandSetsRole(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	f.register(t, "person@example.org")

	var out bytes.Buffer
	if err := RunCommand(f.ctx, f.service, []string{"set-role", "person@example.org", "admin"}, &out); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(out.String(), "person@example.org") {
		t.Errorf("output = %q", out.String())
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin'`); got != 2 {
		t.Errorf("admins = %d, want 2", got)
	}
}
