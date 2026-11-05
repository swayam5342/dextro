package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

const (
	googleIssuer  = "https://accounts.google.com"
	googleTimeout = 10 * time.Second
)

// Google runs the OpenID Connect sign-in against Google. Only the person's
// identity is used, so Google's tokens are dropped rather than stored.
type Google struct {
	issuer       string
	clientID     string
	clientSecret string
	redirectURL  string
	httpClient   *http.Client

	mu       sync.Mutex
	provider *oidc.Provider
}

// NewGoogle returns a client for Google's real endpoints. It makes no network
// calls until first used, so an outage at Google can't stop the server starting.
func NewGoogle(clientID, clientSecret, redirectURL string) *Google {
	return newGoogle(googleIssuer, clientID, clientSecret, redirectURL)
}

func newGoogle(issuer, clientID, clientSecret, redirectURL string) *Google {
	return &Google{
		issuer:       issuer,
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURL:  redirectURL,
		httpClient:   &http.Client{Timeout: googleTimeout},
	}
}

// config discovers Google's endpoints on first use and keeps them.
func (g *Google) config(ctx context.Context) (*oidc.Provider, oauth2.Config, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.provider == nil {
		// The provider keeps this context to refresh signing keys later, so it
		// must outlive the request that happened to come first.
		discovery := oidc.ClientContext(context.WithoutCancel(ctx), g.httpClient)
		provider, err := oidc.NewProvider(discovery, g.issuer)
		if err != nil {
			return nil, oauth2.Config{}, fmt.Errorf("discover google endpoints: %w", err)
		}
		g.provider = provider
	}
	return g.provider, oauth2.Config{
		ClientID:     g.clientID,
		ClientSecret: g.clientSecret,
		RedirectURL:  g.redirectURL,
		Endpoint:     g.provider.Endpoint(),
		Scopes:       []string{oidc.ScopeOpenID, "email"},
	}, nil
}

// AuthCodeURL returns the address to send the browser to.
func (g *Google) AuthCodeURL(ctx context.Context, state, nonce, verifier string) (string, error) {
	_, config, err := g.config(ctx)
	if err != nil {
		return "", err
	}
	return config.AuthCodeURL(state, oidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier)), nil
}

// Identify redeems an authorization code and returns who signed in. The ID token
// is checked for its signature, issuer, audience, expiry and nonce.
func (g *Google) Identify(ctx context.Context, code, nonce, verifier string) (GoogleIdentity, error) {
	provider, config, err := g.config(ctx)
	if err != nil {
		return GoogleIdentity{}, err
	}
	ctx = oidc.ClientContext(ctx, g.httpClient)
	token, err := config.Exchange(ctx, code, oauth2.VerifierOption(verifier))
	if err != nil {
		return GoogleIdentity{}, fmt.Errorf("redeem google code: %w", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		return GoogleIdentity{}, errors.New("google sent no id token")
	}
	idToken, err := provider.Verifier(&oidc.Config{ClientID: g.clientID}).Verify(ctx, rawIDToken)
	if err != nil {
		return GoogleIdentity{}, fmt.Errorf("verify google id token: %w", err)
	}
	if idToken.Nonce != nonce {
		return GoogleIdentity{}, errors.New("google id token has the wrong nonce")
	}
	var claims struct {
		Email         string `json:"email"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := idToken.Claims(&claims); err != nil {
		return GoogleIdentity{}, fmt.Errorf("read google claims: %w", err)
	}
	return GoogleIdentity{Subject: idToken.Subject, Email: claims.Email, EmailVerified: claims.EmailVerified}, nil
}
