package gdrive

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/swayam5342/dextro/internal/storage/storagetest"
)

const testVerifier = "the-verifier-the-verifier-the-verifier-the-verifier"

func newTestClient(f *fakeDrive) *Client {
	return newClient("client-id", "client-secret", "https://files.example.org/callback",
		f.server.URL+"/auth", f.server.URL+"/token", f.server.URL+"/drive/v3/")
}

func connect(t *testing.T, c *Client) (Settings, Credentials) {
	t.Helper()
	settings, credentials, err := c.Connect(context.Background(), "the-code", testVerifier)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return settings, credentials
}

func TestConformance(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p.pageSize = storagetest.ListPageSize
	storagetest.Run(t, p)
	if f.permissionCalls != 0 {
		t.Errorf("permission calls = %d, want none", f.permissionCalls)
	}
}

func TestAuthCodeURL(t *testing.T) {
	f := newFakeDrive(t)
	raw := newTestClient(f).AuthCodeURL("the-state", testVerifier)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := parsed.Query()
	want := map[string]string{
		"state":                 "the-state",
		"client_id":             "client-id",
		"redirect_uri":          "https://files.example.org/callback",
		"access_type":           "offline",
		"prompt":                "consent",
		"code_challenge_method": "S256",
		// Exactly this: dextro must not be able to read the rest of someone's Drive.
		"scope": "https://www.googleapis.com/auth/drive.file",
	}
	for key, value := range want {
		if got := q.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if q.Get("code_challenge") == "" || strings.Contains(raw, testVerifier) {
		t.Errorf("url must carry the PKCE challenge, not the verifier: %s", raw)
	}
}

func TestConnect(t *testing.T) {
	f := newFakeDrive(t)
	settings, credentials := connect(t, newTestClient(f))
	if settings.Email != "person@example.org" || settings.FolderID == "" {
		t.Errorf("settings = %+v", settings)
	}
	if credentials.RefreshToken != "the-refresh-token" {
		t.Errorf("credentials = %+v", credentials)
	}
	if f.gotVerifier != testVerifier {
		t.Errorf("code_verifier = %q", f.gotVerifier)
	}
}

func TestReconnectingReusesTheFolder(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	first, _ := connect(t, c)
	second, _ := connect(t, c)
	if first.FolderID != second.FolderID {
		t.Errorf("folder changed: %s then %s", first.FolderID, second.FolderID)
	}
	if got := f.countFolders(); got != 1 {
		t.Errorf("folders = %d, want 1", got)
	}
}

func TestConnectFailures(t *testing.T) {
	t.Run("a bad code", func(t *testing.T) {
		f := newFakeDrive(t)
		if _, _, err := newTestClient(f).Connect(context.Background(), "wrong", testVerifier); err == nil {
			t.Fatal("Connect succeeded, want error")
		}
	})
	t.Run("no refresh token", func(t *testing.T) {
		f := newFakeDrive(t)
		f.omitRefresh = true
		if _, _, err := newTestClient(f).Connect(context.Background(), "the-code", testVerifier); err == nil {
			t.Fatal("Connect succeeded, want error: the connection would stop working within the hour")
		}
	})
}

func TestCheck(t *testing.T) {
	setup := func(t *testing.T) (*fakeDrive, *Provider, Settings) {
		t.Helper()
		f := newFakeDrive(t)
		c := newTestClient(f)
		settings, credentials := connect(t, c)
		p, err := c.Open(context.Background(), settings, credentials)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return f, p, settings
	}

	t.Run("a revoked token fails", func(t *testing.T) {
		f, p, _ := setup(t)
		f.refreshRevoked = true
		if err := p.Check(context.Background()); err == nil {
			t.Fatal("Check succeeded, want error")
		}
	})

	t.Run("a trashed folder fails", func(t *testing.T) {
		f, p, settings := setup(t)
		f.files[settings.FolderID].Trashed = true
		if err := p.Check(context.Background()); !errors.Is(err, ErrFolderMissing) {
			t.Fatalf("error = %v, want ErrFolderMissing", err)
		}
	})

	t.Run("a deleted folder fails", func(t *testing.T) {
		f, p, settings := setup(t)
		delete(f.files, settings.FolderID)
		if err := p.Check(context.Background()); !errors.Is(err, ErrFolderMissing) {
			t.Fatalf("error = %v, want ErrFolderMissing", err)
		}
	})
}
