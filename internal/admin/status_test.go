package admin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
	"github.com/swayam5342/dextro/internal/storage"
)

type fakeHealth struct {
	health storage.Health
	err    error
}

func (f fakeHealth) Health(context.Context) (storage.Health, error) { return f.health, f.err }

func TestStatus(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(pool)
	health := storage.Health{Accounts: 3, UsedBytes: 42}
	service := NewService(store, fakeHealth{health: health}, nil)

	t.Run("a job that hasn't run isn't listed", func(t *testing.T) {
		got, err := service.Status(ctx)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if len(got.Jobs) != 0 || got.Health.Accounts != 3 {
			t.Errorf("status = %+v", got)
		}
	})

	t.Run("only the latest run of a job is kept", func(t *testing.T) {
		for _, succeeded := range []bool{true, false} {
			if err := store.RecordJobRun(ctx, "quota_sync", succeeded); err != nil {
				t.Fatalf("RecordJobRun: %v", err)
			}
		}
		got, err := service.Status(ctx)
		if err != nil {
			t.Fatalf("Status: %v", err)
		}
		if len(got.Jobs) != 1 || got.Jobs[0].Succeeded {
			t.Errorf("jobs = %+v", got.Jobs)
		}
	})

	t.Run("a failure reading account health is an error", func(t *testing.T) {
		failing := NewService(store, fakeHealth{err: errors.New("no")}, nil)
		if _, err := failing.Status(ctx); err == nil {
			t.Error("error = nil")
		}
	})
}

