package storage

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/oapi-codegen/runtime/types"

	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

const (
	flowCookieName = "dextro_drive_oauth"
	flowCookiePath = "/api/v1/storage/google"
	flowTTL        = 10 * time.Minute
	secretBytes    = 32

	// The browser is waiting on the redirect while this runs.
	afterConnectTimeout = 30 * time.Second

	// Each check calls out to Google, so they are limited per user.
	checkBurst = 10
	checkEvery = 30 * time.Second
)

// Error codes the storage API adds to the shared ones. Clients may depend on these.
const (
	codeConnectionFailed = "connection_failed"
)

// Codes sent to the UI in the error query parameter after a Google redirect.
const (
	redirectGoogleFailed    = "google_failed"
	redirectGoogleCancelled = "google_cancelled"
)

// AfterConnect runs once an account is linked. The files feature passes this in,
// because it imports this one.
type AfterConnect func(ctx context.Context, userID, connectionID uuid.UUID, ip string) error

// Handler serves the storage routes.
type Handler struct {
	service *Service
	logger  *slog.Logger
	// afterConnect may be nil.
	afterConnect AfterConnect
	checks       *httpx.RateLimiter
}

// NewHandler returns a Handler for service.
func NewHandler(service *Service, logger *slog.Logger, afterConnect AfterConnect) *Handler {
	return &Handler{service: service, logger: logger, afterConnect: afterConnect, checks: httpx.NewRateLimiter(checkEvery, checkBurst)}
}

