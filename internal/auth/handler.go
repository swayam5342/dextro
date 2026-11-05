package auth

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

const (
	oauthCookieName = "dextro_oauth"
	oauthCookiePath = "/api/v1/auth/google"
	oauthFlowTTL    = 10 * time.Minute

	flowLogin = "login"
	flowLink  = "link"

	maxUserAgentBytes = 256
)

// Rate limits. An attempt is spent on every try, right or wrong.
const (
	loginBurst       = 10
	loginEveryByIP   = 30 * time.Second
	loginEveryByUser = time.Minute
	signUpBurst      = 5
	signUpEvery      = 5 * time.Minute
)

// Error codes the auth API adds to the shared ones. Clients may depend on these.
const (
	codeInvalidCredentials = "invalid_credentials" //nolint:gosec // An error code, not a credential.
	codeInvalidEmail       = "invalid_email"
	codeWeakPassword       = "weak_password"
	codeEmailTaken         = "email_taken"
	codeRegistrationClosed = "registration_closed"
	codePasswordNotSet     = "password_not_set"
	codePasswordUnchanged  = "password_unchanged"
	codeOwnAccount         = "own_account"
)

// Codes sent to the UI in the error query parameter after a Google redirect.
const (
	redirectGoogleFailed    = "google_failed"
	redirectGoogleCancelled = "google_cancelled"
	redirectAccountExists   = "account_exists"
	redirectEmailUnverified = "google_email_unverified"
	redirectAlreadyLinked   = "google_already_linked"
)

// GoogleClient is the part of Google sign-in the handlers need.
type GoogleClient interface {
	AuthCodeURL(ctx context.Context, state, nonce, verifier string) (string, error)
	Identify(ctx context.Context, code, nonce, verifier string) (GoogleIdentity, error)
}

// Handler serves the auth routes.
type Handler struct {
	service *Service
	// google is nil when Google sign-in isn't configured.
	google      GoogleClient
	logger      *slog.Logger
	loginByIP   *httpx.RateLimiter
	loginByUser *httpx.RateLimiter
	signUpsByIP *httpx.RateLimiter
}

// NewHandler returns a Handler for service. Pass a nil google to turn Google
// sign-in off.
func NewHandler(service *Service, google GoogleClient, logger *slog.Logger) *Handler {
	return &Handler{
		service:     service,
		google:      google,
		logger:      logger,
		loginByIP:   httpx.NewRateLimiter(loginEveryByIP, loginBurst),
		loginByUser: httpx.NewRateLimiter(loginEveryByUser, loginBurst),
		signUpsByIP: httpx.NewRateLimiter(signUpEvery, signUpBurst),
	}
}

// GetAuthConfig implements apigen.ServerInterface.
func (h *Handler) GetAuthConfig(w http.ResponseWriter, r *http.Request) {
	open, err := h.service.RegistrationOpen(r.Context())
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.AuthConfig{RegistrationOpen: open, GoogleEnabled: h.google != nil})
}

