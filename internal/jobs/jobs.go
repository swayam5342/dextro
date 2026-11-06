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

	"github.com/swayam5342/dextro/internal/files"
	"github.com/swayam5342/dextro/internal/storage"
)

// Names of the jobs. These are stored, and the admin's status page knows them.
const (
	KindQuotaSync      = "quota_sync"
	KindSessionCleanup = "session_cleanup"
	KindUploadCleanup  = "upload_cleanup"
	KindFileSync       = "file_sync"
	KindFileTransfers  = "file_transfers"
)

const (
	quotaSyncEvery      = time.Hour
	sessionCleanupEvery = 24 * time.Hour
	uploadCleanupEvery  = time.Hour
	// Often, because this is how a file deleted in Drive leaves the app. It's cheap:
	// a listing is one request per thousand files.
	fileSyncEvery = 5 * time.Minute
	// A dead account can take a while to give up on, and there may be many.
	quotaSyncTimeout = 15 * time.Minute
	maxAttempts      = 3
	maxWorkers       = 2
	// Often, because someone is watching the Storage page. Finding nothing to do is one
	// query.
	fileTransfersEvery = 30 * time.Second
	// One file can take hours. River won't rescue a job inside this time.
	fileTransfersTimeout = 12 * time.Hour
	// Its own queue, with one worker, so moves run one at a time and a long copy doesn't
	// hold up the other jobs. Runs aren't kept unique, because a crashed one would then
	// block the next until river rescued it. Runs that pile up behind a long one find
	// nothing to do.
	queueTransfers = "file_transfers"
)

// Quotas is what the quota sync needs from the storage feature.
type Quotas interface {
	SyncQuotas(ctx context.Context) (storage.SyncResult, error)
}

// Sessions is what the cleanup needs from the auth feature.
type Sessions interface {
	DeleteExpiredSessions(ctx context.Context) (int64, error)
}

// Uploads is what the upload cleanup needs from the files feature.
type Uploads interface {
	CleanupUploads(ctx context.Context) (int, error)
}

// Files is what the file sync needs from the files feature.
type Files interface {
	SyncAll(ctx context.Context) (files.SyncResult, error)
	RunTransfers(ctx context.Context) (files.TransferRun, error)
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
	Uploads  Uploads
	Files    Files
	Runs     Runs
}

type quotaSyncArgs struct{}

func (quotaSyncArgs) Kind() string { return KindQuotaSync }

type sessionCleanupArgs struct{}

func (sessionCleanupArgs) Kind() string { return KindSessionCleanup }

type uploadCleanupArgs struct{}

func (uploadCleanupArgs) Kind() string { return KindUploadCleanup }

type fileSyncArgs struct{}

func (fileSyncArgs) Kind() string { return KindFileSync }

type fileTransfersArgs struct{}

func (fileTransfersArgs) Kind() string { return KindFileTransfers }

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

type uploadCleanupWorker struct {
	river.WorkerDefaults[uploadCleanupArgs]
	deps Deps
}

func (w *uploadCleanupWorker) Work(ctx context.Context, _ *river.Job[uploadCleanupArgs]) error {
	removed, err := w.deps.Uploads.CleanupUploads(ctx)
	if err == nil {
		w.deps.Logger.InfoContext(ctx, "upload cleanup finished", "removed", removed)
	}
	return finish(ctx, w.deps, KindUploadCleanup, err)
}

type fileSyncWorker struct {
	river.WorkerDefaults[fileSyncArgs]
	deps Deps
}

func (w *fileSyncWorker) Timeout(*river.Job[fileSyncArgs]) time.Duration {
	return quotaSyncTimeout
}

func (w *fileSyncWorker) Work(ctx context.Context, _ *river.Job[fileSyncArgs]) error {
	result, err := w.deps.Files.SyncAll(ctx)
	if err == nil {
		w.deps.Logger.InfoContext(ctx, "file sync finished", "added", result.Added, "removed", result.Removed, "skipped", result.Skipped)
		for _, failure := range result.Failures {
			w.deps.Logger.WarnContext(ctx, "file sync skipped an account", "error", failure)
		}
	}
	return finish(ctx, w.deps, KindFileSync, err)
}

type fileTransfersWorker struct {
	river.WorkerDefaults[fileTransfersArgs]
	deps Deps
}

func (w *fileTransfersWorker) Timeout(*river.Job[fileTransfersArgs]) time.Duration {
	return fileTransfersTimeout
}

func (w *fileTransfersWorker) Work(ctx context.Context, _ *river.Job[fileTransfersArgs]) error {
	run, err := w.deps.Files.RunTransfers(ctx)
	if run.Moved > 0 || run.Failed > 0 || run.Removed > 0 {
		w.deps.Logger.InfoContext(ctx, "file transfers ran", "moved", run.Moved, "failed", run.Failed, "removed", run.Removed)
	}
	for _, failure := range run.Failures {
		w.deps.Logger.WarnContext(ctx, "file transfer failed", "error", failure)
	}
	return finish(ctx, w.deps, KindFileTransfers, err)
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
	river.AddWorker(workers, &uploadCleanupWorker{deps: d})
	river.AddWorker(workers, &fileSyncWorker{deps: d})
	river.AddWorker(workers, &fileTransfersWorker{deps: d})
	client, err := river.NewClient(driver, &river.Config{
		Logger: d.Logger,
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: maxWorkers},
			queueTransfers:     {MaxWorkers: 1},
		},
		Workers: workers,
		PeriodicJobs: []*river.PeriodicJob{
			every(quotaSyncEvery, quotaSyncArgs{}),
			every(sessionCleanupEvery, sessionCleanupArgs{}),
			every(uploadCleanupEvery, uploadCleanupArgs{}),
			every(fileSyncEvery, fileSyncArgs{}),
			river.NewPeriodicJob(
				river.PeriodicInterval(fileTransfersEvery),
				func() (river.JobArgs, *river.InsertOpts) {
					return fileTransfersArgs{}, &river.InsertOpts{MaxAttempts: maxAttempts, Queue: queueTransfers}
				},
				&river.PeriodicJobOpts{RunOnStart: true},
			),
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
