package auth

import (
	"bytes"
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
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
)

const testOrigin = "https://files.example.org"

// fakeGoogle stands in for Google. google_test.go covers the real client.
type fakeGoogle struct {
	identity    GoogleIdentity
	err         error
	gotCode     string
	gotNonce    string
	gotVerifier string
}

func (f *fakeGoogle) AuthCodeURL(_ context.Context, state, nonce, verifier string) (string, error) {
	f.gotNonce, f.gotVerifier = nonce, verifier
	return "https://accounts.example/auth?state=" + url.QueryEscape(state), nil
}

func (f *fakeGoogle) Identify(_ context.Context, code, nonce, verifier string) (GoogleIdentity, error) {
	f.gotCode = code
	if nonce != f.gotNonce || verifier != f.gotVerifier {
		return GoogleIdentity{}, errors.New("nonce or verifier changed between start and callback")
	}
	return f.identity, f.err
}

type server struct {
	t       *testing.T
	pool    *pgxpool.Pool
	handler http.Handler
	google  *fakeGoogle
}

// browser keeps cookies between requests. It behaves like a browser on the right
// origin, which net/http's cookie jar can't because the cookies are Secure.
type browser struct {
	s       *server
	cookies map[string]string
	ip      string
}

func (s *server) browser() *browser {
	return &browser{s: s, cookies: map[string]string{}, ip: "192.0.2.1"}
}

func (b *browser) do(method, target string, body any) *httptest.ResponseRecorder {
	b.s.t.Helper()
	var reader io.Reader
	if raw, ok := body.(string); ok {
		reader = strings.NewReader(raw)
	} else if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			b.s.t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req := httptest.NewRequest(method, target, reader)
	req.RemoteAddr = b.ip + ":4000"
	req.Header.Set("Origin", testOrigin)
	req.Header.Set("User-Agent", "test-browser")
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

func credentials(email string) map[string]string {
	return map[string]string{"email": email, "password": goodPassword}
}

// signUp returns a browser signed in to a new account.
func (s *server) signUp(email string) *browser {
	s.t.Helper()
	b := s.browser()
	// Each account gets its own address so tests aren't throttled by sign-up limits.
	b.ip = "198.51.100." + string(rune('1'+len(email)%9))
	if rec := b.do(http.MethodPost, "/api/v1/auth/register", credentials(email)); rec.Code != http.StatusCreated {
		s.t.Fatalf("register %s: status = %d, body %s", email, rec.Code, rec.Body)
	}
	return b
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

func sessionCookie(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == httpx.SessionCookieName {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", rec.Header())
	return nil
}

// TestHandlers shares one Postgres between its subtests, emptying it each time.
func TestHandlers(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	tests := []struct {
		name string
		run  func(*testing.T, *server)
	}{
		{"sign-up sets a safe session cookie", testRegisterSetsCookie},
		{"responses never carry secrets", testResponsesHideSecrets},
		{"me returns the signed-in user", testMe},
		{"login and logout", testLoginAndLogout},
		{"failed logins are indistinguishable", testLoginFailuresMatch},
		{"sign-up errors have stable codes", testRegisterErrors},
		{"malformed json is refused", testMalformedJSON},
		{"logins are rate limited by address", testLoginRateLimitByIP},
		{"logins are rate limited by account", testLoginRateLimitByEmail},
		{"sign-ups are rate limited", testRegisterRateLimit},
		{"password guesses in a session are rate limited", testChangePasswordRateLimit},
		{"changing a password", testChangePasswordHandler},
		{"sessions list marks the current one", testSessionsList},
		{"users can't reach each other's sessions", testSessionsAreIsolated},
		{"admins can close registration", testAdminClosesRegistration},
		{"settings changes must name the setting", testSettingsNeedTheField},
		{"auth config reports google", testAuthConfig},
		{"google routes are off without credentials", testGoogleDisabled},
		{"google sign-in", testGoogleSignIn},
		{"the flow cookie is safe", testGoogleFlowCookie},
		{"a callback without a flow is refused", testGoogleCallbackWithoutFlow},
		{"a callback with the wrong state is refused", testGoogleCallbackWrongState},
		{"a flow works only once", testGoogleCallbackReplay},
		{"cancelling at google", testGoogleCancelled},
		{"google failures are hidden and redirected", testGoogleFailure},
		{"google can't sign in to a password account", testGoogleAccountExists},
		{"google sign-ins are rate limited", testGoogleRateLimit},
		{"linking google", testGoogleLink},
		{"linking needs a session at the callback", testGoogleLinkNeedsSession},
		{"link failures return to the account page", testGoogleLinkFailure},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, newServer(t, pool, &fakeGoogle{}))
		})
	}
}

