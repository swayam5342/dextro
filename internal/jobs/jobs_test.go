package jobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/files"
	"github.com/swayam5342/dextro/internal/storage"
)

type fakeQuotas struct {
	calls atomic.Int32
	err   error
}

func (f *fakeQuotas) SyncQuotas(context.Context) (storage.SyncResult, error) {
	f.calls.Add(1)
	return storage.SyncResult{}, f.err
}

type fakeSessions struct{ calls atomic.Int32 }

func (f *fakeSessions) DeleteExpiredSessions(context.Context) (int64, error) {
	f.calls.Add(1)
	return 0, nil
}

type fakeUploads struct{ calls atomic.Int32 }

func (f *fakeUploads) CleanupUploads(context.Context) (int, error) {
	f.calls.Add(1)
	return 0, nil
}

type fakeFiles struct {
	calls     atomic.Int32
	transfers atomic.Int32
}

func (f *fakeFiles) SyncAll(context.Context) (files.SyncResult, error) {
	f.calls.Add(1)
	return files.SyncResult{}, nil
}

func (f *fakeFiles) RunTransfers(context.Context) (files.TransferRun, error) {
	f.transfers.Add(1)
	return files.TransferRun{}, nil
}

// Runs real river against real Postgres, so it proves the wiring: migrations,
// the schedule and the workers.
func TestJobsRunAndRecordTheirOutcome(t *testing.T) {
	pool := dbtest.Start(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := admin.NewStore(pool)
	quotas := &fakeQuotas{err: errors.New("the database went away")}
	sessions := &fakeSessions{}
	uploads := &fakeUploads{}
	synced := &fakeFiles{}
	runner, err := Start(ctx, Deps{
		Pool:     pool,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		Quotas:   quotas,
		Sessions: sessions,
		Uploads:  uploads,
		Files:    synced,
		Runs:     store,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		stopCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if err := runner.Stop(stopCtx); err != nil {
			t.Errorf("Stop: %v", err)
		}
	}()

	deadline := time.Now().Add(30 * time.Second)
	for {
		runs, err := store.ListJobRuns(ctx)
		if err != nil {
			t.Fatalf("ListJobRuns: %v", err)
		}
		if len(runs) == 5 {
			if runs[0].Name != KindFileSync || !runs[0].Succeeded {
				t.Errorf("file sync = %+v", runs[0])
			}
			if runs[1].Name != KindFileTransfers || !runs[1].Succeeded {
				t.Errorf("file transfers = %+v", runs[1])
			}
			if runs[2].Name != KindQuotaSync || runs[2].Succeeded {
				t.Errorf("a failed job should be recorded as failed: %+v", runs[2])
			}
			if runs[3].Name != KindSessionCleanup || !runs[3].Succeeded {
				t.Errorf("cleanup = %+v", runs[3])
			}
			if runs[4].Name != KindUploadCleanup || !runs[4].Succeeded {
				t.Errorf("upload cleanup = %+v", runs[4])
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("runs = %+v after 30 seconds", runs)
		}
		time.Sleep(100 * time.Millisecond)
	}
	if quotas.calls.Load() == 0 || sessions.calls.Load() == 0 || uploads.calls.Load() == 0 {
		t.Errorf("calls: quotas %d, sessions %d, uploads %d", quotas.calls.Load(), sessions.calls.Load(), uploads.calls.Load())
	}
}

type fakeRuns struct{ calls int }

func (f *fakeRuns) RecordJobRun(context.Context, string, bool) error {
	f.calls++
	return nil
}

func TestAJobStoppedByShutdownIsNotRecorded(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runs := &fakeRuns{}
	if err := finish(ctx, Deps{Runs: runs}, KindQuotaSync, context.Canceled); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v", err)
	}
	if runs.calls != 0 {
		t.Errorf("recorded %d runs, want 0", runs.calls)
	}
}
