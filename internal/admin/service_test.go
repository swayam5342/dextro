package admin

import (
	"context"
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

	t.Run("a change is saved and audited", func(t *testing.T) {
		got, err := service.UpdateSettings(ctx, actor, "203.0.113.7", Settings{AllowRegistration: false})
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
