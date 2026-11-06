package storage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
	"github.com/swayam5342/tributary/internal/provider"
	"github.com/swayam5342/tributary/internal/provider/providertest"
)

// slowQuota is an account that takes its time, so a test can cut it off.
type slowQuota struct {
	*providertest.Fake
	cancel context.CancelFunc
}

func (s slowQuota) Quota(ctx context.Context) (provider.Quota, error) {
	s.cancel()
	<-ctx.Done()
	return provider.Quota{}, ctx.Err()
}

func TestQuotaSync(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	reset := func(t *testing.T) *fixture {
		t.Helper()
		if _, err := pool.Exec(ctx, `TRUNCATE users, audit_events CASCADE`); err != nil {
			t.Fatalf("reset database: %v", err)
		}
		return newFixture(t, pool)
	}

	t.Run("it covers every user's accounts and saves what it saw", func(t *testing.T) {
		f := reset(t)
		f.linked(t, f.user(t), "first@gmail.com", 100, 30)
		f.linked(t, f.user(t), "second@gmail.com", 100, 12)

		result, err := f.service.SyncQuotas(ctx)
		if err != nil {
			t.Fatalf("SyncQuotas: %v", err)
		}
		if result.Checked != 2 || result.Unreachable != 0 {
			t.Errorf("result = %+v", result)
		}
		health, err := f.service.Health(ctx)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if health.Accounts != 2 || health.UsedBytes != 42 || len(health.Unreachable) != 0 {
			t.Errorf("health = %+v", health)
		}
	})

	t.Run("an unreachable account is reported with its owner, and others carry on", func(t *testing.T) {
		f := reset(t)
		owner := f.user(t)
		f.linked(t, owner, "broken@gmail.com", 100, 30)
		f.linked(t, f.user(t), "fine@gmail.com", 100, 12)
		f.account("broken@gmail.com").QuotaErr = errors.New("token expired")

		result, err := f.service.SyncQuotas(ctx)
		if err != nil {
			t.Fatalf("SyncQuotas: %v", err)
		}
		if result.Checked != 2 || result.Unreachable != 1 {
			t.Errorf("result = %+v", result)
		}
		health, err := f.service.Health(ctx)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if len(health.Unreachable) != 1 {
			t.Fatalf("unreachable = %+v", health.Unreachable)
		}
		got := health.Unreachable[0]
		if got.Summary != "broken@gmail.com" || got.OwnerEmail == "" || time.Since(got.CheckedAt) > time.Minute {
			t.Errorf("unreachable = %+v", got)
		}

		f.account("broken@gmail.com").QuotaErr = nil
		if _, err := f.service.SyncQuotas(ctx); err != nil {
			t.Fatalf("SyncQuotas: %v", err)
		}
		health, err = f.service.Health(ctx)
		if err != nil {
			t.Fatalf("Health: %v", err)
		}
		if len(health.Unreachable) != 0 {
			t.Errorf("a repaired account is still listed: %+v", health.Unreachable)
		}
	})

	t.Run("more accounts than one batch are all checked", func(t *testing.T) {
		f := reset(t)
		owner := f.user(t)
		for i := range syncBatch + 3 {
			f.linked(t, owner, "a"+string(rune('a'+i))+"@gmail.com", 100, 1)
		}
		result, err := f.service.SyncQuotas(ctx)
		if err != nil {
			t.Fatalf("SyncQuotas: %v", err)
		}
		if result.Checked != syncBatch+3 {
			t.Errorf("checked = %d", result.Checked)
		}
	})

	t.Run("a shutdown doesn't mark accounts as broken", func(t *testing.T) {
		f := reset(t)
		f.linked(t, f.user(t), "fine@gmail.com", 100, 12)
		syncCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		f.wrap = func(p provider.Provider) provider.Provider {
			return slowQuota{Fake: p.(*providertest.Fake), cancel: cancel}
		}
		if _, err := f.service.SyncQuotas(syncCtx); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
		if got := f.count(t, `SELECT count(*) FROM storage_connections WHERE check_ok = false`); got != 0 {
			t.Errorf("accounts marked broken = %d", got)
		}
	})
}
