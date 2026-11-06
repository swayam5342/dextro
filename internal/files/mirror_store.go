package files

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/swayam5342/dextro/internal/db/dbgen"
)

// folderObject ties a dextro folder to its folder in one account. objectID is empty
// while it's waiting to be made there.
type folderObject struct {
	folderID     uuid.UUID
	connectionID uuid.UUID
	objectID     string
	stale        bool
}

// FolderObjectForOwner returns an account's folder for a dextro folder, or nothing
// if it has none yet.
func (s *Store) FolderObjectForOwner(ctx context.Context, userID, folderID, connectionID uuid.UUID) (string, error) {
	object, err := s.queries.GetFolderObjectForOwner(ctx, dbgen.GetFolderObjectForOwnerParams{
		UserID:       userID,
		FolderID:     folderID,
		ConnectionID: connectionID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("find folder object: %w", err)
	}
	return object.String, nil
}

// SetFolderObject records an account's folder for a dextro folder.
func (s *Store) SetFolderObject(ctx context.Context, folderID, connectionID uuid.UUID, objectID string) error {
	err := s.queries.SetFolderObject(ctx, dbgen.SetFolderObjectParams{
		FolderID:     folderID,
		ConnectionID: connectionID,
		ObjectID:     pgtype.Text{String: objectID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("save folder object: %w", err)
	}
	return nil
}

// AddPendingFolderObject notes that an account should have this folder.
func (s *Store) AddPendingFolderObject(ctx context.Context, folderID, connectionID uuid.UUID) error {
	err := s.queries.AddPendingFolderObject(ctx, dbgen.AddPendingFolderObjectParams{FolderID: folderID, ConnectionID: connectionID})
	if err != nil {
		return fmt.Errorf("add pending folder: %w", err)
	}
	return nil
}

// AddPendingFolderObjectsForConnection notes that an account should have all of a
// user's folders.
func (s *Store) AddPendingFolderObjectsForConnection(ctx context.Context, userID, connectionID uuid.UUID) error {
	err := s.queries.AddPendingFolderObjectsForConnection(ctx, dbgen.AddPendingFolderObjectsForConnectionParams{UserID: userID, ConnectionID: connectionID})
	if err != nil {
		return fmt.Errorf("add pending folders: %w", err)
	}
	return nil
}

// FolderObjectsForConnection returns an account's folders, made or waiting.
func (s *Store) FolderObjectsForConnection(ctx context.Context, userID, connectionID uuid.UUID) ([]folderObject, error) {
	rows, err := s.queries.ListFolderObjectsForConnection(ctx, dbgen.ListFolderObjectsForConnectionParams{UserID: userID, ConnectionID: connectionID})
	if err != nil {
		return nil, fmt.Errorf("list folder objects: %w", err)
	}
	objects := make([]folderObject, 0, len(rows))
	for _, row := range rows {
		objects = append(objects, folderObject{folderID: row.FolderID, connectionID: connectionID, objectID: row.ObjectID.String, stale: row.Stale})
	}
	return objects, nil
}

// FolderObjectsForFolder returns a folder in each account that has made it.
func (s *Store) FolderObjectsForFolder(ctx context.Context, userID, folderID uuid.UUID) ([]folderObject, error) {
	rows, err := s.queries.ListFolderObjectsForFolder(ctx, dbgen.ListFolderObjectsForFolderParams{UserID: userID, FolderID: folderID})
	if err != nil {
		return nil, fmt.Errorf("list folder objects: %w", err)
	}
	objects := make([]folderObject, 0, len(rows))
	for _, row := range rows {
		objects = append(objects, folderObject{folderID: folderID, connectionID: row.ConnectionID, objectID: row.ObjectID.String})
	}
	return objects, nil
}

// MarkFolderObjectStale notes that a change didn't reach an account.
func (s *Store) MarkFolderObjectStale(ctx context.Context, folderID, connectionID uuid.UUID) error {
	err := s.queries.MarkFolderObjectStale(ctx, dbgen.MarkFolderObjectStaleParams{FolderID: folderID, ConnectionID: connectionID})
	if err != nil {
		return fmt.Errorf("mark folder stale: %w", err)
	}
	return nil
}

// ClearFolderObjectStale notes that an account has caught up.
func (s *Store) ClearFolderObjectStale(ctx context.Context, folderID, connectionID uuid.UUID) error {
	err := s.queries.ClearFolderObjectStale(ctx, dbgen.ClearFolderObjectStaleParams{FolderID: folderID, ConnectionID: connectionID})
	if err != nil {
		return fmt.Errorf("clear folder stale: %w", err)
	}
	return nil
}

// DeleteFolderObject forgets an account's folder, which leaves nothing waiting, so
// a sync won't make it again.
func (s *Store) DeleteFolderObject(ctx context.Context, folderID, connectionID uuid.UUID) error {
	err := s.queries.DeleteFolderObject(ctx, dbgen.DeleteFolderObjectParams{FolderID: folderID, ConnectionID: connectionID})
	if err != nil {
		return fmt.Errorf("delete folder object: %w", err)
	}
	return nil
}

// DeleteFolderIfUnused removes a folder that nothing is in and no account has. It
// says whether it did.
func (s *Store) DeleteFolderIfUnused(ctx context.Context, userID, folderID uuid.UUID) (bool, error) {
	rows, err := s.queries.DeleteFolderIfUnused(ctx, dbgen.DeleteFolderIfUnusedParams{UserID: userID, ID: folderID})
	if err != nil {
		return false, fmt.Errorf("delete unused folder: %w", err)
	}
	return rows > 0, nil
}

// FindFolderByName returns a folder by where it is, for merging the same folder in
// several accounts. It's nil if there's none.
func (s *Store) FindFolderByName(ctx context.Context, userID uuid.UUID, parent *uuid.UUID, name string) (*Folder, error) {
	row, err := s.queries.FindFolderByName(ctx, dbgen.FindFolderByNameParams{UserID: userID, ParentID: nullable(parent), Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find folder: %w", err)
	}
	folder := toFolder(row)
	return &folder, nil
}

// FoldersSynced reports whether an account's files have been sorted into real folders.
func (s *Store) FoldersSynced(ctx context.Context, userID, connectionID uuid.UUID) (bool, error) {
	synced, err := s.queries.GetConnectionFoldersSynced(ctx, dbgen.GetConnectionFoldersSyncedParams{UserID: userID, ID: connectionID})
	if err != nil {
		return false, fmt.Errorf("read folder sync: %w", err)
	}
	return synced, nil
}

// SetFoldersSynced records that an account's files have been sorted.
func (s *Store) SetFoldersSynced(ctx context.Context, userID, connectionID uuid.UUID) error {
	err := s.queries.SetConnectionFoldersSynced(ctx, dbgen.SetConnectionFoldersSyncedParams{UserID: userID, ID: connectionID})
	if err != nil {
		return fmt.Errorf("save folder sync: %w", err)
	}
	return nil
}
