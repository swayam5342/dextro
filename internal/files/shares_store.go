package files

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/swayam5342/dextro/internal/db/dbgen"
)

// dueShare is a link the expiry job has to end. An unfinished one never reached anybody.
type dueShare struct {
	fileID       uuid.UUID
	userID       uuid.UUID
	connectionID uuid.UUID
	objectID     string
	unfinished   bool
}

func toShare(row dbgen.FileShare) Share {
	return Share{FileID: row.FileID, Role: row.Role, URL: row.Url, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt}
}

// ShareForOwner returns a file's link, if it has one. URL is empty if Google hasn't
// confirmed it.
func (s *Store) ShareForOwner(ctx context.Context, userID, fileID uuid.UUID) (Share, bool, error) {
	row, err := s.queries.GetShareForOwner(ctx, dbgen.GetShareForOwnerParams{UserID: userID, FileID: fileID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Share{}, false, nil
	}
	if err != nil {
		return Share{}, false, fmt.Errorf("get share: %w", err)
	}
	return toShare(row), true, nil
}

// SharesForOwner lists a user's finished links.
func (s *Store) SharesForOwner(ctx context.Context, userID uuid.UUID) ([]Share, error) {
	rows, err := s.queries.ListSharesForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list shares: %w", err)
	}
	shares := make([]Share, 0, len(rows))
	for _, row := range rows {
		shares = append(shares, Share{FileID: row.FileID, Name: row.Name, Role: row.Role, URL: row.Url, ExpiresAt: row.ExpiresAt, CreatedAt: row.CreatedAt})
	}
	return shares, nil
}

// PrepareShare notes that a link is about to exist.
func (s *Store) PrepareShare(ctx context.Context, userID, fileID uuid.UUID, in ShareInput) error {
	err := s.queries.PrepareShare(ctx, dbgen.PrepareShareParams{FileID: fileID, UserID: userID, Role: in.Role, ExpiresAt: in.ExpiresAt})
	if err != nil {
		return fmt.Errorf("prepare share: %w", err)
	}
	return nil
}

// SaveShare records what Google confirmed. ErrShareNotFound means the row went while
// Google was answering.
func (s *Store) SaveShare(ctx context.Context, userID, fileID uuid.UUID, in ShareInput, url string) (Share, error) {
	row, err := s.queries.SaveShareForOwner(ctx, dbgen.SaveShareForOwnerParams{
		UserID:    userID,
		FileID:    fileID,
		Role:      in.Role,
		Url:       url,
		ExpiresAt: in.ExpiresAt,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Share{}, ErrShareNotFound
	}
	if err != nil {
		return Share{}, fmt.Errorf("save share: %w", err)
	}
	return toShare(row), nil
}

// DelayShare sends a link that couldn't be ended to the back of the queue.
func (s *Store) DelayShare(ctx context.Context, fileID uuid.UUID) error {
	if err := s.queries.DelayShare(ctx, fileID); err != nil {
		return fmt.Errorf("delay share: %w", err)
	}
	return nil
}

// DeleteShareForOwner forgets a link. Google has to have been told first.
func (s *Store) DeleteShareForOwner(ctx context.Context, userID, fileID uuid.UUID) error {
	rows, err := s.queries.DeleteShareForOwner(ctx, dbgen.DeleteShareForOwnerParams{UserID: userID, FileID: fileID})
	if err != nil {
		return fmt.Errorf("delete share: %w", err)
	}
	if rows == 0 {
		return ErrShareNotFound
	}
	return nil
}

// SharedFilesUnderFolder lists the files with links inside a folder, however deep.
func (s *Store) SharedFilesUnderFolder(ctx context.Context, userID, folderID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.queries.ListSharedFilesUnderFolderForOwner(ctx, dbgen.ListSharedFilesUnderFolderForOwnerParams{UserID: userID, ID: folderID})
	if err != nil {
		return nil, fmt.Errorf("list shared files: %w", err)
	}
	return ids, nil
}

// CountSharesForConnection counts the links on files in an account.
func (s *Store) CountSharesForConnection(ctx context.Context, userID, connectionID uuid.UUID) (int64, error) {
	count, err := s.queries.CountSharesForConnection(ctx, dbgen.CountSharesForConnectionParams{UserID: userID, ConnectionID: connectionID})
	if err != nil {
		return 0, fmt.Errorf("count shares: %w", err)
	}
	return count, nil
}

// DueShares returns links the expiry job has to end. It is unscoped on purpose and is
// for the background job only.
func (s *Store) DueShares(ctx context.Context, abandonedBefore time.Time) ([]dueShare, error) {
	rows, err := s.queries.ListDueShares(ctx, abandonedBefore)
	if err != nil {
		return nil, fmt.Errorf("list due shares: %w", err)
	}
	due := make([]dueShare, 0, len(rows))
	for _, row := range rows {
		due = append(due, dueShare{
			fileID:       row.FileID,
			userID:       row.UserID,
			connectionID: row.ConnectionID,
			objectID:     row.ObjectID,
			unfinished:   row.Url == "",
		})
	}
	return due, nil
}
