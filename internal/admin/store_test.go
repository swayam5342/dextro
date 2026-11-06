package admin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
)

func TestSettingsStore(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(pool)

	if _, err := store.GetSetting(ctx, "unset"); !errors.Is(err, ErrSettingNotFound) {
		t.Fatalf("GetSetting(unset) error = %v, want ErrSettingNotFound", err)
	}

	for _, value := range []string{`true`, `false`} {
		if err := store.SetSetting(ctx, "flag", json.RawMessage(value)); err != nil {
			t.Fatalf("SetSetting(%s): %v", value, err)
		}
		got, err := store.GetSetting(ctx, "flag")
		if err != nil {
			t.Fatalf("GetSetting: %v", err)
		}
		if string(got) != value {
			t.Errorf("GetSetting = %s, want %s", got, value)
		}
	}
}
