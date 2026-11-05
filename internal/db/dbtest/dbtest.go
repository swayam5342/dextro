// Package dbtest starts a throwaway Postgres for tests.
package dbtest

import (
	"context"
	"os"
	"runtime"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Image matches the Postgres version in docker-compose.yml.
const Image = "postgres:18-alpine"

func init() {
	// Without DOCKER_HOST, testcontainers probes the Docker named pipe with
	// os.Stat on Windows. That fails while another process is using the pipe,
	// so test packages running in parallel randomly report that Docker is
	// missing. Naming the host skips the probe.
	if runtime.GOOS == "windows" && os.Getenv("DOCKER_HOST") == "" {
		_ = os.Setenv("DOCKER_HOST", "npipe:////./pipe/docker_engine")
	}
}

// Start runs a new, empty Postgres and returns a pool connected to it. Both are
// cleaned up when the test ends. Callers apply migrations themselves.
func Start(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx := context.Background()

	container, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("dextro_test"),
		postgres.WithUsername("dextro"),
		postgres.WithPassword("test-only-password"),
		postgres.BasicWaitStrategies(),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start postgres: %v", err)
	}

	url, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("postgres connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, url)
	if err != nil {
		t.Fatalf("connect to postgres: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}
