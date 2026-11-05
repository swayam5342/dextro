package db

import (
	"context"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/swayam5342/dextro/internal/db/dbtest"
)

func tableExists(t *testing.T, pool *pgxpool.Pool, name string) bool {
	t.Helper()
	var exists bool
	err := pool.QueryRow(context.Background(), "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists)
	if err != nil {
		t.Fatalf("check table %s: %v", name, err)
	}
	return exists
}

func TestMigrations(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()

	t.Run("concurrent startups all succeed", func(t *testing.T) {
		const instances = 4
		errs := make([]error, instances)
		var wg sync.WaitGroup
		for i := range instances {
			wg.Go(func() { errs[i] = Migrate(ctx, pool) })
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Errorf("instance %d: %v", i, err)
			}
		}
		if !tableExists(t, pool, "settings") {
			t.Fatal("settings table missing after migrating")
		}
	})

	t.Run("running again changes nothing", func(t *testing.T) {
		if err := Migrate(ctx, pool); err != nil {
			t.Fatalf("Migrate: %v", err)
		}
	})

	t.Run("every migration can be reversed and reapplied", func(t *testing.T) {
		provider, err := newProvider(pool)
		if err != nil {
			t.Fatalf("newProvider: %v", err)
		}
		defer func() { _ = provider.Close() }()

		if _, err := provider.DownTo(ctx, 0); err != nil {
			t.Fatalf("DownTo: %v", err)
		}
		if tableExists(t, pool, "settings") {
			t.Fatal("settings table still present after migrating down")
		}
		if _, err := provider.Up(ctx); err != nil {
			t.Fatalf("Up: %v", err)
		}
		if !tableExists(t, pool, "settings") {
			t.Fatal("settings table missing after migrating back up")
		}
	})
}
