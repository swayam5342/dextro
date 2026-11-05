package admin

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
)

// Settings are the instance-wide options an admin can change.
type Settings struct {
	AllowRegistration bool
}

// Service holds the rules for instance-wide settings. Only the admin route
// group calls it, so it does no access checks of its own.
type Service struct {
	store *Store
}

// NewService returns a Service backed by store.
func NewService(store *Store) *Service {
	return &Service{store: store}
}

// Settings returns the current settings.
func (s *Service) Settings(ctx context.Context) (Settings, error) {
	allowed, err := s.store.AllowRegistration(ctx)
	if err != nil {
		return Settings{}, err
	}
	return Settings{AllowRegistration: allowed}, nil
}

// UpdateSettings saves new settings and records who changed them.
func (s *Service) UpdateSettings(ctx context.Context, actor uuid.UUID, ip string, next Settings) (Settings, error) {
	encoded, err := json.Marshal(next.AllowRegistration)
	if err != nil {
		return Settings{}, fmt.Errorf("encode setting: %w", err)
	}
	err = s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.SetSetting(ctx, settingAllowRegistration, encoded); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  actor,
			Action:   audit.ActionSettingsUpdated,
			IP:       ip,
			Metadata: map[string]any{settingAllowRegistration: next.AllowRegistration},
		})
	})
	if err != nil {
		return Settings{}, err
	}
	return next, nil
}
