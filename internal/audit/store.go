// Package audit records security-relevant actions.
package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db/dbgen"
)

// Actions recorded in audit_events. Stored values, so don't rename them.
const (
	ActionUserRegistered  = "user.registered"
	ActionRoleChanged     = "user.role_changed"
	ActionLogin           = "auth.login"
	ActionLoginFailed     = "auth.login_failed"
	ActionLogout          = "auth.logout"
	ActionPasswordChanged = "auth.password_changed"
	ActionSessionRevoked  = "auth.session_revoked"
	ActionGoogleLinked    = "auth.google_linked"
	ActionSettingsUpdated = "settings.updated"
)

// Event is one recorded action. Metadata must never hold secrets or tokens.
type Event struct {
	// ActorID is the user who acted. It stays zero when nobody can be
	// identified, as with the CLI or a failed login for an unknown email.
	ActorID  uuid.UUID
	Action   string
	IP       string
	Metadata map[string]any
}

// Store writes audit events. Pass a transaction to record an event atomically
// with the change it describes.
type Store struct {
	queries *dbgen.Queries
}

// NewStore returns a Store that runs its queries on db.
func NewStore(db dbgen.DBTX) *Store {
	return &Store{queries: dbgen.New(db)}
}

// Record saves one event.
func (s *Store) Record(ctx context.Context, e Event) error {
	metadata := e.Metadata
	if metadata == nil {
		metadata = map[string]any{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	err = s.queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
		ActorID:  uuid.NullUUID{UUID: e.ActorID, Valid: e.ActorID != uuid.Nil},
		Action:   e.Action,
		Ip:       e.IP,
		Metadata: encoded,
	})
	if err != nil {
		return fmt.Errorf("record audit event %q: %w", e.Action, err)
	}
	return nil
}
