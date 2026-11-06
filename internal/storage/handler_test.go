package storage

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
)

const testOrigin = "https://files.example.org"

// sessions signs requests in by treating the cookie as the user's ID.
type sessions struct{}

func (sessions) ResolveSession(_ context.Context, token string) (httpx.Principal, error) {
	id, err := uuid.Parse(token)
	if err != nil {
		return httpx.Principal{}, httpx.ErrNoSession
	}
	return httpx.Principal{UserID: id, SessionID: id}, nil
}

type server struct {
	*fixture
	handler http.Handler
}

// browser keeps cookies between requests, as net/http's jar won't for Secure ones.
type browser struct {
	s       *server
	t       *testing.T
	cookies map[string]string
	userID  uuid.UUID
}

func (s *server) signedIn(t *testing.T) *browser {
	t.Helper()
	id := s.user(t)
	return &browser{s: s, t: t, userID: id, cookies: map[string]string{httpx.SessionCookieName: id.String()}}
}

func (b *browser) do(method, target string) *httptest.ResponseRecorder {
	b.t.Helper()
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Origin", testOrigin)
	for name, value := range b.cookies {
		req.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	rec := httptest.NewRecorder()
	b.s.handler.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(b.cookies, c.Name)
		} else {
			b.cookies[c.Name] = c.Value
		}
	}
	return rec
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(rec.Body.Bytes(), &value); err != nil {
		t.Fatalf("decode %q: %v", rec.Body, err)
	}
	return value
}

func wantError(t *testing.T, rec *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	body := decode[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, rec)
	if body.Error.Code != code {
		t.Errorf("code = %q, want %q", body.Error.Code, code)
	}
}

func wantRedirect(t *testing.T, rec *httptest.ResponseRecorder, location string) {
	t.Helper()
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want 302; body %s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Location"); got != location {
		t.Errorf("Location = %q, want %q", got, location)
	}
}

// startDrive begins connecting Drive and returns the state handed to Google.
func (b *browser) startDrive() string {
	b.t.Helper()
	rec := b.do(http.MethodPost, "/api/v1/storage/google/connect")
	if rec.Code != http.StatusOK {
		b.t.Fatalf("connect: status = %d, body %s", rec.Code, rec.Body)
	}
	parsed, err := url.Parse(decode[apigen.GoogleRedirect](b.t, rec).Url)
	if err != nil {
		b.t.Fatalf("parse url: %v", err)
	}
	return parsed.Query().Get("state")
}

func (b *browser) callback(state string) *httptest.ResponseRecorder {
	return b.do(http.MethodGet, "/api/v1/storage/google/callback?code=the-code&state="+url.QueryEscape(state))
}

// TestHandlers shares one Postgres between its subtests, emptying it each time.
func TestHandlers(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	tests := []struct {
		name string
		run  func(*testing.T, *server)
	}{
		{"listing connections", testHandlerList},
		{"the pool view", testHandlerPool},
		{"responses never carry credentials", testResponsesHideCredentials},
		{"testing a connection", testHandlerTest},
		{"failure details stay out of responses", testFailureDetailsAreHidden},
		{"users can't reach each other's connections", testHandlerIsolation},
		{"removing a connection", testHandlerDelete},
		{"connection checks are rate limited", testChecksAreRateLimited},
		{"google drive is off without credentials", testDriveDisabled},
		{"connecting google drive", testHandlerDrive},
		{"the flow cookie is safe", testDriveFlowCookie},
		{"a callback without a flow is refused", testDriveCallbackWithoutFlow},
		{"a callback with the wrong state is refused", testDriveCallbackWrongState},
		{"a flow works only once", testDriveCallbackReplay},
		{"cancelling at google", testDriveCancelled},
		{"google failures are hidden", testDriveFailureHidden},
		{"the callback needs a session", testDriveCallbackNeedsSession},
		{"linking an account syncs it", testLinkingSyncsTheAccount},
		{"the picker is off without a key", testPickerDisabled},
		{"opening the picker", testOpenPicker},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			f := newFixture(t, pool)
			tt.run(t, &server{fixture: f, handler: httpx.NewRouter(httpx.Deps{
				Logger:       logger,
				DB:           f.pool,
				PublicOrigin: testOrigin,
				Sessions:     sessions{},
				Storage: NewHandler(f.service, logger, func(ctx context.Context, userID, id uuid.UUID, ip string) error {
					if f.afterConnect == nil {
						return nil
					}
					return f.afterConnect(ctx, userID, id, ip)
				}),
			})})
		})
	}
}