func newServer(t *testing.T, pool *pgxpool.Pool, google GoogleClient) *server {
	t.Helper()
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	service := NewService(NewStore(pool))
	s := &server{t: t, pool: pool}
	if fake, ok := google.(*fakeGoogle); ok {
		s.google = fake
	}
	s.handler = httpx.NewRouter(httpx.Deps{
		Logger:       logger,
		DB:           pool,
		PublicOrigin: testOrigin,
		Sessions:     service,
		Auth:         NewHandler(service, google, logger),
		Admin:        admin.NewHandler(admin.NewService(admin.NewStore(pool)), logger),
	})
	return s
}

func testRegisterSetsCookie(t *testing.T, s *server) {
	rec := s.browser().do(http.MethodPost, "/api/v1/auth/register", credentials("person@example.org"))
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	c := sessionCookie(t, rec)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("cookie = %+v", c)
	}
	if c.MaxAge <= 0 {
		t.Errorf("MaxAge = %d, want a lasting cookie", c.MaxAge)
	}
}

func testResponsesHideSecrets(t *testing.T, s *server) {
	b := s.browser()
	rec := b.do(http.MethodPost, "/api/v1/auth/register", credentials("person@example.org"))
	for _, secret := range []string{goodPassword, "argon2", b.cookies[httpx.SessionCookieName], "password_hash"} {
		if strings.Contains(rec.Body.String(), secret) {
			t.Errorf("response contains %q: %s", secret, rec.Body)
		}
	}
}

