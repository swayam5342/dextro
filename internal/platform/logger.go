// Package platform holds the infrastructure shared by every feature.
package platform

import (
	"io"
	"log/slog"
)

// NewLogger returns a JSON logger writing to w at the given level.
func NewLogger(w io.Writer, level slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(w, &slog.HandlerOptions{Level: level}))
}
