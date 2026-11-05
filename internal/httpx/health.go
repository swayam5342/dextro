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

// api implements the generated server interface. As features arrive, it embeds
// their handlers.
type api struct {
	db     Pinger
	logger *slog.Logger
}

var _ apigen.ServerInterface = (*api)(nil)

func (a *api) GetHealth(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), healthTimeout)
	defer cancel()

	w.Header().Set("Cache-Control", "no-store")
	if err := a.db.Ping(ctx); err != nil {
		a.logger.ErrorContext(ctx, "health check: database ping failed",
			"request_id", RequestID(ctx), "error", err)
		writeJSON(w, http.StatusServiceUnavailable, apigen.Health{Status: apigen.Unavailable})
		return
	}
	writeJSON(w, http.StatusOK, apigen.Health{Status: apigen.Ok})
}