func testMe(t *testing.T, s *server) {
	b := s.signUp("person@example.org")
	rec := b.do(http.MethodGet, "/api/v1/auth/me", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	me := decode[map[string]any](t, rec)
	if me["email"] != "person@example.org" || me["role"] != "admin" || me["has_password"] != true || me["google_linked"] != false {
		t.Errorf("me = %v", me)
	}
}

func testLoginAndLogout(t *testing.T, s *server) {
	s.signUp("person@example.org")
	b := s.browser()
	if rec := b.do(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("before login: status = %d", rec.Code)
	}
	if rec := b.do(http.MethodPost, "/api/v1/auth/login", credentials("person@example.org")); rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := b.do(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusOK {
		t.Fatalf("after login: status = %d", rec.Code)
	}

	stolen := b.cookies[httpx.SessionCookieName]
	if rec := b.do(http.MethodPost, "/api/v1/auth/logout", nil); rec.Code != http.StatusNoContent {
		t.Fatalf("logout: status = %d", rec.Code)
	}
	if _, ok := b.cookies[httpx.SessionCookieName]; ok {
		t.Error("logout left the cookie in place")
	}
	// The server must forget the session, not just ask the browser to.
	b.cookies[httpx.SessionCookieName] = stolen
	if rec := b.do(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("after logout: status = %d", rec.Code)
	}
}

func testLoginFailuresMatch(t *testing.T, s *server) {
	s.signUp("person@example.org")
	wrong := s.browser().do(http.MethodPost, "/api/v1/auth/login",
		map[string]string{"email": "person@example.org", "password": "wrong password here"})
	unknown := s.browser().do(http.MethodPost, "/api/v1/auth/login", credentials("nobody@example.org"))
	wantError(t, wrong, http.StatusUnauthorized, "invalid_credentials")
	wantError(t, unknown, http.StatusUnauthorized, "invalid_credentials")

	type body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if decode[body](t, wrong).Error.Message != decode[body](t, unknown).Error.Message {
		t.Errorf("messages differ: %s vs %s", wrong.Body, unknown.Body)
	}
}

func testRegisterErrors(t *testing.T, s *server) {
	s.signUp("person@example.org")
	register := func(email, password string) *httptest.ResponseRecorder {
		return s.browser().do(http.MethodPost, "/api/v1/auth/register",
			map[string]string{"email": email, "password": password})
	}
	wantError(t, register("nope", goodPassword), http.StatusBadRequest, "invalid_email")
	wantError(t, register("new@example.org", "short"), http.StatusBadRequest, "weak_password")
	wantError(t, register("person@example.org", goodPassword), http.StatusConflict, "email_taken")

	if err := admin.NewStore(s.pool).SetSetting(context.Background(), "allow_registration", []byte("false")); err != nil {
		t.Fatalf("close registration: %v", err)
	}
	wantError(t, register("new@example.org", goodPassword), http.StatusForbidden, "registration_closed")
}

func testMalformedJSON(t *testing.T, s *server) {
	wantError(t, s.browser().do(http.MethodPost, "/api/v1/auth/login", "{not json"), http.StatusBadRequest, "bad_request")
}

func testLoginRateLimitByIP(t *testing.T, s *server) {
	b := s.browser()
	var last *httptest.ResponseRecorder
	for i := range loginBurst + 1 {
		email := uuid.NewString() + "@example.org"
		last = b.do(http.MethodPost, "/api/v1/auth/login", credentials(email))
		if i < loginBurst && last.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: status = %d", i+1, last.Code)
		}
	}
	wantError(t, last, http.StatusTooManyRequests, "rate_limited")
	if last.Header().Get("Retry-After") == "" {
		t.Error("Retry-After is missing")
	}

	other := s.browser()
	other.ip = "192.0.2.99"
	wantError(t, other.do(http.MethodPost, "/api/v1/auth/login", credentials("a@example.org")), http.StatusUnauthorized, "invalid_credentials")
}

func testLoginRateLimitByEmail(t *testing.T, s *server) {
	var last *httptest.ResponseRecorder
	for i := range loginBurst + 1 {
		b := s.browser()
		b.ip = "203.0.113." + string(rune('0'+i%10)) + string(rune('0'+i/10))
		// The limit must see through different spellings of one address.
		last = b.do(http.MethodPost, "/api/v1/auth/login", credentials(" Target@Example.org"))
	}
	wantError(t, last, http.StatusTooManyRequests, "rate_limited")
}

func testRegisterRateLimit(t *testing.T, s *server) {
	b := s.browser()
	var last *httptest.ResponseRecorder
	for range signUpBurst + 1 {
		last = b.do(http.MethodPost, "/api/v1/auth/register", credentials("not an email"))
	}
	wantError(t, last, http.StatusTooManyRequests, "rate_limited")
}

func testChangePasswordRateLimit(t *testing.T, s *server) {
	b := s.signUp("person@example.org")
	var last *httptest.ResponseRecorder
	for range loginBurst + 1 {
		last = b.do(http.MethodPost, "/api/v1/auth/password",
			map[string]string{"current_password": "wrong password here", "new_password": "a different long password"})
	}
	wantError(t, last, http.StatusTooManyRequests, "rate_limited")
}

func testChangePasswordHandler(t *testing.T, s *server) {
	b := s.signUp("person@example.org")
	wantError(t, b.do(http.MethodPost, "/api/v1/auth/password",
		map[string]string{"current_password": "wrong password here", "new_password": "a different long password"}),
		http.StatusUnauthorized, "invalid_credentials")

	rec := b.do(http.MethodPost, "/api/v1/auth/password",
		map[string]string{"current_password": goodPassword, "new_password": "a different long password"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := b.do(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusOK {
		t.Errorf("current session stopped working: status = %d", rec.Code)
	}
}

type sessionBody struct {
	ID        string `json:"id"`
	Current   bool   `json:"current"`
	UserAgent string `json:"user_agent"`
	IP        string `json:"ip"`
}

func testSessionsList(t *testing.T, s *server) {
	first := s.signUp("person@example.org")
	second := s.browser()
	second.do(http.MethodPost, "/api/v1/auth/login", credentials("person@example.org"))

	sessions := decode[[]sessionBody](t, first.do(http.MethodGet, "/api/v1/auth/sessions", nil))
	if len(sessions) != 2 {
		t.Fatalf("sessions = %d, want 2", len(sessions))
	}
	current := 0
	for _, session := range sessions {
		if session.Current {
			current++
		}
		if session.UserAgent != "test-browser" {
			t.Errorf("user agent = %q", session.UserAgent)
		}
	}
	if current != 1 {
		t.Errorf("sessions marked current = %d, want 1", current)
	}
}

func testSessionsAreIsolated(t *testing.T, s *server) {
	mine := s.signUp("mine@example.org")
	theirs := s.signUp("someone-else@example.org")

	id := decode[[]sessionBody](t, theirs.do(http.MethodGet, "/api/v1/auth/sessions", nil))[0].ID
	wantError(t, mine.do(http.MethodDelete, "/api/v1/auth/sessions/"+id, nil), http.StatusNotFound, "not_found")
	if rec := theirs.do(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusOK {
		t.Errorf("their session was ended: status = %d", rec.Code)
	}
	if got := decode[[]sessionBody](t, mine.do(http.MethodGet, "/api/v1/auth/sessions", nil)); len(got) != 1 || got[0].ID == id {
		t.Errorf("my sessions = %+v", got)
	}

	if rec := theirs.do(http.MethodDelete, "/api/v1/auth/sessions/"+id, nil); rec.Code != http.StatusNoContent {
		t.Fatalf("own revoke: status = %d", rec.Code)
	}
	if rec := theirs.do(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("revoked session still works: status = %d", rec.Code)
	}
}

func testAdminClosesRegistration(t *testing.T, s *server) {
	adminBrowser := s.signUp("admin@example.org")
	user := s.signUp("user-account@example.org")

	wantError(t, user.do(http.MethodPatch, "/api/v1/admin/settings", map[string]bool{"allow_registration": false}),
		http.StatusForbidden, "forbidden")

	rec := adminBrowser.do(http.MethodPatch, "/api/v1/admin/settings", map[string]bool{"allow_registration": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	if got := decode[map[string]bool](t, adminBrowser.do(http.MethodGet, "/api/v1/admin/settings", nil)); got["allow_registration"] {
		t.Errorf("settings = %v", got)
	}
	config := decode[map[string]bool](t, s.browser().do(http.MethodGet, "/api/v1/auth/config", nil))
	if config["registration_open"] {
		t.Errorf("config = %v", config)
	}
	wantError(t, s.browser().do(http.MethodPost, "/api/v1/auth/register", credentials("late@example.org")),
		http.StatusForbidden, "registration_closed")
}

func testSettingsNeedTheField(t *testing.T, s *server) {
	b := s.signUp("admin@example.org")
	wantError(t, b.do(http.MethodPatch, "/api/v1/admin/settings", "{}"), http.StatusBadRequest, "bad_request")
	if got := decode[map[string]bool](t, b.do(http.MethodGet, "/api/v1/admin/settings", nil)); !got["allow_registration"] {
		t.Errorf("an empty body changed the settings: %v", got)
	}
}

func testAuthConfig(t *testing.T, s *server) {
	config := decode[map[string]bool](t, s.browser().do(http.MethodGet, "/api/v1/auth/config", nil))
	if !config["google_enabled"] || !config["registration_open"] {
		t.Errorf("config = %v", config)
	}
}

func testGoogleDisabled(t *testing.T, s *server) {
	off := newServer(t, s.pool, nil)
	config := decode[map[string]bool](t, off.browser().do(http.MethodGet, "/api/v1/auth/config", nil))
	if config["google_enabled"] {
		t.Errorf("config = %v", config)
	}
	wantError(t, off.browser().do(http.MethodGet, "/api/v1/auth/google/start", nil), http.StatusNotFound, "not_found")

	signedIn := off.signUp("person@example.org")
	wantError(t, signedIn.do(http.MethodPost, "/api/v1/auth/google/link", nil), http.StatusNotFound, "not_found")

	rec := off.browser().do(http.MethodGet, "/api/v1/auth/google/callback?code=x&state=y", nil)
	wantRedirect(t, rec, "/sign-in?error=google_failed")
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

// startFlow begins a Google sign-in and returns the state it handed to Google.
func startFlow(t *testing.T, b *browser) string {
	t.Helper()
	rec := b.do(http.MethodGet, "/api/v1/auth/google/start", nil)
	if rec.Code != http.StatusFound {
		t.Fatalf("start: status = %d, body %s", rec.Code, rec.Body)
	}
	return stateFrom(t, rec.Header().Get("Location"))
}

func stateFrom(t *testing.T, location string) string {
	t.Helper()
	parsed, err := url.Parse(location)
	if err != nil {
		t.Fatalf("parse %q: %v", location, err)
	}
	state := parsed.Query().Get("state")
	if state == "" {
		t.Fatalf("no state in %q", location)
	}
	return state
}

func callback(b *browser, state string) *httptest.ResponseRecorder {
	return b.do(http.MethodGet, "/api/v1/auth/google/callback?code=the-code&state="+url.QueryEscape(state), nil)
}

func testGoogleSignIn(t *testing.T, s *server) {
	s.google.identity = googleID("sub-1", "person@example.org")
	b := s.browser()
	state := startFlow(t, b)

	rec := callback(b, state)
	wantRedirect(t, rec, "/")
	sessionCookie(t, rec)
	if s.google.gotCode != "the-code" {
		t.Errorf("code = %q", s.google.gotCode)
	}
	if len(s.google.gotVerifier) < 43 {
		t.Errorf("PKCE verifier is %d characters, the minimum is 43", len(s.google.gotVerifier))
	}

	me := decode[map[string]any](t, b.do(http.MethodGet, "/api/v1/auth/me", nil))
	if me["email"] != "person@example.org" || me["google_linked"] != true || me["has_password"] != false {
		t.Errorf("me = %v", me)
	}
	if _, ok := b.cookies[oauthCookieName]; ok {
		t.Error("the flow cookie was not cleared")
	}
}

func testGoogleFlowCookie(t *testing.T, s *server) {
	rec := s.browser().do(http.MethodGet, "/api/v1/auth/google/start", nil)
	for _, c := range rec.Result().Cookies() {
		if c.Name != oauthCookieName {
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

func testGoogleCallbackWithoutFlow(t *testing.T, s *server) {
	s.google.identity = googleID("sub-1", "person@example.org")
	rec := callback(s.browser(), "made-up")
	wantRedirect(t, rec, "/sign-in?error=google_failed")
	if s.google.gotCode != "" {
		t.Error("the code was redeemed without a flow")
	}
}

// A callback URL prepared by an attacker must not sign the victim's browser in
// to the attacker's account.
func testGoogleCallbackWrongState(t *testing.T, s *server) {
	s.google.identity = googleID("attacker-sub", "attacker@example.org")
	victim := s.browser()
	startFlow(t, victim)
	attacker := s.browser()
	attackerState := startFlow(t, attacker)

	rec := callback(victim, attackerState)
	wantRedirect(t, rec, "/sign-in?error=google_failed")
	if s.google.gotCode != "" {
		t.Error("the code was redeemed despite the wrong state")
	}
	if rec := victim.do(http.MethodGet, "/api/v1/auth/me", nil); rec.Code != http.StatusUnauthorized {
		t.Errorf("victim is signed in: status = %d", rec.Code)
	}
}

func testGoogleCallbackReplay(t *testing.T, s *server) {
	s.google.identity = googleID("sub-1", "person@example.org")
	b := s.browser()
	state := startFlow(t, b)
	wantRedirect(t, callback(b, state), "/")
	wantRedirect(t, callback(b, state), "/sign-in?error=google_failed")
}

func testGoogleCancelled(t *testing.T, s *server) {
	b := s.browser()
	state := startFlow(t, b)
	rec := b.do(http.MethodGet, "/api/v1/auth/google/callback?error=access_denied&state="+url.QueryEscape(state), nil)
	wantRedirect(t, rec, "/sign-in?error=google_cancelled")
}

func testGoogleFailure(t *testing.T, s *server) {
	s.google.err = errors.New("token endpoint said secret-detail")
	b := s.browser()
	rec := callback(b, startFlow(t, b))
	wantRedirect(t, rec, "/sign-in?error=google_failed")
	if strings.Contains(rec.Body.String(), "secret-detail") {
		t.Errorf("response leaks the failure: %s", rec.Body)
	}
}

func testGoogleAccountExists(t *testing.T, s *server) {
	s.signUp("person@example.org")
	s.google.identity = googleID("sub-1", "person@example.org")
	b := s.browser()
	rec := callback(b, startFlow(t, b))
	wantRedirect(t, rec, "/sign-in?error=account_exists")
	if _, ok := b.cookies[httpx.SessionCookieName]; ok {
		t.Error("a session was started")
	}
}

func testGoogleRateLimit(t *testing.T, s *server) {
	b := s.browser()
	var last *httptest.ResponseRecorder
	for range loginBurst + 1 {
		last = b.do(http.MethodGet, "/api/v1/auth/google/start", nil)
	}
	wantError(t, last, http.StatusTooManyRequests, "rate_limited")
	wantRedirect(t, callback(b, "x"), "/sign-in?error=rate_limited")
}

func startLink(t *testing.T, b *browser) string {
	t.Helper()
	rec := b.do(http.MethodPost, "/api/v1/auth/google/link", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("link: status = %d, body %s", rec.Code, rec.Body)
	}
	return stateFrom(t, decode[map[string]string](t, rec)["url"])
}

func testGoogleLink(t *testing.T, s *server) {
	b := s.signUp("person@example.org")
	s.google.identity = googleID("sub-1", "another-address@example.org")

	wantRedirect(t, callback(b, startLink(t, b)), "/account?linked=google")

	me := decode[map[string]any](t, b.do(http.MethodGet, "/api/v1/auth/me", nil))
	if me["google_linked"] != true {
		t.Errorf("me = %v", me)
	}

	other := s.browser()
	wantRedirect(t, callback(other, startFlow(t, other)), "/")
	signedIn := decode[map[string]any](t, other.do(http.MethodGet, "/api/v1/auth/me", nil))
	if signedIn["email"] != "person@example.org" {
		t.Errorf("signed in as %v", signedIn)
	}
}

func testGoogleLinkNeedsSession(t *testing.T, s *server) {
	b := s.signUp("person@example.org")
	s.google.identity = googleID("sub-1", "person@example.org")
	state := startLink(t, b)
	delete(b.cookies, httpx.SessionCookieName)

	wantRedirect(t, callback(b, state), "/sign-in?error=google_failed")
	var count int
	if err := s.pool.QueryRow(context.Background(), `SELECT count(*) FROM user_identities`).Scan(&count); err != nil {
		t.Fatalf("count identities: %v", err)
	}
	if count != 0 {
		t.Errorf("identities = %d, want 0", count)
	}
}

func testGoogleLinkFailure(t *testing.T, s *server) {
	owner := s.signUp("owner@example.org")
	other := s.signUp("someone-else@example.org")
	s.google.identity = googleID("sub-1", "owner@example.org")
	wantRedirect(t, callback(owner, startLink(t, owner)), "/account?linked=google")

	wantRedirect(t, callback(other, startLink(t, other)), "/account?error=google_already_linked")

	s.google.identity = GoogleIdentity{Subject: "sub-2", Email: "x@example.org", EmailVerified: false}
	third := s.signUp("third-person@example.org")
	wantRedirect(t, callback(third, startLink(t, third)), "/account?error=google_email_unverified")
}
