package files

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/swayam5342/tributary/internal/db/dbgen"
)

// searchSorts are the orders a search can come back in, which match the app's. It has to
// happen here, because the limit would otherwise cut off what a sort is looking for.
var searchSorts = map[string]bool{
	"name-asc": true, "name-desc": true,
	"date-desc": true, "date-asc": true,
	"size-desc": true, "size-asc": true,
}

// Search finds a user's live files and folders. The text can carry operators, the way
// Gmail's does: see searchFilter. Only a file can have a type, a size and so on, so a
// search that asks for those leaves folders out. An unknown sort means by name.
func (s *Service) Search(ctx context.Context, userID uuid.UUID, raw, sort string) (Listing, error) {
	query := strings.TrimSpace(raw)
	if query == "" || utf8.RuneCountInString(query) > maxNameRunes {
		return Listing{}, ErrInvalidSearch
	}
	filter := parseSearch(query, time.Now())
	if filter.empty() {
		return Listing{}, ErrInvalidSearch
	}
	if !searchSorts[sort] {
		sort = "name-asc"
	}
	folders := []Folder{}
	if !filter.filtersFiles() {
		var err error
		if folders, err = s.store.SearchFoldersForOwner(ctx, userID, filter); err != nil {
			return Listing{}, err
		}
	}
	files, err := s.store.SearchFilesForOwner(ctx, userID, filter, sort)
	if err != nil {
		return Listing{}, err
	}
	return Listing{Folders: folders, Files: files}, nil
}

func optionalSize(size *int64) pgtype.Int8 {
	if size == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *size, Valid: true}
}

// SearchFilesForOwner lists a user's live files that match a search, in the order asked for.
func (s *Store) SearchFilesForOwner(ctx context.Context, userID uuid.UUID, f searchFilter, sort string) ([]File, error) {
	rows, err := s.queries.SearchFilesForOwner(ctx, dbgen.SearchFilesForOwnerParams{
		UserID:    userID,
		Words:     f.words,
		Without:   f.without,
		Types:     f.types,
		MinSize:   optionalSize(f.minSize),
		MaxSize:   optionalSize(f.maxSize),
		After:     f.after,
		Before:    f.before,
		Account:   optional(f.account),
		InFolder:  optional(f.folder),
		Starred:   f.starred,
		Encrypted: f.encrypted,
		Shared:    f.shared,
		Sort:      sort,
	})
	if err != nil {
		return nil, fmt.Errorf("search files: %w", err)
	}
	return toFiles(rows), nil
}

// SearchFoldersForOwner lists a user's live folders whose name matches a search.
func (s *Store) SearchFoldersForOwner(ctx context.Context, userID uuid.UUID, f searchFilter) ([]Folder, error) {
	rows, err := s.queries.SearchFoldersForOwner(ctx, dbgen.SearchFoldersForOwnerParams{UserID: userID, Words: f.words, Without: f.without})
	if err != nil {
		return nil, fmt.Errorf("search folders: %w", err)
	}
	folders := make([]Folder, 0, len(rows))
	for _, row := range rows {
		folders = append(folders, toFolder(row))
	}
	return folders, nil
}