func TestAuditLog(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(pool)
	service := NewService(store, nil, nil)

	if _, err := pool.Exec(ctx, `INSERT INTO users (id, email) VALUES ('00000000-0000-0000-0000-000000000001', 'actor@example.org')`); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	actor := "00000000-0000-0000-0000-000000000001"
	for i := range auditPageSize + 5 {
		_, err := pool.Exec(ctx, `INSERT INTO audit_events (actor_id, action, ip, metadata) VALUES ($1, 'auth.login', '203.0.113.7', $2)`,
			actor, fmt.Sprintf(`{"n": %d}`, i))
		if err != nil {
			t.Fatalf("seed event: %v", err)
		}
	}
	if err := store.Audit().Record(ctx, audit.Event{Action: audit.ActionRoleChanged, IP: "cli"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	t.Run("pages walk the whole log once, newest first", func(t *testing.T) {
		first, err := service.AuditLog(ctx, audit.Filter{})
		if err != nil {
			t.Fatalf("AuditLog: %v", err)
		}
		if len(first.Events) != auditPageSize || first.NextBefore == 0 {
			t.Fatalf("first page: %d events, next %d", len(first.Events), first.NextBefore)
		}
		if first.Events[0].Action != audit.ActionRoleChanged || first.Events[0].ActorEmail != nil {
			t.Errorf("newest = %+v", first.Events[0])
		}
		if got := first.Events[1].ActorEmail; got == nil || *got != "actor@example.org" {
			t.Errorf("actor = %v", got)
		}
		second, err := service.AuditLog(ctx, audit.Filter{Before: first.NextBefore})
		if err != nil {
			t.Fatalf("AuditLog: %v", err)
		}
		if len(second.Events) != 6 || second.NextBefore != 0 {
			t.Errorf("second page: %d events, next %d", len(second.Events), second.NextBefore)
		}
		if second.Events[0].ID >= first.NextBefore {
			t.Errorf("pages overlap: %d, %d", second.Events[0].ID, first.NextBefore)
		}
	})

	t.Run("a filter keeps only that action", func(t *testing.T) {
		page, err := service.AuditLog(ctx, audit.Filter{Action: audit.ActionRoleChanged})
		if err != nil {
			t.Fatalf("AuditLog: %v", err)
		}
		if len(page.Events) != 1 || page.NextBefore != 0 {
			t.Errorf("page = %+v", page)
		}
	})

	t.Run("a full last page has no next page", func(t *testing.T) {
		if _, err := pool.Exec(ctx, `DELETE FROM audit_events WHERE id NOT IN (SELECT id FROM audit_events ORDER BY id LIMIT $1)`, auditPageSize); err != nil {
			t.Fatalf("trim: %v", err)
		}
		page, err := service.AuditLog(ctx, audit.Filter{})
		if err != nil {
			t.Fatalf("AuditLog: %v", err)
		}
		if len(page.Events) != auditPageSize || page.NextBefore != 0 {
			t.Errorf("%d events, next %d", len(page.Events), page.NextBefore)
		}
	})
}

// adminOnly stands in for the session lookup, so these tests reach the handlers.
type adminOnly struct{}

func (adminOnly) ResolveSession(context.Context, string) (httpx.Principal, error) {
	return httpx.Principal{IsAdmin: true}, nil
}

// fakeJobs knows one job.
type fakeJobs struct{ started *[]string }

func (f fakeJobs) RunNow(_ context.Context, name string) error {
	if name != "file_sync" {
		return ErrUnknownJob
	}
	*f.started = append(*f.started, name)
	return nil
}

func TestRunJob(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(pool)
	var started []string
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpx.NewRouter(httpx.Deps{
		Logger:       logger,
		DB:           pool,
		PublicOrigin: "https://files.example.org",
		Sessions:     adminOnly{},
		Auth:         apigen.Unimplemented{},
		Admin:        NewHandler(NewService(store, fakeHealth{}, fakeJobs{&started}), logger),
		Storage:      apigen.Unimplemented{},
		Files:        apigen.Unimplemented{},
	})
	post := func(name string) int {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/jobs/"+name+"/run", nil)
		req.Header.Set("Origin", "https://files.example.org")
		req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: "any"})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := post("file_sync"); got != http.StatusAccepted {
		t.Errorf("status = %d", got)
	}
	if len(started) != 1 {
		t.Errorf("started = %v", started)
	}
	var events int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'job.started' AND metadata->>'job' = 'file_sync'`).Scan(&events); err != nil {
		t.Fatalf("count: %v", err)
	}
	if events != 1 {
		t.Errorf("%d audit events", events)
	}
	if got := post("nonsense"); got != http.StatusNotFound {
		t.Errorf("unknown job: status = %d", got)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_events WHERE action = 'job.started'`).Scan(&events); err != nil {
		t.Fatalf("count: %v", err)
	}
	if events != 1 {
		t.Errorf("%d audit events, a job that didn't start isn't one", events)
	}
}

func TestAuditHandler(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	store := NewStore(pool)
	if err := store.Audit().Record(ctx, audit.Event{Action: audit.ActionLogin, IP: "203.0.113.7", Metadata: map[string]any{"method": "password"}}); err != nil {
		t.Fatalf("Record: %v", err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpx.NewRouter(httpx.Deps{
		Logger:       logger,
		DB:           pool,
		PublicOrigin: "https://files.example.org",
		Sessions:     adminOnly{},
		Auth:         apigen.Unimplemented{},
		Admin:        NewHandler(NewService(store, fakeHealth{}, nil), logger),
		Storage:      apigen.Unimplemented{},
		Files:        apigen.Unimplemented{},
	})
	get := func(target string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: "any"})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	rec := get("/api/v1/admin/audit")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", rec.Code, rec.Body)
	}
	var page apigen.AuditPage
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(page.Events) != 1 || page.Events[0].Metadata["method"] != "password" || page.NextBefore != nil {
		t.Errorf("page = %+v", page)
	}
	if rec := get("/api/v1/admin/audit?before=abc"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad before: status = %d", rec.Code)
	}
	if rec := get("/api/v1/admin/status"); rec.Code != http.StatusOK {
		t.Errorf("status page: status = %d, body %s", rec.Code, rec.Body)
	}
}
