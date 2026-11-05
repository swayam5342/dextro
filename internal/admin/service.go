package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/storage"
)

const (
	minUploadBytes = 1 << 20
	maxUploadBytes = 5 << 40
)

// ErrInvalidSettings means a new value is out of range.
var ErrInvalidSettings = errors.New("invalid settings")

// Settings are the instance-wide options an admin can change.
type Settings struct {
	MaxUploadBytes int64
}

// Update is a change to Settings. Fields left nil stay as they are.
type Update struct {
	MaxUploadBytes *int64
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
}

// NewService returns a Service backed by store.
func NewService(store *Store, health AccountHealth) *Service {
	return &Service{store: store, health: health}
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
	return Settings{MaxUploadBytes: limit}, nil
}

// UpdateSettings saves new settings and records who changed them.
func (s *Service) UpdateSettings(ctx context.Context, actor uuid.UUID, ip string, next Update) (Settings, error) {
	if next.MaxUploadBytes != nil && (*next.MaxUploadBytes < minUploadBytes || *next.MaxUploadBytes > maxUploadBytes) {
		return Settings{}, ErrInvalidSettings
	}
	changes := map[string]any{}
	if next.MaxUploadBytes != nil {
		changes[settingMaxUploadBytes] = *next.MaxUploadBytes
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
