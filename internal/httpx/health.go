package httpx

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

const healthTimeout = 2 * time.Second

// Pinger reports whether a dependency is reachable.
type Pinger interface {
	Ping(ctx context.Context) error
}

// api implements the generated server interface by embedding each feature's
// handlers. Features depend on this package, so it names them by interface.
type api struct {
	AuthHandlers
	AdminHandlers
	StorageHandlers
	db     Pinger
	logger *slog.Logger
}

var _ apigen.ServerInterface = (*api)(nil)

func (a *api) GetHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	if err := a.db.Ping(ctx); err != nil {
		a.logger.ErrorContext(ctx, "health check: database ping failed",
			"request_id", RequestID(ctx), "error", err)
		WriteJSON(w, http.StatusServiceUnavailable, apigen.Health{Status: apigen.Unavailable})
		return
	}
	WriteJSON(w, http.StatusOK, apigen.Health{Status: apigen.Ok})
}
