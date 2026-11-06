// Command server runs Tributary. This file only wires the pieces together.
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

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/auth"
	"github.com/swayam5342/dextro/internal/config"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/files"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/jobs"
	"github.com/swayam5342/dextro/internal/platform"
	"github.com/swayam5342/dextro/internal/storage"
)

const shutdownTimeout = 15 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "tributary:", err)
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

	// Left nil when Google isn't configured, which turns its routes off.
	var google auth.GoogleClient
	if cfg.GoogleEnabled() {
		google = auth.NewGoogle(cfg.GoogleClientID, cfg.GoogleClientSecret, cfg.PublicURL+"/api/v1/auth/google/callback")
	}

	storageService, err := storage.FromConfig(cfg, pool)
	if err != nil {
		return err
	}
	filesService := files.NewService(files.NewStore(pool), storageService)
	settings := admin.NewStore(pool)
	runner, err := jobs.Start(ctx, jobs.Deps{
		Pool:     pool,
		Logger:   logger,
		Quotas:   storageService,
		Sessions: authService,
		Uploads:  filesService,
		Files:    filesService,
		Runs:     settings,
	})
	if err != nil {
		return err
	}

	adminService := admin.NewService(settings, storageService, runner)
	router := httpx.NewRouter(httpx.Deps{
		Logger:         logger,
		DB:             pool,
		PublicOrigin:   cfg.PublicURL,
		TrustedProxies: cfg.TrustedProxies,
		Sessions:       authService,
		APIKeys:        authService,
		Auth:           auth.NewHandler(authService, google, logger),
		Admin:          admin.NewHandler(adminService, logger),
		Storage:        storage.NewHandler(storageService, logger, syncNewAccount(filesService)),
		Files:          files.NewHandler(filesService, logger),
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
	serveErr := serve(ctx, server, logger)
	// ctx may already have ended, and the jobs still get time to finish.
	stopCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	return errors.Join(serveErr, runner.Stop(stopCtx))
}

// syncNewAccount lets the storage feature, which files imports, trigger a sync.
func syncNewAccount(service *files.Service) storage.AfterConnect {
	return func(ctx context.Context, userID, connectionID uuid.UUID, ip string) error {
		_, err := service.SyncConnection(ctx, userID, connectionID, ip)
		return err
	}
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