// ListConnections implements apigen.ServerInterface.
func (h *Handler) ListConnections(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	connections, err := h.service.List(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	body := make([]apigen.Connection, 0, len(connections))
	for _, c := range connections {
		body = append(body, toAPI(c))
	}
	httpx.WriteJSON(w, http.StatusOK, body)
}

// GetPool implements apigen.ServerInterface.
func (h *Handler) GetPool(w http.ResponseWriter, r *http.Request) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	pool, err := h.service.Pool(r.Context(), principal.UserID)
	if err != nil {
		h.fail(w, r, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, toAPIPool(pool))
}

// DeleteConnection implements apigen.ServerInterface.
func (h *Handler) DeleteConnection(w http.ResponseWriter, r *http.Request, id types.UUID) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	if err := h.service.Delete(r.Context(), principal.UserID, id, httpx.ClientIP(r.Context())); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// TestConnection implements apigen.ServerInterface.
func (h *Handler) TestConnection(w http.ResponseWriter, r *http.Request, id types.UUID) {
	principal, _ := httpx.PrincipalFrom(r.Context())
	if !h.allowCheck(w, r, principal.UserID) {
		return
	}
	if err := h.service.Test(r.Context(), principal.UserID, id); err != nil {
		h.fail(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ConnectGoogleDrive implements apigen.ServerInterface. The cookie ties the
// callback to this browser, which stops an attacker from feeding someone a
// callback URL that would attach the attacker's Drive to their account.
func (h *Handler) ConnectGoogleDrive(w http.ResponseWriter, r *http.Request) {
	if !h.service.DriveEnabled() {
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "Not found.")
		return
	}
	state, verifier := newSecret(), newSecret()
	http.SetCookie(w, &http.Cookie{
		Name:     flowCookieName,
		Value:    state + "." + verifier,
		Path:     flowCookiePath,
		MaxAge:   int(flowTTL.Seconds()),
		HttpOnly: true,
		Secure:   true,
		// Strict would keep the cookie off the redirect back from Google.
		SameSite: http.SameSiteLaxMode,
	})
	httpx.WriteJSON(w, http.StatusOK, apigen.GoogleRedirect{Url: h.service.DriveAuthURL(state, verifier)})
}

// GoogleDriveCallback implements apigen.ServerInterface. It always redirects into
// the UI, because the browser arrives here by navigating.
func (h *Handler) GoogleDriveCallback(w http.ResponseWriter, r *http.Request, params apigen.GoogleDriveCallbackParams) {
	http.SetCookie(w, &http.Cookie{
		Name: flowCookieName, Path: flowCookiePath, MaxAge: -1,
		HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode,
	})
	state, verifier, ok := readFlow(r)
	if !h.service.DriveEnabled() || !ok || params.State == nil ||
		subtle.ConstantTimeCompare([]byte(*params.State), []byte(state)) != 1 {
		redirectError(w, r, redirectGoogleFailed)
		return
	}
	if params.Code == nil {
		redirectError(w, r, redirectGoogleCancelled)
		return
	}
	principal, _ := httpx.PrincipalFrom(r.Context())
	connection, err := h.service.ConnectDrive(r.Context(), principal.UserID, *params.Code, verifier, httpx.ClientIP(r.Context()))
	if err != nil {
		h.logger.WarnContext(r.Context(), "connect google drive",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		redirectError(w, r, redirectGoogleFailed)
		return
	}
	h.syncNewAccount(r, principal.UserID, connection.ID)
	http.Redirect(w, r, "/storage?connected=google", http.StatusFound)
}

// syncNewAccount brings back what's already in an account, so its files show up the
// moment it's linked. It's not worth failing the link over: the background sync
// picks up whatever this doesn't finish.
func (h *Handler) syncNewAccount(r *http.Request, userID, connectionID uuid.UUID) {
	if h.afterConnect == nil {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), afterConnectTimeout)
	defer cancel()
	if err := h.afterConnect(ctx, userID, connectionID, httpx.ClientIP(r.Context())); err != nil {
		h.logger.WarnContext(r.Context(), "sync new account",
			"request_id", httpx.RequestID(r.Context()), "error", err)
	}
}

func readFlow(r *http.Request) (state, verifier string, ok bool) {
	cookie, err := r.Cookie(flowCookieName)
	if err != nil {
		return "", "", false
	}
	return strings.Cut(cookie.Value, ".")
}

func redirectError(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/storage?error="+code, http.StatusFound)
}

func (h *Handler) allowCheck(w http.ResponseWriter, r *http.Request, userID uuid.UUID) bool {
	if h.checks.Allow(userID.String()) {
		return true
	}
	w.Header().Set("Retry-After", strconv.Itoa(int(checkEvery.Seconds())))
	httpx.WriteError(w, r, http.StatusTooManyRequests, httpx.CodeRateLimited, "Too many attempts. Try again later.")
	return false
}

// fail sends the response for a service error. Why a connection failed goes to
// the logs only, because the reason comes from outside and can hold anything.
func (h *Handler) fail(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrConnectionNotFound):
		httpx.WriteError(w, r, http.StatusNotFound, httpx.CodeNotFound, "Not found.")
	case errors.Is(err, ErrConnectionFailed):
		h.logger.WarnContext(r.Context(), "storage connection failed",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusUnprocessableEntity, codeConnectionFailed,
			"This account couldn't be reached.")
	default:
		h.logger.ErrorContext(r.Context(), "storage request failed",
			"request_id", httpx.RequestID(r.Context()), "error", err)
		httpx.WriteError(w, r, http.StatusInternalServerError, httpx.CodeInternal, "Something went wrong.")
	}
}

func toAPIPool(p Pool) apigen.Pool {
	body := apigen.Pool{Accounts: make([]apigen.PoolAccount, 0, len(p.Accounts)), UsedBytes: p.Used}
	if !p.Unlimited {
		body.LimitBytes, body.FreeBytes = &p.Limit, &p.Free
	}
	for _, account := range p.Accounts {
		item := apigen.PoolAccount{
			Connection: toAPI(account.Connection),
			Available:  account.Available,
			UsedBytes:  account.Used,

			ReservedBytes: account.Reserved,
		}
		if account.Limit > 0 {
			limit := account.Limit
			item.LimitBytes = &limit
		}
		body.Accounts = append(body.Accounts, item)
	}
	return body
}

func toAPI(c Connection) apigen.Connection {
	return apigen.Connection{
		Id:        c.ID,
		Name:      c.Name,
		Provider:  apigen.ConnectionProvider(c.Provider),
		Summary:   c.Summary,
		CreatedAt: c.CreatedAt,
	}
}

// newSecret returns 256 random bits in a form safe for cookies and URLs.
func newSecret() string {
	secret := make([]byte, secretBytes)
	_, _ = rand.Read(secret) // crypto/rand never returns an error.
	return base64.RawURLEncoding.EncodeToString(secret)
}
