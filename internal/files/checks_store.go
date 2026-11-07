package files

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/swayam5342/tributary/internal/db/dbgen"
)

// SetFileChecksumForObject saves what an account says about one of its objects.
func (s *Store) SetFileChecksumForObject(ctx context.Context, userID, id, connectionID uuid.UUID, objectID, checksum string) error {
	err := s.queries.SetFileChecksumForObject(ctx, dbgen.SetFileChecksumForObjectParams{
		Checksum:     optional(checksum),
		UserID:       userID,
		ID:           id,
		ConnectionID: connectionID,
		ObjectID:     objectID,
	})
	if err != nil {
		return fmt.Errorf("set file checksum: %w", err)
	}
	return nil
}

// FilesToCheck returns the files that have waited longest for a check, anyone's: only
// the background check calls this.
func (s *Store) FilesToCheck(ctx context.Context, limit int32) ([]checkItem, error) {
	rows, err := s.queries.ListFilesToCheck(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list files to check: %w", err)
	}
	items := make([]checkItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, checkItem{fileRecord: toRecord(row), userID: row.UserID})
	}
	return items, nil
}

// MarkFileChecked notes that a check reached a file.
func (s *Store) MarkFileChecked(ctx context.Context, userID, id uuid.UUID) error {
	err := s.queries.MarkFileCheckedForOwner(ctx, dbgen.MarkFileCheckedForOwnerParams{UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("mark file checked: %w", err)
	}
	return nil
}

// RecordFileProblem saves a problem. One a file already has isn't added twice. reported is
// what the account says now, for a file that changed.
func (s *Store) RecordFileProblem(ctx context.Context, userID uuid.UUID, p FileProblem, reported string) error {
	err := s.queries.RecordFileProblem(ctx, dbgen.RecordFileProblemParams{
		UserID:           userID,
		FileID:           nullable(p.FileID),
		ConnectionID:     p.ConnectionID,
		Name:             p.Name,
		Size:             p.Size,
		Kind:             string(p.Kind),
		ReportedChecksum: optional(reported),
	})
	if err != nil {
		return fmt.Errorf("record file problem: %w", err)
	}
	return nil
}

// ClearFileProblem removes a problem a file no longer has.
func (s *Store) ClearFileProblem(ctx context.Context, userID, fileID uuid.UUID, kind ProblemKind) error {
	err := s.queries.ClearFileProblemForOwner(ctx, dbgen.ClearFileProblemForOwnerParams{
		UserID: userID,
		FileID: uuid.NullUUID{UUID: fileID, Valid: true},
		Kind:   string(kind),
	})
	if err != nil {
		return fmt.Errorf("clear file problem: %w", err)
	}
	return nil
}

func toProblem(row dbgen.FileProblem) FileProblem {
	return FileProblem{
		ID:           row.ID,
		FileID:       pointer(row.FileID),
		ConnectionID: row.ConnectionID,
		Name:         row.Name,
		Size:         row.Size,
		Kind:         ProblemKind(row.Kind),
		FoundAt:      row.FoundAt,
	}
}

// FileProblems lists a user's problems, the newest first.
func (s *Store) FileProblems(ctx context.Context, userID uuid.UUID) ([]FileProblem, error) {
	rows, err := s.queries.ListFileProblemsForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list file problems: %w", err)
	}
	problems := make([]FileProblem, 0, len(rows))
	for _, row := range rows {
		problems = append(problems, toProblem(row))
	}
	return problems, nil
}

// FileProblem returns one of a user's problems, and what the account reported.
func (s *Store) FileProblem(ctx context.Context, userID, id uuid.UUID) (FileProblem, string, error) {
	row, err := s.queries.GetFileProblemForOwner(ctx, dbgen.GetFileProblemForOwnerParams{UserID: userID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return FileProblem{}, "", ErrProblemNotFound
	}
	if err != nil {
		return FileProblem{}, "", fmt.Errorf("get file problem: %w", err)
	}
	return toProblem(row), row.ReportedChecksum.String, nil
}

// DeleteFileProblem removes one of a user's problems.
func (s *Store) DeleteFileProblem(ctx context.Context, userID, id uuid.UUID) error {
	rows, err := s.queries.DeleteFileProblemForOwner(ctx, dbgen.DeleteFileProblemForOwnerParams{UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("delete file problem: %w", err)
	}
	if rows == 0 {
		return ErrProblemNotFound
	}
	return nil
}

// AcceptFileChecksum takes a file's content as it is now.
func (s *Store) AcceptFileChecksum(ctx context.Context, userID, id uuid.UUID, checksum string) error {
	err := s.queries.AcceptFileChecksumForOwner(ctx, dbgen.AcceptFileChecksumForOwnerParams{Checksum: optional(checksum), UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("accept file checksum: %w", err)
	}
	return nil
}

// CountFileChecks says how many of a user's files can be checked and have been.
func (s *Store) CountFileChecks(ctx context.Context, userID uuid.UUID) (FileChecks, error) {
	row, err := s.queries.CountFileChecksForOwner(ctx, userID)
	if err != nil {
		return FileChecks{}, fmt.Errorf("count file checks: %w", err)
	}
	return FileChecks{Checkable: row.Checkable, Checked: row.Checked}, nil
}

// DuplicateFiles returns a user's live files that share their content with another.
func (s *Store) DuplicateFiles(ctx context.Context, userID uuid.UUID) ([]fileRecord, error) {
	rows, err := s.queries.ListDuplicateFilesForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list duplicate files: %w", err)
	}
	records := make([]fileRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, toRecord(row))
	}
	return records, nil
}

// AllFolders returns every folder a user has, by ID.
func (s *Store) AllFolders(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]Folder, error) {
	rows, err := s.queries.ListAllFoldersForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	folders := make(map[uuid.UUID]Folder, len(rows))
	for _, row := range rows {
		folders[row.ID] = toFolder(row)
	}
	return folders, nil
}
