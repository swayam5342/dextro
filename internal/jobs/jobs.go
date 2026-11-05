// Package jobs runs the server's background work on river, which keeps its queue
// in Postgres.
package jobs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"

	"github.com/swayam5342/dextro/internal/storage"
)

// Names of the jobs. These are stored, and the admin's status page knows them.
const (
	KindQuotaSync      = "quota_sync"
	KindSessionCleanup = "session_cleanup"
)

const (
	quotaSyncEvery      = time.Hour
	sessionCleanupEvery = 24 * time.Hour
	// A dead account can take a while to give up on, and there may be many.
	quotaSyncTimeout = 15 * time.Minute
	maxAttempts      = 3
	maxWorkers       = 2
)

// Quotas is what the quota sync needs from the storage feature.
type Quotas interface {
	SyncQuotas(ctx context.Context) (storage.SyncResult, error)
}

// Sessions is what the cleanup needs from the auth feature.
type Sessions interface {
	DeleteExpiredSessions(ctx context.Context) (int64, error)
}

// Runs remembers when each job last finished.
type Runs interface {
	RecordJobRun(ctx context.Context, name string, succeeded bool) error
}

// Deps is everything the jobs need.
type Deps struct {
	Pool     *pgxpool.Pool
	Logger   *slog.Logger
	Quotas   Quotas
	Sessions Sessions
	Runs     Runs
}

type quotaSyncArgs struct{}

func (quotaSyncArgs) Kind() string { return KindQuotaSync }

type sessionCleanupArgs struct{}

func (sessionCleanupArgs) Kind() string { return KindSessionCleanup }

type quotaSyncWorker struct {
	river.WorkerDefaults[quotaSyncArgs]
	deps Deps
}

func (w *quotaSyncWorker) Timeout(*river.Job[quotaSyncArgs]) time.Duration {
	return quotaSyncTimeout
}

func (w *quotaSyncWorker) Work(ctx context.Context, _ *river.Job[quotaSyncArgs]) error {
	result, err := w.deps.Quotas.SyncQuotas(ctx)
	if err == nil {
		w.deps.Logger.InfoContext(ctx, "quota sync finished", "checked", result.Checked, "unreachable", result.Unreachable)
	}
	return finish(ctx, w.deps, KindQuotaSync, err)
}

type sessionCleanupWorker struct {
	river.WorkerDefaults[sessionCleanupArgs]
	deps Deps
}

func (w *sessionCleanupWorker) Work(ctx context.Context, _ *river.Job[sessionCleanupArgs]) error {
	deleted, err := w.deps.Sessions.DeleteExpiredSessions(ctx)
	if err == nil {
		w.deps.Logger.InfoContext(ctx, "session cleanup finished", "deleted", deleted)
	}
	return finish(ctx, w.deps, KindSessionCleanup, err)
}

// finish records the outcome. A job stopped by the server shutting down didn't
// fail, and river runs it again, so it isn't recorded.
func finish(ctx context.Context, d Deps, name string, jobErr error) error {
	if ctx.Err() != nil {
		return jobErr
	}
	if err := d.Runs.RecordJobRun(ctx, name, jobErr == nil); err != nil {
		return err
	}
	return jobErr
}

func every(interval time.Duration, args river.JobArgs) *river.PeriodicJob {
	return river.NewPeriodicJob(
		river.PeriodicInterval(interval),
		func() (river.JobArgs, *river.InsertOpts) {
			return args, &river.InsertOpts{MaxAttempts: maxAttempts}
		},
		&river.PeriodicJobOpts{RunOnStart: true},
	)
}

// Runner is the running background work.
type Runner struct {
	client *river.Client[pgx.Tx]
}

// Start applies river's own migrations and begins running jobs until ctx ends or
// Stop is called.
func Start(ctx context.Context, d Deps) (*Runner, error) {
	driver := riverpgxv5.New(d.Pool)
	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		return nil, fmt.Errorf("create river migrator: %w", err)
	}
	if _, err := migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return nil, fmt.Errorf("apply river migrations: %w", err)
	}

	workers := river.NewWorkers()
	river.AddWorker(workers, &quotaSyncWorker{deps: d})
	river.AddWorker(workers, &sessionCleanupWorker{deps: d})
	client, err := river.NewClient(driver, &river.Config{
		Logger:  d.Logger,
		Queues:  map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: maxWorkers}},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			every(quotaSyncEvery, quotaSyncArgs{}),
			every(sessionCleanupEvery, sessionCleanupArgs{}),
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create river client: %w", err)
	}
	if err := client.Start(ctx); err != nil {
		return nil, fmt.Errorf("start river: %w", err)
	}
	return &Runner{client: client}, nil
}

// Stop lets running jobs finish, up to ctx's deadline.
func (r *Runner) Stop(ctx context.Context) error {
	if err := r.client.Stop(ctx); err != nil {
		return fmt.Errorf("stop river: %w", err)
	}
	return nil
}