func testHandlerList(t *testing.T, s *server) {
	b := s.signedIn(t)
	created := s.connect(t, b.userID, "person@example.org")
	list := decode[[]apigen.Connection](t, b.do(http.MethodGet, "/api/v1/storage/connections"))
	if len(list) != 1 || list[0].Id != created.ID || list[0].Provider != apigen.GoogleDrive || list[0].Summary != "person@example.org" {
		t.Errorf("list = %+v", list)
	}
}

func testResponsesHideCredentials(t *testing.T, s *server) {
	b := s.signedIn(t)
	s.connect(t, b.userID, "person@example.org")
	body := b.do(http.MethodGet, "/api/v1/storage/connections").Body.String()
	for _, secret := range []string{refreshToken, "ciphertext", "folder-1"} {
		if strings.Contains(body, secret) {
			t.Errorf("response contains %q: %s", secret, body)
		}
	}
}

func testHandlerTest(t *testing.T, s *server) {
	b := s.signedIn(t)
	id := s.connect(t, b.userID, "person@example.org").ID.String()
	if rec := b.do(http.MethodPost, "/api/v1/storage/connections/"+id+"/test"); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	s.account("person@example.org").CheckErr = errors.New("expired")
	wantError(t, b.do(http.MethodPost, "/api/v1/storage/connections/"+id+"/test"), http.StatusUnprocessableEntity, "connection_failed")
}

func testFailureDetailsAreHidden(t *testing.T, s *server) {
	b := s.signedIn(t)
	id := s.connect(t, b.userID, "person@example.org").ID.String()
	s.account("person@example.org").CheckErr = errors.New("token endpoint said secret-detail")
	rec := b.do(http.MethodPost, "/api/v1/storage/connections/"+id+"/test")
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
}

func testHandlerIsolation(t *testing.T, s *server) {
	owner, other := s.signedIn(t), s.signedIn(t)
	id := s.connect(t, owner.userID, "person@example.org").ID.String()

	wantError(t, other.do(http.MethodPost, "/api/v1/storage/connections/"+id+"/test"), http.StatusNotFound, "not_found")
	wantError(t, other.do(http.MethodDelete, "/api/v1/storage/connections/"+id), http.StatusNotFound, "not_found")
	if list := decode[[]apigen.Connection](t, other.do(http.MethodGet, "/api/v1/storage/connections")); len(list) != 0 {
		t.Errorf("list = %+v", list)
	}
	if list := decode[[]apigen.Connection](t, owner.do(http.MethodGet, "/api/v1/storage/connections")); len(list) != 1 {
		t.Errorf("the owner's connection changed: %+v", list)
	}
}

func testHandlerDelete(t *testing.T, s *server) {
	b := s.signedIn(t)
	id := s.connect(t, b.userID, "person@example.org").ID.String()
	if rec := b.do(http.MethodDelete, "/api/v1/storage/connections/"+id); rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d", rec.Code)
	}
	wantError(t, b.do(http.MethodDelete, "/api/v1/storage/connections/"+id), http.StatusNotFound, "not_found")
}

func testChecksAreRateLimited(t *testing.T, s *server) {
	b := s.signedIn(t)
	id := s.connect(t, b.userID, "person@example.org").ID.String()
	var last *httptest.ResponseRecorder
	for range checkBurst + 1 {
		last = b.do(http.MethodPost, "/api/v1/storage/connections/"+id+"/test")
	}
	wantError(t, last, http.StatusTooManyRequests, "rate_limited")
	if last.Header().Get("Retry-After") == "" {
		t.Error("Retry-After is missing")
	}
}

