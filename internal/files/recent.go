package files

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/db/dbgen"
)

// Recent lists a user's newest live files, wherever they are.
func (s *Service) Recent(ctx context.Context, userID uuid.UUID) ([]File, error) {
	return s.store.ListRecentFilesForOwner(ctx, userID)
}

// Starred lists a user's starred live files, the latest star first.
func (s *Service) Starred(ctx context.Context, userID uuid.UUID) ([]File, error) {
	return s.store.ListStarredFilesForOwner(ctx, userID)
}

// SetStarred stars a file or takes its star away. A star stays through the trash.
func (s *Service) SetStarred(ctx context.Context, userID, id uuid.UUID, starred bool) error {
	return s.store.SetFileStarredForOwner(ctx, userID, id, starred)
}

// ListRecentFilesForOwner returns a user's newest live files.
func (s *Store) ListRecentFilesForOwner(ctx context.Context, userID uuid.UUID) ([]File, error) {
	rows, err := s.queries.ListRecentFilesForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list recent files: %w", err)
	}
	return toFiles(rows), nil
}

// ListStarredFilesForOwner returns a user's starred live files.
func (s *Store) ListStarredFilesForOwner(ctx context.Context, userID uuid.UUID) ([]File, error) {
	rows, err := s.queries.ListStarredFilesForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list starred files: %w", err)
	}
	return toFiles(rows), nil
}

// SetFileStarredForOwner stars one of a user's files or takes the star away.
func (s *Store) SetFileStarredForOwner(ctx context.Context, userID, id uuid.UUID, starred bool) error {
	rows, err := s.queries.SetFileStarredForOwner(ctx, dbgen.SetFileStarredForOwnerParams{Starred: starred, UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("set file starred: %w", err)
	}
	if rows == 0 {
		return ErrFileNotFound
	}
	return nil
}
