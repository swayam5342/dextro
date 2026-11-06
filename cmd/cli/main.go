// Command dextro-cli is the admin's command line. It's wiring only: the commands are in
// internal/cli.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/auth"
	"github.com/swayam5342/dextro/internal/cli"
	"github.com/swayam5342/dextro/internal/config"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/files"
	"github.com/swayam5342/dextro/internal/platform"
	"github.com/swayam5342/dextro/internal/storage"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dextro-cli:", err)
		os.Exit(1)
	}
}

func run(args []string, out io.Writer) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Asking what the commands are shouldn't need a server to be set up.
	if cli.NeedsNoServer(args) {
		return cli.Run(ctx, cli.Deps{}, args, out)
	}
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("invalid configuration:\n%w", err)
	}
	pool, err := platform.NewPool(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	storageService, err := storage.FromConfig(cfg, pool)
	if err != nil {
		return err
	}
	return cli.Run(ctx, cli.Deps{
		Auth:  auth.NewService(auth.NewStore(pool)),
		Admin: admin.NewService(admin.NewStore(pool), storageService),
		Files: files.NewService(files.NewStore(pool), storageService),
	}, args, out)
}
