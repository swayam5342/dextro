package auth

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRunCommandRejectsAnythingElse(t *testing.T) {
	for _, args := range [][]string{
		{"bogus", "a@example.org"},
		{"set-role", "a@example.org"},
		{"transfer-admin"},
		{"reset-password"},
		{"transfer-admin", "a@example.org", "extra"},
	} {
		// A nil service proves the arguments are refused before anything runs.
		err := RunCommand(context.Background(), nil, args, &bytes.Buffer{})
		if err == nil || !strings.Contains(err.Error(), "usage:") {
			t.Errorf("RunCommand(%v) error = %v, want usage", args, err)
		}
	}
}

func testRunCommandTransfersAdmin(t *testing.T, f *fixture) {
	f.register(t, "first@example.org")
	f.register(t, "person@example.org")

	var out bytes.Buffer
	if err := RunCommand(f.ctx, f.service, []string{"transfer-admin", "person@example.org"}, &out); err != nil {
		t.Fatalf("RunCommand: %v", err)
	}
	if !strings.Contains(out.String(), "person@example.org") {
		t.Errorf("output = %q", out.String())
	}
	if got := f.count(t, `SELECT count(*) FROM users WHERE role = 'admin' AND email = 'person@example.org'`); got != 1 {
		t.Error("the role didn't move")
	}
}