// Register implements apigen.ServerInterface.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	if !h.signUpsByIP.Allow(ipKey(r)) {
		writeRateLimited(w, r, signUpEvery)
		return
	}
	var body apigen.Credentials
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	issued, err := h.service.Register(r.Context(), body.Email, body.Password, clientFrom(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	setSessionCookie(w, issued)
	httpx.WriteJSON(w, http.StatusCreated, toAPIUser(issued.User))
}

// Login implements apigen.ServerInterface.
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	var body apigen.Credentials
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	if !h.loginByIP.Allow(ipKey(r)) {
		writeRateLimited(w, r, loginEveryByIP)
		return
	}
	// Per account too, or guesses spread across many addresses would never slow.
	if !h.loginByUser.Allow(strings.ToLower(strings.TrimSpace(body.Email))) {
		writeRateLimited(w, r, loginEveryByUser)
		return
	}
	issued, err := h.service.Login(r.Context(), body.Email, body.Password, clientFrom(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	setSessionCookie(w, issued)
	httpx.WriteJSON(w, http.StatusOK, toAPIUser(issued.User))
}

// Logout implements apigen.ServerInterface.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	if err := h.service.Logout(r.Context(), principal, clientFrom(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	clearCookie(w, httpx.SessionCookieName, "/")
	w.WriteHeader(http.StatusNoContent)
}

// GetMe implements apigen.ServerInterface.
func (h *Handler) GetMe(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	user, err := h.service.Me(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIUser(user))
}

// ChangePassword implements apigen.ServerInterface.
func (h *Handler) ChangePassword(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	// A stolen session mustn't become an unlimited way to guess the password.
	if !h.loginByUser.Allow(principal.UserID.String()) {
		writeRateLimited(w, r, loginEveryByUser)
		return
	}
	var body apigen.ChangePassword
	if !httpx.DecodeJSON(w, r, &body) {
		return
	}
	err := h.service.ChangePassword(r.Context(), principal, body.CurrentPassword, body.NewPassword, clientFrom(r))
	if err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ListSessions implements apigen.ServerInterface.
func (h *Handler) ListSessions(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	sessions, err := h.service.ListSessions(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := make([]apigen.Session, 0, len(sessions))
	for _, s := range sessions {
		body = append(body, apigen.Session{
			Id:         s.ID,
			Current:    s.ID == principal.SessionID,
			UserAgent:  s.UserAgent,
			Ip:         s.IP,
			CreatedAt:  s.CreatedAt,
			LastSeenAt: s.LastSeenAt,
			ExpiresAt:  s.ExpiresAt,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// RevokeSession implements apigen.ServerInterface.
func (h *Handler) RevokeSession(w http.ResponseWriter, r *http.Request, id types.UUID) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	if err := h.service.RevokeSession(r.Context(), principal.UserID, id, clientFrom(r)); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// StartGoogleSignIn implements apigen.ServerInterface.
func (h *Handler) StartGoogleSignIn(w http.ResponseWriter, r *http.Request) {
	if h.google == nil {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "Not found.")
		return
	}
	if !h.loginByIP.Allow(ipKey(r)) {
		writeRateLimited(w, r, loginEveryByIP)
		return
	}
	target, err := h.beginFlow(w, r, flowLogin)
	if err != nil {
		h.logger.ErrorContext(r.Context(), "start google sign-in",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		http.Redirect(w, r, "/sign-in?error="+redirectGoogleFailed, http.StatusFound)
		return
	}
	// target comes from Google's discovery document, never from the request.
	http.Redirect(w, r, target, http.StatusFound) //nolint:gosec
}

// LinkGoogle implements apigen.ServerInterface.
func (h *Handler) LinkGoogle(w http.ResponseWriter, r *http.Request) {
	if h.google == nil {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "Not found.")
		return
	}
	target, err := h.beginFlow(w, r, flowLink)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, apigen.GoogleRedirect{Url: target})
}

// beginFlow remembers the flow in a short-lived cookie and returns Google's URL.
// The cookie ties the callback to this browser, which stops an attacker from
// feeding someone a callback URL for the attacker's own Google account.
func (h *Handler) beginFlow(w http.ResponseWriter, r *http.Request, mode string) (string, error) {
	flow := oauthFlow{mode: mode, state: newSecret(), nonce: newSecret(), verifier: newSecret()}
	target, err := h.google.AuthCodeURL(r.Context(), flow.state, flow.nonce, flow.verifier)
	if err != nil {
		return "", err
	}
	http.SetCookie(w, &http.Cookie{
		Name:     oauthCookieName,
		Value:    flow.encode(),
		Path:     oauthCookiePath,
		MaxAge:   int(oauthFlowTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		// Strict would keep the cookie off the redirect back from Google.
		SameSite: http.SameSiteLaxMode,
	})
	return target, nil
}

// GoogleCallback implements apigen.ServerInterface. It always redirects into the
// UI, because the browser arrives here by navigating.
func (h *Handler) GoogleCallback(w http.ResponseWriter, r *http.Request, params apigen.GoogleCallbackParams) {
	clearCookie(w, oauthCookieName, oauthCookiePath)
	if !h.loginByIP.Allow(ipKey(r)) {
		redirectError(w, r, "/sign-in", httpx.CodeRateLimited)
		return
	}
	flow, ok := readFlow(r)
	if h.google == nil || !ok || params.State == nil ||
		subtle.ConstantTimeCompare([]byte(*params.State), []byte(flow.state)) != 1 {
		redirectError(w, r, "/sign-in", redirectGoogleFailed)
		return
	}

	failureTarget := "/sign-in"
	if flow.mode == flowLink {
		failureTarget = "/account"
	}
	if params.Code == nil {
		redirectError(w, r, failureTarget, redirectGoogleCancelled)
		return
	}
	identity, err := h.google.Identify(r.Context(), *params.Code, flow.nonce, flow.verifier)
	if err != nil {
		h.logger.WarnContext(r.Context(), "google identify failed",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		redirectError(w, r, failureTarget, redirectGoogleFailed)
		return
	}

	if flow.mode == flowLink {
		h.finishLink(w, r, identity)
		return
	}
	issued, err := h.service.SignInWithGoogle(r.Context(), identity, clientFrom(r))
	if err != nil {
		redirectError(w, r, failureTarget, h.redirectCode(r, err))
		return
	}
	setSessionCookie(w, issued)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) finishLink(w http.ResponseWriter, r *http.Request, identity GoogleIdentity) {
	principal, signedIn := httpx.PrincipalFrom(r.Context())
	if !signedIn {
		redirectError(w, r, "/sign-in", redirectGoogleFailed)
		return
	}
	if err := h.service.LinkGoogle(r.Context(), principal.UserID, identity, clientFrom(r)); err != nil {
		redirectError(w, r, "/account", h.redirectCode(r, err))
		return
	}
	http.Redirect(w, r, "/account?linked=google", http.StatusFound)
}

// redirectCode maps a sign-in error to the code the UI understands.
func (h *Handler) redirectCode(r *http.Request, err error) string {
	switch {
	case errors.Is(err, ErrAccountExists):
		return redirectAccountExists
	case errors.Is(err, ErrRegistrationClosed):
		return codeRegistrationClosed
	case errors.Is(err, ErrGoogleEmailUnverified):
		return redirectEmailUnverified
	case errors.Is(err, ErrGoogleAlreadyLinked):
		return redirectAlreadyLinked
	}
	h.logger.ErrorContext(r.Context(), "google sign-in failed",
		"request_id", httpx.RequestID(r.Context()), "error", err)
	return redirectGoogleFailed
}

func redirectError(w http.ResponseWriter, r *http.Request, target, code string) {
	http.Redirect(w, r, target+"?error="+code, http.StatusFound)
}

// fail sends the response for a service error. Anything unexpected is logged
// and reaches the client only as a generic message.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrInvalidCredentials):
		httpx.WriteError(w, r, http.StatusUnauthorized, codeInvalidCredentials, "The email or password is incorrect.")
	case errors.Is(err, ErrInvalidEmail):
		httpx.WriteError(w, r, http.StatusBadRequest, codeInvalidEmail, "Enter a valid email address.")
	case errors.Is(err, ErrWeakPassword):
		httpx.WriteError(w, r, http.StatusBadRequest, codeWeakPassword,
			"Use a password between "+strconv.Itoa(minPasswordLength)+" and "+strconv.Itoa(maxPasswordLength)+" characters.")
	case errors.Is(err, ErrPasswordUnchanged):
		httpx.WriteError(w, r, http.StatusBadRequest, codePasswordUnchanged, "Choose a new password, different from this one.")
	case errors.Is(err, ErrOwnAccount):
		httpx.WriteError(w, r, http.StatusBadRequest, codeOwnAccount, "You can't do that to your own account here.")
	case errors.Is(err, ErrEmailTaken):
		httpx.WriteError(w, r, http.StatusConflict, codeEmailTaken, "An account with this email already exists.")
	case errors.Is(err, ErrRegistrationClosed):
		httpx.WriteError(w, r, http.StatusForbidden, codeRegistrationClosed, "This server doesn't take sign-ups. Ask its admin for an account.")
	case errors.Is(err, ErrInvalidAPIKey):
		httpx.WriteError(w, r, http.StatusBadRequest, codeInvalidAPIKey, "Give the key a name and at least one permission.")
	case errors.Is(err, ErrNoPassword):
		httpx.WriteError(w, r, http.StatusBadRequest, codePasswordNotSet, "This account signs in with Google and has no password.")
	case errors.Is(err, ErrSessionNotFound), errors.Is(err, ErrAPIKeyNotFound), errors.Is(err, ErrUserNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "Not found.")
	default:
		h.logger.ErrorContext(r.Context(), "auth request failed",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "Something went wrong.")
	}
}

func writeRateLimited(w http.ResponseWriter, r *http.Request, retryAfter time.Duration) {
	w.Header().Set("Retry-After", strconv.Itoa(int(retryAfter.Seconds())))
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "Too many attempts. Try again later.")
}

func ipKey(r *http.Request) string {
	return httpx.RateKey(httpx.ClientIP(r.Context()))
}

func clientFrom(r *http.Request) Client {
	userAgent := r.UserAgent()
	if len(userAgent) > maxUserAgentBytes {
		userAgent = userAgent[:maxUserAgentBytes]
	}
	return Client{
		IP: httpx.ClientIP(r.Context()),
		// Postgres refuses invalid UTF-8, and the header is whatever the client sent.
		UserAgent: strings.ToValidUTF8(userAgent, ""),
	}
}

func setSessionCookie(w http.ResponseWriter, issued Issued) {
	http.SetCookie(w, &http.Cookie{
		Name:     httpx.SessionCookieName,
		Value:    issued.Token,
		Path:     "/",
		Expires:  issued.ExpiresAt,
		MaxAge:   int(time.Until(issued.ExpiresAt).Seconds()),
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearCookie(w http.ResponseWriter, name, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Path:     path,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteLaxMode,
	})
}

func toAPIUser(u User) apigen.User {
	return apigen.User{
		Id:                 u.ID,
		Email:              u.Email,
		Role:               apigen.UserRole(u.Role),
		HasPassword:        u.HasPassword,
		GoogleLinked:       u.GoogleLinked,
		CreatedAt:          u.CreatedAt,
		MustChangePassword: u.MustChangePassword,
	}
}

// oauthFlow is what the browser carries between leaving for Google and coming
// back. The values are secrets, but only from other people: they stay in this
// browser's HttpOnly cookie for ten minutes and are useless after the callback.
type oauthFlow struct {
	mode, state, nonce, verifier string
}

func (f oauthFlow) encode() string {
	return strings.Join([]string{f.mode, f.state, f.nonce, f.verifier}, ".")
}

func readFlow(r *http.Request) (oauthFlow, bool) {
	cookie, err := r.Cookie(oauthCookieName)
	if err != nil {
		return oauthFlow{}, false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 4 || (parts[0] != flowLogin && parts[0] != flowLink) {
		return oauthFlow{}, false
	}
	return oauthFlow{mode: parts[0], state: parts[1], nonce: parts[2], verifier: parts[3]}, true
}
