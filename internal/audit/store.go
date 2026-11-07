// Package audit records security-relevant actions.
package audit

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/swayam5342/tributary/internal/db/dbgen"
)

// Actions recorded in audit_events. Stored values, so don't rename them.
const (
	ActionUserRegistered   = "user.registered"
	ActionUserCreated      = "user.created"
	ActionUserDeleted      = "user.deleted"
	ActionPasswordReset    = "user.password_reset"
	ActionRoleChanged      = "user.role_changed"
	ActionLogin            = "auth.login"
	ActionLoginFailed      = "auth.login_failed"
	ActionLogout           = "auth.logout"
	ActionPasswordChanged  = "auth.password_changed"
	ActionSessionRevoked   = "auth.session_revoked"
	ActionGoogleLinked     = "auth.google_linked"
	ActionSettingsUpdated  = "settings.updated"
	ActionJobStarted       = "job.started"
	ActionStorageConnected = "storage.connected"
	ActionStorageRemoved   = "storage.removed"
	ActionStorageSynced    = "storage.synced"
	ActionPickerOpened     = "storage.picker_opened"
	ActionFilesAdded       = "storage.files_added"
	ActionFilesTransferred = "storage.files_transferred"
	ActionStorageEmptied   = "storage.emptied"
	ActionRoutingChanged   = "storage.routing_changed"
	ActionAPIKeyCreated    = "api_key.created"
	ActionAPIKeyRevoked    = "api_key.revoked"
	ActionFileDeleted      = "file.deleted"
	ActionFileShared       = "file.shared"
	ActionShareChanged     = "file.share_changed"
	ActionFileUnshared     = "file.unshared"
	ActionShareExpired     = "file.share_expired"
	ActionFolderDeleted    = "folder.deleted"
	ActionFolderEncryption = "folder.encryption_changed"
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

// Store reads and writes audit events. Pass a transaction to record an event
// atomically with the change it describes.
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

// RememberActor writes a user's email onto their events. Call it before deleting
// the account, which blanks actor_id, so the log still says who did what.
func (s *Store) RememberActor(ctx context.Context, userID uuid.UUID, email string) error {
	err := s.queries.SetAuditActorEmail(ctx, dbgen.SetAuditActorEmailParams{
		ActorID:    uuid.NullUUID{UUID: userID, Valid: true},
		ActorEmail: pgtype.Text{String: email, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("remember audit actor: %w", err)
	}
	return nil
}

// Filter narrows a listing. Zero values mean no filter.
type Filter struct {
	Action string
	// Before keeps events older than this ID.
	Before int64
}

// Entry is a recorded event, as the admin reads it.
type Entry struct {
	ID int64
	// ActorEmail is nil for the command line, or if nobody could be identified.
	ActorEmail *string
	Action     string
	IP         string
	Metadata   map[string]any
	CreatedAt  time.Time
}

// List returns up to limit events, newest first. It covers every user on purpose,
// and only the admin's audit log calls it.
func (s *Store) List(ctx context.Context, filter Filter, limit int32) ([]Entry, error) {
	rows, err := s.queries.ListAuditEvents(ctx, dbgen.ListAuditEventsParams{
		Action:   pgtype.Text{String: filter.Action, Valid: filter.Action != ""},
		Before:   pgtype.Int8{Int64: filter.Before, Valid: filter.Before != 0},
		PageSize: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	entries := make([]Entry, 0, len(rows))
	for _, row := range rows {
		entry := Entry{ID: row.ID, Action: row.Action, IP: row.Ip, CreatedAt: row.CreatedAt}
		switch {
		case row.CurrentEmail.Valid:
			entry.ActorEmail = &row.CurrentEmail.String
		case row.DeletedEmail.Valid:
			entry.ActorEmail = &row.DeletedEmail.String
		}
		if err := json.Unmarshal(row.Metadata, &entry.Metadata); err != nil {
			return nil, fmt.Errorf("decode audit metadata: %w", err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}
