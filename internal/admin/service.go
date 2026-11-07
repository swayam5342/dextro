package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/audit"
	"github.com/swayam5342/tributary/internal/storage"
)

const (
	minUploadBytes = 1 << 20
	maxUploadBytes = 5 << 40
)

// The most a file check can be set to: a month between runs, and about a terabyte a run.
const (
	maxFileCheckHours = 720
	maxFileCheckMB    = 1_000_000
)

// ErrInvalidSettings means a new value is out of range.
var ErrInvalidSettings = errors.New("invalid settings")

// Settings are the instance-wide options an admin can change.
type Settings struct {
	MaxUploadBytes int64
	GoogleLinks    bool
	FileChecks     bool
	FileCheckHours int
	FileCheckMB    int
}

// Update is a change to Settings. Fields left nil stay as they are.
type Update struct {
	MaxUploadBytes *int64
	GoogleLinks    *bool
	FileChecks     *bool
	FileCheckHours *int
	FileCheckMB    *int
}

// ErrUnknownJob means there's no background job by that name.
var ErrUnknownJob = errors.New("unknown job")

// JobRunner starts background jobs. It's an interface because the jobs package imports
// the features, and they import this one.
type JobRunner interface {
	RunNow(ctx context.Context, name string) error
}

// AccountHealth is what the status page needs from the storage feature.
type AccountHealth interface {
	Health(ctx context.Context) (storage.Health, error)
}

// Service holds the rules for instance-wide settings and maintenance. Only the
// admin route group calls it, so it does no access checks of its own.
type Service struct {
	store  *Store
	health AccountHealth
	jobs   JobRunner
}

// NewService returns a Service backed by store.
func NewService(store *Store, health AccountHealth, jobs JobRunner) *Service {
	return &Service{store: store, health: health, jobs: jobs}
}

// RunJob starts a background job now, and records who asked.
func (s *Service) RunJob(ctx context.Context, actor uuid.UUID, ip, name string) error {
	if err := s.jobs.RunNow(ctx, name); err != nil {
		return err
	}
	return s.store.Audit().Record(ctx, audit.Event{
		ActorID:  actor,
		Action:   audit.ActionJobStarted,
		IP:       ip,
		Metadata: map[string]any{"job": name},
	})
}

// Settings returns the current settings.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	return readSettings(ctx, s.store)
}

func readSettings(ctx context.Context, store *Store) (Settings, error) {
	limit, err := store.MaxUploadBytes(ctx)
	if err != nil {
		return Settings{}, err
	}
	links, err := store.GoogleLinksEnabled(ctx)
	if err != nil {
		return Settings{}, err
	}
	checks, err := store.FileChecksEnabled(ctx)
	if err != nil {
		return Settings{}, err
	}
	hours, err := store.FileCheckHours(ctx)
	if err != nil {
		return Settings{}, err
	}
	megabytes, err := store.FileCheckMB(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{MaxUploadBytes: limit, GoogleLinks: links, FileChecks: checks, FileCheckHours: hours, FileCheckMB: megabytes}, nil
}

// UpdateSettings saves new settings and records who changed them.
func (s *Service) UpdateSettings(ctx context.Context, actor uuid.UUID, ip string, next Update) (Settings, error) {
	if next.MaxUploadBytes != nil && (*next.MaxUploadBytes < minUploadBytes || *next.MaxUploadBytes > maxUploadBytes) {
		return Settings{}, ErrInvalidSettings
	}
	if next.FileCheckHours != nil && (*next.FileCheckHours < 1 || *next.FileCheckHours > maxFileCheckHours) {
		return Settings{}, ErrInvalidSettings
	}
	if next.FileCheckMB != nil && (*next.FileCheckMB < 1 || *next.FileCheckMB > maxFileCheckMB) {
		return Settings{}, ErrInvalidSettings
	}
	changes := map[string]any{}
	if next.MaxUploadBytes != nil {
		changes[settingMaxUploadBytes] = *next.MaxUploadBytes
	}
	if next.GoogleLinks != nil {
		changes[settingGoogleLinks] = *next.GoogleLinks
	}
	if next.FileChecks != nil {
		changes[settingFileChecks] = *next.FileChecks
	}
	if next.FileCheckHours != nil {
		changes[settingFileCheckHours] = *next.FileCheckHours
	}
	if next.FileCheckMB != nil {
		changes[settingFileCheckMB] = *next.FileCheckMB
	}

	var saved Settings
	err := s.store.InTx(ctx, func(tx *Store) error {
		for key, value := range changes {
			encoded, err := json.Marshal(value)
			if err != nil {
				return fmt.Errorf("encode setting: %w", err)
			}
			if err := tx.SetSetting(ctx, key, encoded); err != nil {
				return err
			}
		}
		if err := tx.Audit().Record(ctx, audit.Event{
			ActorID:  actor,
			Action:   audit.ActionSettingsUpdated,
			IP:       ip,
			Metadata: changes,
		}); err != nil {
			return err
		}
		var err error
		saved, err = readSettings(ctx, tx)
		return err
	})
	if err != nil {
		return Settings{}, err
	}
	return saved, nil
}
