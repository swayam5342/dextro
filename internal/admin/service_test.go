package admin

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
)

func TestRegistrationSetting(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	service := NewService(NewStore(pool))

	t.Run("registration is open until someone closes it", func(t *testing.T) {
		got, err := service.Settings(ctx)
		if err != nil {
			t.Fatalf("Settings: %v", err)
		}
		if !got.AllowRegistration {
			t.Error("AllowRegistration = false, want true")
		}
	})

	var actor uuid.UUID
	if err := pool.QueryRow(ctx, `INSERT INTO users (email) VALUES ('a@example.org') RETURNING id`).Scan(&actor); err != nil {
		t.Fatalf("create user: %v", err)
	}

	t.Run("the upload limit has a default", func(t *testing.T) {
		got, err := service.Settings(ctx)
		if err != nil {
			t.Fatalf("Settings: %v", err)
		}
		if got.MaxUploadBytes != 10<<30 {
			t.Errorf("MaxUploadBytes = %d, want 10 GiB", got.MaxUploadBytes)
		}
	})

	t.Run("changing one setting leaves the other alone", func(t *testing.T) {
		limit := int64(5 << 20)
		got, err := service.UpdateSettings(ctx, actor, "203.0.113.7", Update{MaxUploadBytes: &limit})
		if err != nil {
			t.Fatalf("UpdateSettings: %v", err)
		}
		if got.MaxUploadBytes != limit || !got.AllowRegistration {
			t.Errorf("settings = %+v", got)
		}
		stored, err := NewStore(pool).MaxUploadBytes(ctx)
		if err != nil {
			t.Fatalf("MaxUploadBytes: %v", err)
		}
		if stored != limit {
			t.Errorf("stored limit = %d", stored)
		}
	})

	t.Run("the upload limit must be sensible", func(t *testing.T) {
		for _, limit := range []int64{0, -1, 1<<20 - 1, 6 << 40} {
			if _, err := service.UpdateSettings(ctx, actor, "203.0.113.7", Update{MaxUploadBytes: &limit}); !errors.Is(err, ErrInvalidSettings) {
				t.Errorf("limit %d: error = %v, want ErrInvalidSettings", limit, err)
			}
		}
	})

	t.Run("a change is saved and audited", func(t *testing.T) {
		closed := false
		got, err := service.UpdateSettings(ctx, actor, "203.0.113.7", Update{AllowRegistration: &closed})
		if err != nil {
			t.Fatalf("UpdateSettings: %v", err)
		}
		if got.AllowRegistration {
			t.Error("returned AllowRegistration = true, want false")
		}
		allowed, err := NewStore(pool).AllowRegistration(ctx)
		if err != nil {
			t.Fatalf("AllowRegistration: %v", err)
		}
		if allowed {
			t.Error("stored AllowRegistration = true, want false")
		}

		var count int
		err = pool.QueryRow(ctx, `SELECT count(*) FROM audit_events
			WHERE action = 'settings.updated' AND actor_id = $1 AND ip = '203.0.113.7'
			AND metadata->>'allow_registration' = 'false'`, actor).Scan(&count)
		if err != nil {
			t.Fatalf("count events: %v", err)
		}
		if count != 1 {
			t.Errorf("audit events = %d, want 1", count)
		}
	})
}
