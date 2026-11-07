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

	"github.com/swayam5342/tributary/internal/admin"
	"github.com/swayam5342/tributary/internal/files"
	"github.com/swayam5342/tributary/internal/storage"
)

// Names of the jobs. These are stored, and the admin's status page knows them.
const (
	KindQuotaSync      = "quota_sync"
	KindSessionCleanup = "session_cleanup"
	KindUploadCleanup  = "upload_cleanup"
	KindFileSync       = "file_sync"
	KindFileTransfers  = "file_transfers"
	KindShareExpiry    = "share_expiry"
	KindFileCheck      = "file_check"
)

const (
	quotaSyncEvery      = time.Hour
	sessionCleanupEvery = 24 * time.Hour
	uploadCleanupEvery  = time.Hour
	// Often, because this is how a file deleted in Drive leaves the app. It's cheap:
	// a listing is one request per thousand files.
	fileSyncEvery = 5 * time.Minute
	// jobTimeout is for jobs that visit every account. A dead one can take a while to
	// give up on, and there may be many.
	jobTimeout  = 15 * time.Minute
	maxAttempts = 3
	maxWorkers  = 2
	// Often, because a public link that should have ended is the thing to avoid.
	shareExpiryEvery = time.Minute
	// Often, because someone is watching the Storage page. Finding nothing to do is a
	// couple of small queries.
	fileTransfersEvery = 30 * time.Second
	// One file can take hours. River won't rescue a job inside this time.
	fileTransfersTimeout = 12 * time.Hour
	// This only looks in: how often it really reads files is a server setting, and it decides
	// that itself. It downloads, so a run can take a while on a slow connection.
	fileCheckEvery   = time.Hour
	fileCheckTimeout = time.Hour
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
	ExpireShares(ctx context.Context) (files.ShareRun, error)
	CheckFiles(ctx context.Context, force bool) (files.CheckRun, error)
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

// InsertOpts puts moves on their own queue.
func (fileTransfersArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{Queue: queueTransfers}
}

type shareExpiryArgs struct{}

func (shareExpiryArgs) Kind() string { return KindShareExpiry }

// fileCheckArgs says to go now, for the admin's button, rather than wait for the schedule.
type fileCheckArgs struct {
	Force bool `json:"force"`
}

func (fileCheckArgs) Kind() string { return KindFileCheck }

type quotaSyncWorker struct {
	river.WorkerDefaults[quotaSyncArgs]
	deps Deps
}

func (w *quotaSyncWorker) Timeout(*river.Job[quotaSyncArgs]) time.Duration {
	return jobTimeout
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
	return jobTimeout
}

func (w *fileSyncWorker) Work(ctx context.Context, _ *river.Job[fileSyncArgs]) error {
	result, err := w.deps.Files.SyncAll(ctx)
	if err == nil {
		w.deps.Logger.InfoContext(ctx, "file sync finished", "added", result.Added, "removed", result.Removed, "skipped", result.Skipped)
		logFailures(ctx, w.deps, "file sync skipped an account", result.Failures)
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
	logFailures(ctx, w.deps, "file transfer failed", run.Failures)
	return finish(ctx, w.deps, KindFileTransfers, err)
}

type shareExpiryWorker struct {
	river.WorkerDefaults[shareExpiryArgs]
	deps Deps
}

func (w *shareExpiryWorker) Timeout(*river.Job[shareExpiryArgs]) time.Duration {
	return jobTimeout
}

func (w *shareExpiryWorker) Work(ctx context.Context, _ *river.Job[shareExpiryArgs]) error {
	run, err := w.deps.Files.ExpireShares(ctx)
	if run.Expired > 0 {
		w.deps.Logger.InfoContext(ctx, "google links expired", "removed", run.Expired)
	}
	logFailures(ctx, w.deps, "google link couldn't be removed", run.Failures)
	return finish(ctx, w.deps, KindShareExpiry, err)
}

type fileCheckWorker struct {
	river.WorkerDefaults[fileCheckArgs]
	deps Deps
}

func (w *fileCheckWorker) Timeout(*river.Job[fileCheckArgs]) time.Duration {
	return fileCheckTimeout
}

func (w *fileCheckWorker) Work(ctx context.Context, job *river.Job[fileCheckArgs]) error {
	run, err := w.deps.Files.CheckFiles(ctx, job.Args.Force)
	if run.Checked > 0 || run.Damaged > 0 || len(run.Failures) > 0 {
		w.deps.Logger.InfoContext(ctx, "file check ran", "checked", run.Checked, "damaged", run.Damaged, "failed", len(run.Failures))
	}
	logFailures(ctx, w.deps, "file couldn't be checked", run.Failures)
	return finish(ctx, w.deps, KindFileCheck, err)
}

func logFailures(ctx context.Context, d Deps, message string, failures []error) {
	for _, failure := range failures {
		d.Logger.WarnContext(ctx, message, "error", failure)
	}
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
	river.AddWorker(workers, &shareExpiryWorker{deps: d})
	river.AddWorker(workers, &fileCheckWorker{deps: d})
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
			every(shareExpiryEvery, shareExpiryArgs{}),
			every(fileTransfersEvery, fileTransfersArgs{}),
			every(fileCheckEvery, fileCheckArgs{}),
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

// RunNow starts a job straight away, alongside its schedule. A job that's already running
// runs twice, which every job here can take.
func (r *Runner) RunNow(ctx context.Context, name string) error {
	for _, args := range []river.JobArgs{quotaSyncArgs{}, sessionCleanupArgs{}, uploadCleanupArgs{}, fileSyncArgs{}, fileTransfersArgs{}, shareExpiryArgs{}, fileCheckArgs{Force: true}} {
		if args.Kind() == name {
			if _, err := r.client.Insert(ctx, args, &river.InsertOpts{MaxAttempts: maxAttempts}); err != nil {
				return fmt.Errorf("start job %s: %w", name, err)
			}
			return nil
		}
	}
	return admin.ErrUnknownJob
}

// Stop lets running jobs finish, up to ctx's deadline.
func (r *Runner) Stop(ctx context.Context) error {
	if err := r.client.Stop(ctx); err != nil {
		return fmt.Errorf("stop river: %w", err)
	}
	return nil
}
