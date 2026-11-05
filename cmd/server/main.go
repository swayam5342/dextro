// Command server runs dextro. This file only wires the pieces together.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/auth"
	"github.com/swayam5342/dextro/internal/config"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/platform"
)

const shutdownTimeout = 15 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dextro:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	logger := platform.NewLogger(os.Stdout, cfg.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := platform.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}

	authService := auth.NewService(auth.NewStore(pool))
	if args := os.Args[1:]; len(args) > 0 {
		return auth.RunCommand(ctx, authService, args, os.Stdout)
	}

	// Left nil when Google isn't configured, which turns its routes off.
	var google auth.GoogleClient
	if cfg.GoogleEnabled() {
		google = auth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.PublicURL+"/api/v1/auth/google/callback")
	}
	router := httpx.NewRouter(httpx.Deps{
		Logger:         logger,
		DB:             pool,
		PublicOrigin:   cfg.PublicURL,
		TrustedProxies: cfg.TrustedProxies,
		Sessions:       authService,
		Auth:           auth.NewHandler(authService, google, logger),
		Admin:          admin.NewHandler(admin.NewService(admin.NewStore(pool)), logger),
	})

	// There is no WriteTimeout or ReadTimeout because uploads and downloads can
	// legitimately run for hours.
	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          slog.NewLogLogger(logger.Handler(), slog.LevelWarn),
	}
	return serve(ctx, server, logger)
}

// serve runs the server until ctx ends, then drains in-flight requests.
func serve(ctx context.Context, server *http.Server, logger *slog.Logger) error {
	listenErr := make(chan error, 1)
	go func() {
		logger.Info("server listening", "addr", server.Addr)
		listenErr <- server.ListenAndServe()
	}()

	select {
	case err := <-listenErr:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	if err := <-listenErr; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("listen: %w", err)
	}
	return nil
}
