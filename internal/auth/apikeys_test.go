package auth

import (
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

var bothScopes = []string{httpx.ScopeFilesRead, httpx.ScopeFilesWrite}

// TestAPIKeys shares one Postgres between its subtests, emptying it each time.
func TestAPIKeys(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *fixture)
	}{
		{"a new key works", testKeyWorks},
		{"keys are stored hashed", testKeysAreHashed},
		{"listing never shows the secret", testListHidesSecret},
		{"users see only their own keys", testKeyListIsScoped},
		{"users can't revoke each other's keys", testRevokeIsScoped},
		{"a revoked key stops working", testRevokedKeyStops},
		{"an admin's key is not an admin", testAdminKeyIsNotAdmin},
		{"bad input is refused", testKeyValidation},
		{"last use is recorded", testKeyLastUsed},
		{"creating and revoking are audited", testKeyAudit},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, &fixture{ctx: ctx, pool: pool, service: NewService(NewStore(pool))})
		})
	}

	t.Run("handlers", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
			t.Fatalf("reset database: %v", err)
		}
		testKeyHandlers(t, pool)
	})
}

func (f *fixture) key(t *testing.T, userID uuid.UUID, scopes ...string) (APIKey, string) {
	t.Helper()
	created, token, err := f.service.CreateAPIKey(f.ctx, userID, "Backup script", scopes, client)
	if err != nil {
		t.Fatalf("CreateAPIKey: %v", err)
	}
	return created, token
}

func testKeyWorks(t *testing.T, f *fixture) {
	user := f.register(t, "person@example.org").User
	created, token := f.key(t, user.ID, httpx.ScopeFilesRead)
	if !strings.HasPrefix(token, "dxt_") {
		t.Errorf("token = %q", token[:8])
	}
	if !strings.HasPrefix(token, created.Prefix) || len(created.Prefix) >= len(token)/2 {
		t.Errorf("prefix = %q", created.Prefix)
	}
	principal, err := f.service.ResolveAPIKey(f.ctx, token)
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if principal.UserID != user.ID || principal.Key == nil || principal.Key.ID != created.ID {
		t.Fatalf("principal = %+v", principal)
	}
	if len(principal.Key.Scopes) != 1 || principal.Key.Scopes[0] != httpx.ScopeFilesRead {
		t.Errorf("scopes = %v", principal.Key.Scopes)
	}
	if _, err := f.service.ResolveAPIKey(f.ctx, "dxt_made-up"); !errors.Is(err, httpx.ErrNoSession) {
		t.Errorf("unknown key: error = %v, want ErrNoSession", err)
	}
}

func testKeysAreHashed(t *testing.T, f *fixture) {
	user := f.register(t, "person@example.org").User
	_, token := f.key(t, user.ID, bothScopes...)
	sum := sha256.Sum256([]byte(token))
	if got := f.count(t, `SELECT count(*) FROM api_keys WHERE token_hash = $1`, sum[:]); got != 1 {
		t.Errorf("keys with the hashed token = %d, want 1", got)
	}
	if got := f.count(t, `SELECT count(*) FROM api_keys WHERE token_hash = $1`, []byte(token)); got != 0 {
		t.Error("the raw token is stored")
	}
}