func testDriveDisabled(t *testing.T, s *server) {
	s.service.driveAuth = nil
	b := s.signedIn(t)
	wantError(t, b.do(http.MethodPost, "/api/v1/storage/google/connect"), http.StatusNotFound, "not_found")
	wantRedirect(t, b.callback("x"), "/storage?error=google_failed")
}

func testHandlerDrive(t *testing.T, s *server) {
	b := s.signedIn(t)
	wantRedirect(t, b.callback(b.startDrive()), "/storage?connected=google")
	if _, ok := b.cookies[flowCookieName]; ok {
		t.Error("the flow cookie was not cleared")
	}
	list := decode[[]apigen.Connection](t, b.do(http.MethodGet, "/api/v1/storage/connections"))
	if len(list) != 1 || list[0].Provider != apigen.GoogleDrive || list[0].Summary != "person@example.org" {
		t.Errorf("list = %+v", list)
	}
}

func testDriveFlowCookie(t *testing.T, s *server) {
	rec := s.signedIn(t).do(http.MethodPost, "/api/v1/storage/google/connect")
	for _, c := range rec.Result().Cookies() {
		if c.Name != flowCookieName {
			continue
		}
		// Lax is what lets the cookie arrive on the redirect back from Google.
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
			t.Errorf("cookie = %+v", c)
		}
		if c.MaxAge <= 0 || c.MaxAge > 600 {
			t.Errorf("MaxAge = %d", c.MaxAge)
		}
		return
	}
	t.Fatal("no flow cookie")
}

func testDriveCallbackWithoutFlow(t *testing.T, s *server) {
	b := s.signedIn(t)
	wantRedirect(t, b.callback("made-up"), "/storage?error=google_failed")
	if got := s.count(t, `SELECT count(*) FROM storage_connections`); got != 0 {
		t.Errorf("connections = %d, want 0", got)
	}
}

// A callback URL prepared by an attacker must not attach the attacker's Drive to
// the victim's account, where the victim's uploads would land in it.
func testDriveCallbackWrongState(t *testing.T, s *server) {
	victim, attacker := s.signedIn(t), s.signedIn(t)
	victim.startDrive()
	attackerState := attacker.startDrive()

	wantRedirect(t, victim.callback(attackerState), "/storage?error=google_failed")
	if got := s.count(t, `SELECT count(*) FROM storage_connections`); got != 0 {
		t.Errorf("connections = %d, want 0", got)
	}
}

func testDriveCallbackReplay(t *testing.T, s *server) {
	b := s.signedIn(t)
	state := b.startDrive()
	wantRedirect(t, b.callback(state), "/storage?connected=google")
	wantRedirect(t, b.callback(state), "/storage?error=google_failed")
}

func testDriveCancelled(t *testing.T, s *server) {
	b := s.signedIn(t)
	state := b.startDrive()
	rec := b.do(http.MethodGet, "/api/v1/storage/google/callback?error=access_denied&state="+url.QueryEscape(state))
	wantRedirect(t, rec, "/storage?error=google_cancelled")
}

func testDriveFailureHidden(t *testing.T, s *server) {
	s.drive.err = errors.New("token endpoint said secret-detail")
	b := s.signedIn(t)
	rec := b.callback(b.startDrive())
	wantRedirect(t, rec, "/storage?error=google_failed")
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
}

func testDriveCallbackNeedsSession(t *testing.T, s *server) {
	b := s.signedIn(t)
	state := b.startDrive()
	delete(b.cookies, httpx.SessionCookieName)
	wantError(t, b.callback(state), http.StatusUnauthorized, "unauthorized")
	if got := s.count(t, `SELECT count(*) FROM storage_connections`); got != 0 {
		t.Errorf("connections = %d, want 0", got)
	}
}

