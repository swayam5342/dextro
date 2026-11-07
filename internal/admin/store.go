// Package admin holds instance-wide settings and maintenance.
package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/swayam5342/tributary/internal/audit"
	"github.com/swayam5342/tributary/internal/db/dbgen"
)

// ErrSettingNotFound means no value has been stored for the key.
var ErrSettingNotFound = errors.New("setting not found")

const (
	settingMaxUploadBytes = "max_upload_bytes"
	settingGoogleLinks    = "google_links_enabled"
	settingFileChecks     = "file_checks_enabled"
	settingFileCheckHours = "file_check_every_hours"
	settingFileCheckMB    = "file_check_limit_mb"
	settingFileCheckRun   = "file_check_last_run"
	// A little, now and then: a full read of everything isn't worth anyone's internet allowance.
	defaultFileCheckHours = 6
	defaultFileCheckMB    = 200
	defaultMaxUploadBytes = 10 << 30
)

// database is a pool or a transaction.
type database interface {
	dbgen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// Store reads and writes instance-wide settings. These belong to the instance
// rather than to a user, so its functions take no user ID; access control is
// the job of the admin route group.
type Store struct {
	db      database
	queries *dbgen.Queries
}

// NewStore returns a Store that runs its queries on db.
func NewStore(db database) *Store {
	return &Store{db: db, queries: dbgen.New(db)}
}

// InTx runs fn with a Store bound to one transaction.
func (s *Store) InTx(ctx context.Context, fn func(tx *Store) error) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		return fn(NewStore(tx))
	})
}

// Audit returns the audit log on the same connection as this store.
func (s *Store) Audit() *audit.Store {
	return audit.NewStore(s.db)
}

// GetSetting returns the JSON value stored for key.
func (s *Store) GetSetting(ctx context.Context, key string) (json.RawMessage, error) {
	row, err := s.queries.GetSetting(ctx, key)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrSettingNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get setting %q: %w", key, err)
	}
	return row.Value, nil
}

// SetSetting creates or replaces the JSON value stored for key.
func (s *Store) SetSetting(ctx context.Context, key string, value json.RawMessage) error {
	err := s.queries.UpsertSetting(ctx, dbgen.UpsertSettingParams{Key: key, Value: value})
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// readSetting returns a setting, or fallback if nobody has set it.
func readSetting[T any](ctx context.Context, s *Store, key string, fallback T) (T, error) {
	raw, err := s.GetSetting(ctx, key)
	if errors.Is(err, ErrSettingNotFound) {
		return fallback, nil
	}
	if err != nil {
		return fallback, err
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return fallback, fmt.Errorf("decode setting %q: %w", key, err)
	}
	return value, nil
}

// MaxUploadBytes returns the largest file anyone can upload.
func (s *Store) MaxUploadBytes(ctx context.Context) (int64, error) {
	return readSetting[int64](ctx, s, settingMaxUploadBytes, defaultMaxUploadBytes)
}

// GoogleLinksEnabled says whether people can make public links. It's off until the
// admin says otherwise, because a public link is a decision for whoever runs the server.
func (s *Store) GoogleLinksEnabled(ctx context.Context) (bool, error) {
	return readSetting(ctx, s, settingGoogleLinks, false)
}

// FileChecksEnabled says whether files are read back in the background. It's on unless the
// admin says otherwise, because it costs download bandwidth, which a metered connection
// may not want to spend.
func (s *Store) FileChecksEnabled(ctx context.Context) (bool, error) {
	return readSetting(ctx, s, settingFileChecks, true)
}

// FileCheckHours says how long the file check waits between runs.
func (s *Store) FileCheckHours(ctx context.Context) (int, error) {
	return readSetting(ctx, s, settingFileCheckHours, defaultFileCheckHours)
}

// FileCheckMB says how much one run of the file check can read, in MiB.
func (s *Store) FileCheckMB(ctx context.Context) (int, error) {
	return readSetting(ctx, s, settingFileCheckMB, defaultFileCheckMB)
}

// LastFileCheck says when the file check last read anything. It's kept here because the job
// looks in at every startup, and a restart mustn't count as the schedule coming round.
func (s *Store) LastFileCheck(ctx context.Context) (time.Time, error) {
	return readSetting(ctx, s, settingFileCheckRun, time.Time{})
}

// MarkFileCheck notes that the file check is starting.
func (s *Store) MarkFileCheck(ctx context.Context, at time.Time) error {
	encoded, err := json.Marshal(at)
	if err != nil {
		return fmt.Errorf("encode time: %w", err)
	}
	return s.SetSetting(ctx, settingFileCheckRun, encoded)
}

// JobRun is the last finish of a background job.
type JobRun struct {
	Name       string
	FinishedAt time.Time
	Succeeded  bool
}

// RecordJobRun saves how a background job just finished.
func (s *Store) RecordJobRun(ctx context.Context, name string, succeeded bool) error {
	err := s.queries.RecordJobRun(ctx, dbgen.RecordJobRunParams{Name: name, Succeeded: succeeded})
	if err != nil {
		return fmt.Errorf("record job run: %w", err)
	}
	return nil
}

// ListJobRuns returns the last finish of every job that has run.
func (s *Store) ListJobRuns(ctx context.Context) ([]JobRun, error) {
	rows, err := s.queries.ListJobRuns(ctx)
	if err != nil {
		return nil, fmt.Errorf("list job runs: %w", err)
	}
	runs := make([]JobRun, 0, len(rows))
	for _, row := range rows {
		runs = append(runs, JobRun{Name: row.Name, FinishedAt: row.FinishedAt, Succeeded: row.Succeeded})
	}
	return runs, nil
}
