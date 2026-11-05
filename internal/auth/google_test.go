package auth

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

const (
	testClientID = "dextro-test.apps.googleusercontent.com"
	testNonce    = "the-nonce"
	testVerifier = "the-verifier-the-verifier-the-verifier-the-verifier"
)

// fakeIssuer is a minimal OpenID provider, so the real client can be tested
// without reaching Google.
type fakeIssuer struct {
	server *httptest.Server
	key    *rsa.PrivateKey
	// claims builds the ID token returned by the token endpoint.
	claims map[string]any
	// signWith overrides the signing key, to forge a token.
	signWith    *rsa.PrivateKey
	gotVerifier string
	discoveries int
}

func newFakeIssuer(t *testing.T) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	f := &fakeIssuer{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /.well-known/openid-configuration", f.discovery)
	mux.HandleFunc("GET /keys", f.keys)
	mux.HandleFunc("POST /token", f.token)
	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	f.claims = map[string]any{
		"iss":            f.server.URL,
		"aud":            testClientID,
		"sub":            "google-subject",
		"nonce":          testNonce,
		"email":          "person@example.org",
		"email_verified": true,
		"iat":            time.Now().Unix(),
		"exp":            time.Now().Add(time.Hour).Unix(),
	}
	return f
}

func (f *fakeIssuer) discovery(w http.ResponseWriter, _ *http.Request) {
	f.discoveries++
	_ = json.NewEncoder(w).Encode(map[string]any{
		"issuer":                                f.server.URL,
		"authorization_endpoint":                f.server.URL + "/auth",
		"token_endpoint":                        f.server.URL + "/token",
		"jwks_uri":                              f.server.URL + "/keys",
		"id_token_signing_alg_values_supported": []string{"RS256"},
	})
}

func (f *fakeIssuer) keys(w http.ResponseWriter, _ *http.Request) {
	encode := base64.RawURLEncoding.EncodeToString
	_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{
		"kty": "RSA",
		"alg": "RS256",
		"use": "sig",
		"kid": "test",
		"n":   encode(f.key.N.Bytes()),
		"e":   encode(big.NewInt(int64(f.key.E)).Bytes()),
	}}})
}

func (f *fakeIssuer) token(w http.ResponseWriter, r *http.Request) {
	f.gotVerifier = r.PostFormValue("code_verifier")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": "access",
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     f.idToken(),
	})
}

func (f *fakeIssuer) idToken() string {
	encode := base64.RawURLEncoding.EncodeToString
	header, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test", "typ": "JWT"})
	payload, _ := json.Marshal(f.claims)
	signingInput := encode(header) + "." + encode(payload)
	sum := sha256.Sum256([]byte(signingInput))
	key := f.key
	if f.signWith != nil {
		key = f.signWith
	}
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		panic(err)
	}
	return signingInput + "." + encode(signature)
}

func newTestGoogle(f *fakeIssuer) *Google {
	return newGoogle(f.server.URL, testClientID, "client-secret", "https://files.example.org/callback")
}

func TestGoogleAuthURL(t *testing.T) {
	f := newFakeIssuer(t)
	raw, err := newTestGoogle(f).AuthCodeURL(context.Background(), "the-state", testNonce, testVerifier)
	if err != nil {
		t.Fatalf("AuthCodeURL: %v", err)
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !strings.HasPrefix(raw, f.server.URL+"/auth?") {
		t.Errorf("url = %s", raw)
	}
	q := parsed.Query()
	want := map[string]string{
		"state":                 "the-state",
		"nonce":                 testNonce,
		"client_id":             testClientID,
		"redirect_uri":          "https://files.example.org/callback",
		"response_type":         "code",
		"code_challenge_method": "S256",
	}
	for key, value := range want {
		if got := q.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if q.Get("code_challenge") == "" || strings.Contains(raw, testVerifier) {
		t.Errorf("url must carry the PKCE challenge, not the verifier: %s", raw)
	}
	if scope := q.Get("scope"); !strings.Contains(scope, "openid") || !strings.Contains(scope, "email") {
		t.Errorf("scope = %q", scope)
	}
}

func TestGoogleIdentify(t *testing.T) {
	f := newFakeIssuer(t)
	id, err := newTestGoogle(f).Identify(context.Background(), "code", testNonce, testVerifier)
	if err != nil {
		t.Fatalf("Identify: %v", err)
	}
	if id.Subject != "google-subject" || id.Email != "person@example.org" || !id.EmailVerified {
		t.Errorf("identity = %+v", id)
	}
	if f.gotVerifier != testVerifier {
		t.Errorf("code_verifier = %q", f.gotVerifier)
	}
}

func TestGoogleReportsAnUnverifiedEmailAsUnverified(t *testing.T) {
	for name, claims := range map[string]func(map[string]any){
		"false":   func(c map[string]any) { c["email_verified"] = false },
		"missing": func(c map[string]any) { delete(c, "email_verified") },
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeIssuer(t)
			claims(f.claims)
			id, err := newTestGoogle(f).Identify(context.Background(), "code", testNonce, testVerifier)
			if err != nil {
				t.Fatalf("Identify: %v", err)
			}
			if id.EmailVerified {
				t.Error("EmailVerified = true")
			}
		})
	}
}

func TestGoogleRefusesBadIDTokens(t *testing.T) {
	otherKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tests := []struct {
		name   string
		tamper func(*fakeIssuer)
	}{
		{"forged signature", func(f *fakeIssuer) { f.signWith = otherKey }},
		{"another app's token", func(f *fakeIssuer) { f.claims["aud"] = "someone-else" }},
		{"another issuer", func(f *fakeIssuer) { f.claims["iss"] = "https://evil.example" }},
		{"expired", func(f *fakeIssuer) { f.claims["exp"] = time.Now().Add(-time.Hour).Unix() }},
		{"replayed from another flow", func(f *fakeIssuer) { f.claims["nonce"] = "another-nonce" }},
		{"no nonce", func(f *fakeIssuer) { delete(f.claims, "nonce") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFakeIssuer(t)
			tt.tamper(f)
			if _, err := newTestGoogle(f).Identify(context.Background(), "code", testNonce, testVerifier); err == nil {
				t.Fatal("Identify succeeded, want error")
			}
		})
	}
}

func TestGoogleDiscoversOnFirstUseNotAtStartup(t *testing.T) {
	f := newFakeIssuer(t)
	g := newTestGoogle(f)
	if f.discoveries != 0 {
		t.Fatalf("discoveries = %d before first use", f.discoveries)
	}
	for range 2 {
		if _, err := g.AuthCodeURL(context.Background(), "s", testNonce, testVerifier); err != nil {
			t.Fatalf("AuthCodeURL: %v", err)
		}
	}
	if f.discoveries != 1 {
		t.Errorf("discoveries = %d, want 1", f.discoveries)
	}
}

func TestGoogleKeepsWorkingAfterTheFirstRequestEnds(t *testing.T) {
	f := newFakeIssuer(t)
	g := newTestGoogle(f)
	first, cancel := context.WithCancel(context.Background())
	if _, err := g.AuthCodeURL(first, "s", testNonce, testVerifier); err != nil {
		t.Fatalf("AuthCodeURL: %v", err)
	}
	cancel()
	if _, err := g.Identify(context.Background(), "code", testNonce, testVerifier); err != nil {
		t.Errorf("Identify after the first context ended: %v", err)
	}
}
