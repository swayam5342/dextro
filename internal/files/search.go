package files

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db/dbgen"
)

// searchEscaper makes a search literal, so typing % doesn't match everything.
var searchEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// Search finds a user's live files and folders by part of their name.
func (s *Service) Search(ctx context.Context, userID uuid.UUID, raw string) (Listing, error) {
	query := strings.TrimSpace(raw)
	if query == "" || utf8.RuneCountInString(query) > maxNameRunes {
		return Listing{}, ErrInvalidSearch
	}
	pattern := "%" + searchEscaper.Replace(query) + "%"
	folders, err := s.store.SearchFoldersForOwner(ctx, userID, pattern)
	if err != nil {
		return Listing{}, err
	}
	files, err := s.store.SearchFilesForOwner(ctx, userID, pattern)
	if err != nil {
		return Listing{}, err
	}
	return Listing{Folders: folders, Files: files}, nil
}

// SearchFilesForOwner lists a user's live files whose name matches a LIKE pattern.
func (s *Store) SearchFilesForOwner(ctx context.Context, userID uuid.UUID, pattern string) ([]File, error) {
	rows, err := s.queries.SearchFilesForOwner(ctx, dbgen.SearchFilesForOwnerParams{UserID: userID, Pattern: pattern})
	if err != nil {
		return nil, fmt.Errorf("search files: %w", err)
	}
	return toFiles(rows), nil
}

// SearchFoldersForOwner lists a user's live folders whose name matches a LIKE pattern.
func (s *Store) SearchFoldersForOwner(ctx context.Context, userID uuid.UUID, pattern string) ([]Folder, error) {
	rows, err := s.queries.SearchFoldersForOwner(ctx, dbgen.SearchFoldersForOwnerParams{UserID: userID, Pattern: pattern})
	if err != nil {
		return nil, fmt.Errorf("search folders: %w", err)
	}
	folders := make([]Folder, 0, len(rows))
	for _, row := range rows {
		folders = append(folders, toFolder(row))
	}
	return folders, nil
}
