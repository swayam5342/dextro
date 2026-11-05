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

// Upgrades an instance from before S3 was removed. Its S3 connections have to go,
// along with their file records, and Drive connections must not be touched.
func TestDriveOnlyMigration(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	provider, err := newProvider(pool)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	defer func() { _ = provider.Close() }()
	if _, err := provider.UpTo(ctx, 4); err != nil {
		t.Fatalf("migrate to version 4: %v", err)
	}

	_, err = pool.Exec(ctx, `
		INSERT INTO users (id, email) VALUES ('00000000-0000-0000-0000-000000000001', 'a@example.org');
		INSERT INTO storage_connections (id, user_id, provider, name, settings, secret_key_id, secret_ciphertext) VALUES
			('00000000-0000-0000-0000-000000000002', '00000000-0000-0000-0000-000000000001', 's3', 'Bucket', '{}', 'k1', '\x00'),
			('00000000-0000-0000-0000-000000000003', '00000000-0000-0000-0000-000000000001', 'google_drive', 'Drive', '{}', 'k1', '\x00');
		INSERT INTO files (id, user_id, connection_id, object_id, name, size, content_type) VALUES
			('00000000-0000-0000-0000-000000000004', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000002', 'o1', 'in-s3.txt', 1, 'text/plain'),
			('00000000-0000-0000-0000-000000000005', '00000000-0000-0000-0000-000000000001', '00000000-0000-0000-0000-000000000003', 'o2', 'in-drive.txt', 1, 'text/plain');`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var connections, files int
	if err := pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM storage_connections), (SELECT count(*) FROM files)`).Scan(&connections, &files); err != nil {
		t.Fatalf("count: %v", err)
	}
	if connections != 1 || files != 1 {
		t.Errorf("connections = %d, files = %d, want 1 of each", connections, files)
	}
	var name string
	if err := pool.QueryRow(ctx, `SELECT name FROM files`).Scan(&name); err != nil {
		t.Fatalf("read file: %v", err)
	}
	if name != "in-drive.txt" {
		t.Errorf("kept %q, want the Drive file", name)
	}
	_, err = pool.Exec(ctx, `INSERT INTO storage_connections (id, user_id, provider, name, settings, secret_key_id, secret_ciphertext)
		VALUES ('00000000-0000-0000-0000-000000000006', '00000000-0000-0000-0000-000000000001', 's3', 'Again', '{}', 'k1', '\x00')`)
	if err == nil {
		t.Error("a new S3 connection was accepted")
	}
}

// An instance from before there could only be one admin keeps its oldest.
func TestOneAdminMigration(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	provider, err := newProvider(pool)
	if err != nil {
		t.Fatalf("newProvider: %v", err)
	}
	defer func() { _ = provider.Close() }()
	if _, err := provider.UpTo(ctx, 5); err != nil {
		t.Fatalf("migrate to version 5: %v", err)
	}
	_, err = pool.Exec(ctx, `
		INSERT INTO users (email, role, created_at) VALUES
			('second@example.org', 'admin', '2026-02-01'),
			('first@example.org', 'admin', '2026-01-01'),
			('person@example.org', 'user', '2025-01-01');
		INSERT INTO settings (key, value) VALUES
			('allow_registration', 'false'),
			('max_upload_bytes', '5242880');`)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	var admin string
	if err := pool.QueryRow(ctx, `SELECT email FROM users WHERE role = 'admin'`).Scan(&admin); err != nil {
		t.Fatalf("find the admin: %v", err)
	}
	if admin != "first@example.org" {
		t.Errorf("admin = %s, want the oldest", admin)
	}
	var demoted, closed, limit int
	err = pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM audit_events WHERE action = 'user.role_changed' AND ip = 'migration' AND metadata->>'email' = 'second@example.org'),
		(SELECT count(*) FROM settings WHERE key = 'allow_registration'),
		(SELECT count(*) FROM settings WHERE key = 'max_upload_bytes')`).Scan(&demoted, &closed, &limit)
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if demoted != 1 || closed != 0 || limit != 1 {
		t.Errorf("demotions recorded = %d, registration settings = %d, upload limits = %d", demoted, closed, limit)
	}
}
