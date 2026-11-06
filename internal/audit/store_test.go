package audit

import (
	"context"
	"testing"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
)

func TestRecord(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	err := NewStore(pool).Record(ctx, Event{
		Action:   ActionLoginFailed,
		IP:       "203.0.113.7",
		Metadata: map[string]any{"method": "password"},
	})
	if err != nil {
		t.Fatalf("Record: %v", err)
	}

	var action, ip, method string
	var hasActor bool
	row := pool.QueryRow(ctx, `SELECT action, ip, metadata->>'method', actor_id IS NOT NULL FROM audit_events`)
	if err := row.Scan(&action, &ip, &method, &hasActor); err != nil {
		t.Fatalf("read event: %v", err)
	}
	if action != ActionLoginFailed || ip != "203.0.113.7" || method != "password" || hasActor {
		t.Errorf("event = %q %q %q actor=%v", action, ip, method, hasActor)
	}
}