func testListHidesSecret(t *testing.T, f *fixture) {
	user := f.register(t, "person@example.org").User
	f.key(t, user.ID, bothScopes...)
	keys, err := f.service.ListAPIKeys(f.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 1 || keys[0].Name != "Backup script" || len(keys[0].Scopes) != 2 {
		t.Errorf("keys = %+v", keys)
	}
}

func testKeyListIsScoped(t *testing.T, f *fixture) {
	mine := f.register(t, "mine@example.org").User
	theirs := f.register(t, "theirs@example.org").User
	f.key(t, theirs.ID, bothScopes...)
	keys, err := f.service.ListAPIKeys(f.ctx, mine.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("keys = %+v", keys)
	}
}

func testRevokeIsScoped(t *testing.T, f *fixture) {
	mine := f.register(t, "mine@example.org").User
	theirs := f.register(t, "theirs@example.org").User
	created, token := f.key(t, theirs.ID, bothScopes...)
	if err := f.service.RevokeAPIKey(f.ctx, mine.ID, created.ID, client); !errors.Is(err, ErrAPIKeyNotFound) {
		t.Fatalf("error = %v, want ErrAPIKeyNotFound", err)
	}
	if _, err := f.service.ResolveAPIKey(f.ctx, token); err != nil {
		t.Errorf("their key stopped working: %v", err)
	}
}

func testRevokedKeyStops(t *testing.T, f *fixture) {
	user := f.register(t, "person@example.org").User
	created, token := f.key(t, user.ID, bothScopes...)
	if err := f.service.RevokeAPIKey(f.ctx, user.ID, created.ID, client); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if _, err := f.service.ResolveAPIKey(f.ctx, token); !errors.Is(err, httpx.ErrNoSession) {
		t.Errorf("error = %v, want ErrNoSession", err)
	}
}

func testAdminKeyIsNotAdmin(t *testing.T, f *fixture) {
	admin := f.register(t, "admin@example.org").User
	_, token := f.key(t, admin.ID, bothScopes...)
	principal, err := f.service.ResolveAPIKey(f.ctx, token)
	if err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	if principal.IsAdmin {
		t.Error("the key carries the admin role")
	}
}

func testKeyValidation(t *testing.T, f *fixture) {
	user := f.register(t, "person@example.org").User
	tests := []struct {
		name   string
		key    string
		scopes []string
	}{
		{"no name", "  ", bothScopes},
		{"a name too long", strings.Repeat("x", 101), bothScopes},
		{"a control character", "a\nb", bothScopes},
		{"no scopes", "Script", nil},
		{"an unknown scope", "Script", []string{"admin"}},
		{"one bad scope among good ones", "Script", []string{httpx.ScopeFilesRead, "files:delete"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := f.service.CreateAPIKey(f.ctx, user.ID, tt.key, tt.scopes, client); !errors.Is(err, ErrInvalidAPIKey) {
				t.Errorf("error = %v, want ErrInvalidAPIKey", err)
			}
		})
	}
}

func testKeyLastUsed(t *testing.T, f *fixture) {
	user := f.register(t, "person@example.org").User
	created, token := f.key(t, user.ID, bothScopes...)
	if created.LastUsedAt != nil {
		t.Fatalf("LastUsedAt = %v before first use", created.LastUsedAt)
	}
	if _, err := f.service.ResolveAPIKey(f.ctx, token); err != nil {
		t.Fatalf("ResolveAPIKey: %v", err)
	}
	keys, err := f.service.ListAPIKeys(f.ctx, user.ID)
	if err != nil {
		t.Fatalf("ListAPIKeys: %v", err)
	}
	if keys[0].LastUsedAt == nil {
		t.Error("LastUsedAt is still empty")
	}
}

func testKeyAudit(t *testing.T, f *fixture) {
	user := f.register(t, "person@example.org").User
	created, token := f.key(t, user.ID, bothScopes...)
	if err := f.service.RevokeAPIKey(f.ctx, user.ID, created.ID, client); err != nil {
		t.Fatalf("RevokeAPIKey: %v", err)
	}
	if got := f.auditCount(t, "api_key.created"); got != 1 {
		t.Errorf("created events = %d, want 1", got)
	}
	if got := f.auditCount(t, "api_key.revoked"); got != 1 {
		t.Errorf("revoked events = %d, want 1", got)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE metadata::text LIKE '%' || $1 || '%'`, token); got != 0 {
		t.Error("an audit event contains the token")
	}
}

func testKeyHandlers(t *testing.T, pool *pgxpool.Pool) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(NewStore(pool))
	s := &server{t: t, pool: pool}
	s.handler = httpx.NewRouter(httpx.Deps{
		Logger:       logger,
		DB:           pool,
		PublicOrigin: testOrigin,
		Sessions:     service,
		APIKeys:      service,
		Auth:         NewHandler(service, nil, logger),
		Admin:        admin.NewHandler(admin.NewService(admin.NewStore(pool)), logger),
	})

	owner := s.signUp("owner@example.org")
	other := s.signUp("someone-else@example.org")

	rec := owner.do(http.MethodPost, "/api/v1/auth/api-keys",
		map[string]any{"name": "Backup script", "scopes": []string{"files:read"}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create: status = %d, body %s", rec.Code, rec.Body)
	}
	created := decode[apigen.APIKeyCreated](t, rec)
	if !strings.HasPrefix(created.Token, "dxt_") {
		t.Fatalf("token = %q", created.Token)
	}

	list := owner.do(http.MethodGet, "/api/v1/auth/api-keys", nil)
	if strings.Contains(list.Body.String(), created.Token) {
		t.Errorf("the list contains the secret: %s", list.Body)
	}
	if keys := decode[[]apigen.APIKey](t, list); len(keys) != 1 {
		t.Errorf("keys = %+v", keys)
	}

	wantError(t, owner.do(http.MethodPost, "/api/v1/auth/api-keys",
		map[string]any{"name": "x", "scopes": []string{"everything"}}), http.StatusBadRequest, "invalid_api_key")

	id := created.Key.Id.String()
	wantError(t, other.do(http.MethodDelete, "/api/v1/auth/api-keys/"+id, nil), http.StatusNotFound, "not_found")

	// A key must not be able to create more keys or revoke others.
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/api-keys", strings.NewReader(`{"name":"x","scopes":["files:read"]}`))
	req.Header.Set("Authorization", "Bearer "+created.Token)
	req.Header.Set("Origin", testOrigin)
	keyRec := httptest.NewRecorder()
	s.handler.ServeHTTP(keyRec, req)
	wantError(t, keyRec, http.StatusForbidden, "forbidden")

	if rec := owner.do(http.MethodDelete, "/api/v1/auth/api-keys/"+id, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("revoke: status = %d", rec.Code)
	}
}
