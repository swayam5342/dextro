package admin

import (
	"context"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/storage"
)

// auditPageSize is how many events one page of the log shows.
const auditPageSize = 50

// Status is how the server's background work is doing.
type Status struct {
	Jobs   []JobRun
	Health storage.Health
}

// Status reports on the background jobs and on linked accounts across the server.
func (s *Service) Status(ctx context.Context) (Status, error) {
	jobs, err := s.store.ListJobRuns(ctx)
	if err != nil {
		return Status{}, err
	}
	health, err := s.health.Health(ctx)
	if err != nil {
		return Status{}, err
	}
	return Status{Jobs: jobs, Health: health}, nil
}

// AuditPage is one page of the audit log. NextBefore is zero on the last page.
type AuditPage struct {
	Events     []audit.Entry
	NextBefore int64
}

// AuditLog returns a page of events, newest first.
func (s *Service) AuditLog(ctx context.Context, filter audit.Filter) (AuditPage, error) {
	// One extra shows whether there's another page, without a second query.
	entries, err := s.store.Audit().List(ctx, filter, auditPageSize+1)
	if err != nil {
		return AuditPage{}, err
	}
	if len(entries) <= auditPageSize {
		return AuditPage{Events: entries}, nil
	}
	entries = entries[:auditPageSize]
	return AuditPage{Events: entries, NextBefore: entries[auditPageSize-1].ID}, nil
}