func testHandlerPool(t *testing.T, s *server) {
	b, other := s.signedIn(t), s.signedIn(t)
	s.linked(t, b.userID, "one@example.org", 15*gib, 5*gib)
	s.linked(t, b.userID, "broken@example.org", 15*gib, 1*gib)
	s.linked(t, other.userID, "theirs@example.org", 15*gib, 1*gib)
	s.account("broken@example.org").QuotaErr = errors.New("token for broken@example.org said secret-detail")

	rec := b.do(http.MethodGet, "/api/v1/storage/pool")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
	pool := decode[apigen.Pool](t, rec)
	if len(pool.Accounts) != 2 || pool.UsedBytes != 5*gib {
		t.Fatalf("pool = %+v", pool)
	}
	if pool.LimitBytes == nil || *pool.LimitBytes != 15*gib || pool.FreeBytes == nil || *pool.FreeBytes != 10*gib {
		t.Errorf("limit = %v, free = %v", pool.LimitBytes, pool.FreeBytes)
	}
	for _, account := range pool.Accounts {
		broken := account.Connection.Summary == "broken@example.org"
		if account.Available == broken {
			t.Errorf("account = %+v", account)
		}
		if broken && account.LimitBytes != nil {
			t.Errorf("a broken account reports a limit: %+v", account)
		}
	}

	s.account("unlimited@example.org").ExtraUsed = gib
	s.connect(t, b.userID, "unlimited@example.org")
	pool = decode[apigen.Pool](t, b.do(http.MethodGet, "/api/v1/storage/pool"))
	if pool.LimitBytes != nil || pool.FreeBytes != nil {
		t.Errorf("limit = %v, free = %v, want none with an unlimited account", pool.LimitBytes, pool.FreeBytes)
	}
}

// A linked account's files show up straight away, and a sync that fails doesn't
// undo the link.
func testLinkingSyncsTheAccount(t *testing.T, s *server) {
	var calls int
	s.afterConnect = func(_ context.Context, _, _ uuid.UUID, _ string) error {
		calls++
		return errors.New("drive is having a moment")
	}
	b := s.signedIn(t)
	rec := b.callback(b.startDrive())
	if got := rec.Header().Get("Location"); got != "/storage?connected=google" {
		t.Errorf("redirect = %q, a failed sync must not undo the link", got)
	}
	if calls != 1 {
		t.Errorf("syncs = %d, want 1", calls)
	}
}

func testPickerDisabled(t *testing.T, s *server) {
	b := s.signedIn(t)
	connection := s.connect(t, b.userID, "person@example.org")
	wantError(t, b.do(http.MethodPost, "/api/v1/storage/connections/"+connection.ID.String()+"/picker"),
		http.StatusNotFound, "picker_disabled")
	if got := decode[apigen.Pool](t, b.do(http.MethodGet, "/api/v1/storage/pool")); got.PickerEnabled {
		t.Errorf("pool = %+v", got)
	}
}

func testOpenPicker(t *testing.T, s *server) {
	s.service.picker = PickerConfig{APIKey: "the-api-key", AppID: "1234"}
	b := s.signedIn(t)
	connection := s.connect(t, b.userID, "person@example.org")
	target := "/api/v1/storage/connections/" + connection.ID.String() + "/picker"

	wantError(t, s.signedIn(t).do(http.MethodPost, target), http.StatusNotFound, "not_found")

	rec := b.do(http.MethodPost, target)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	got := decode[apigen.PickerAccess](t, rec)
	if got.AccessToken == "" || got.DeveloperKey != "the-api-key" || got.AppId != "1234" {
		t.Errorf("access = %+v", got)
	}
	if body := rec.Body.String(); strings.Contains(body, refreshToken) {
		t.Errorf("the refresh token left the server: %s", body)
	}
	if cache := rec.Header().Get("Cache-Control"); cache != "no-store" {
		t.Errorf("Cache-Control = %q, a token must not be cached", cache)
	}
	if n := s.count(t, `SELECT count(*) FROM audit_events WHERE action = 'storage.picker_opened' AND actor_id = $1`, b.userID); n != 1 {
		t.Errorf("audit events = %d, want 1", n)
	}
	if n := s.count(t, `SELECT count(*) FROM audit_events WHERE metadata::text LIKE '%' || $1 || '%'`, got.AccessToken); n != 0 {
		t.Error("the access token reached the audit log")
	}
	if pool := decode[apigen.Pool](t, b.do(http.MethodGet, "/api/v1/storage/pool")); !pool.PickerEnabled {
		t.Errorf("pool = %+v", pool)
	}
}
