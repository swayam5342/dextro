package files

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/swayam5342/dextro/internal/admin"
	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/db/dbgen"
)

// database is a pool or a transaction.
type database interface {
	dbgen.DBTX
	Begin(ctx context.Context) (pgx.Tx, error)
}

// fileRecord is a file plus the provider's ID for it, which never leaves this
// package.
type fileRecord struct {
	File
	objectID string
}

// Store reads and writes files and folders. Every function takes the owner's ID.
type Store struct {
	db      database
	queries *dbgen.Queries
}

// NewStore returns a Store that runs its queries on db.
func NewStore(db database) *Store {
	return &Store{db: db, queries: dbgen.New(db)}
}

// InTx runs fn with a Store bound to one transaction.
func (s *Store) InTx(ctx context.Context, fn func(tx *Store) error) error {
	return pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		return fn(NewStore(tx))
	})
}

// Audit returns the audit log on the same connection as this store.
func (s *Store) Audit() *audit.Store {
	return audit.NewStore(s.db)
}

// Settings returns the instance settings on the same connection as this store.
func (s *Store) Settings() *admin.Store {
	return admin.NewStore(s.db)
}

// CreateFile saves a stored object's details.
func (s *Store) CreateFile(ctx context.Context, userID uuid.UUID, r fileRecord) (File, error) {
	row, err := s.queries.CreateFile(ctx, dbgen.CreateFileParams{
		ID:           uuid.New(),
		UserID:       userID,
		ConnectionID: r.ConnectionID,
		FolderID:     nullable(r.FolderID),
		ObjectID:     r.objectID,
		Name:         r.Name,
		Size:         r.Size,
		ContentType:  r.ContentType,
	})
	if err != nil {
		return File{}, fmt.Errorf("create file: %w", err)
	}
	return toRecord(row).File, nil
}

// FileForOwner returns one of a user's files.
func (s *Store) FileForOwner(ctx context.Context, userID, id uuid.UUID) (fileRecord, error) {
	row, err := s.queries.GetFileForOwner(ctx, dbgen.GetFileForOwnerParams{UserID: userID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return fileRecord{}, ErrFileNotFound
	}
	if err != nil {
		return fileRecord{}, fmt.Errorf("get file: %w", err)
	}
	return toRecord(row), nil
}

// FilesInFolderForOwner lists the live files directly in a folder, or at the top
// level if folderID is nil.
func (s *Store) FilesInFolderForOwner(ctx context.Context, userID uuid.UUID, folderID *uuid.UUID) ([]File, error) {
	rows, err := s.queries.ListFilesInFolderForOwner(ctx, dbgen.ListFilesInFolderForOwnerParams{
		UserID:   userID,
		FolderID: nullable(folderID),
	})
	if err != nil {
		return nil, fmt.Errorf("list files: %w", err)
	}
	return toFiles(rows), nil
}

// RenameFileForOwner changes a file's name.
func (s *Store) RenameFileForOwner(ctx context.Context, userID, id uuid.UUID, name string) (File, error) {
	row, err := s.queries.RenameFileForOwner(ctx, dbgen.RenameFileForOwnerParams{UserID: userID, ID: id, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, ErrFileNotFound
	}
	if err != nil {
		return File{}, fmt.Errorf("rename file: %w", err)
	}
	return toRecord(row).File, nil
}

// MoveFileForOwner changes a file's folder.
func (s *Store) MoveFileForOwner(ctx context.Context, userID, id uuid.UUID, folderID *uuid.UUID) (File, error) {
	row, err := s.queries.MoveFileForOwner(ctx, dbgen.MoveFileForOwnerParams{UserID: userID, ID: id, FolderID: nullable(folderID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return File{}, ErrFileNotFound
	}
	if err != nil {
		return File{}, fmt.Errorf("move file: %w", err)
	}
	return toRecord(row).File, nil
}

// SetFileTrashedForOwner puts a file in the trash, or takes it out if at is nil.
func (s *Store) SetFileTrashedForOwner(ctx context.Context, userID, id uuid.UUID, at *time.Time) error {
	rows, err := s.queries.SetFileTrashedForOwner(ctx, dbgen.SetFileTrashedForOwnerParams{UserID: userID, ID: id, TrashedAt: at})
	if err != nil {
		return fmt.Errorf("set file trashed: %w", err)
	}
	if rows == 0 {
		return ErrFileNotFound
	}
	return nil
}

// DeleteFileForOwner removes a file's record.
func (s *Store) DeleteFileForOwner(ctx context.Context, userID, id uuid.UUID) error {
	rows, err := s.queries.DeleteFileForOwner(ctx, dbgen.DeleteFileForOwnerParams{UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if rows == 0 {
		return ErrFileNotFound
	}
	return nil
}

// TrashedFilesForOwner lists a user's files in the trash.
func (s *Store) TrashedFilesForOwner(ctx context.Context, userID uuid.UUID) ([]fileRecord, error) {
	rows, err := s.queries.ListTrashedFilesForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list trashed files: %w", err)
	}
	records := make([]fileRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, toRecord(row))
	}
	return records, nil
}

// FilesUnderFolderForOwner lists every file below a folder, at any depth.
func (s *Store) FilesUnderFolderForOwner(ctx context.Context, userID, folderID uuid.UUID) ([]fileRecord, error) {
	rows, err := s.queries.ListFilesUnderFolderForOwner(ctx, dbgen.ListFilesUnderFolderForOwnerParams{UserID: userID, ID: folderID})
	if err != nil {
		return nil, fmt.Errorf("list files under folder: %w", err)
	}
	records := make([]fileRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, toRecord(row))
	}
	return records, nil
}

// CreateFolder adds a folder.
func (s *Store) CreateFolder(ctx context.Context, userID uuid.UUID, parentID *uuid.UUID, name string) (Folder, error) {
	row, err := s.queries.CreateFolder(ctx, dbgen.CreateFolderParams{UserID: userID, ParentID: nullable(parentID), Name: name})
	if err != nil {
		return Folder{}, fmt.Errorf("create folder: %w", err)
	}
	return toFolder(row), nil
}

// FolderForOwner returns one of a user's folders.
func (s *Store) FolderForOwner(ctx context.Context, userID, id uuid.UUID) (Folder, error) {
	row, err := s.queries.GetFolderForOwner(ctx, dbgen.GetFolderForOwnerParams{UserID: userID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return Folder{}, ErrFolderNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("get folder: %w", err)
	}
	return toFolder(row), nil
}

// FoldersInFolderForOwner lists the live folders directly in a folder, or at the
// top level if parentID is nil.
func (s *Store) FoldersInFolderForOwner(ctx context.Context, userID uuid.UUID, parentID *uuid.UUID) ([]Folder, error) {
	rows, err := s.queries.ListFoldersInFolderForOwner(ctx, dbgen.ListFoldersInFolderForOwnerParams{
		UserID:   userID,
		ParentID: nullable(parentID),
	})
	if err != nil {
		return nil, fmt.Errorf("list folders: %w", err)
	}
	folders := make([]Folder, 0, len(rows))
	for _, row := range rows {
		folders = append(folders, toFolder(row))
	}
	return folders, nil
}

// FolderPathForOwner returns a folder and its ancestors, top level first. It is
// empty if the user has no such folder.
func (s *Store) FolderPathForOwner(ctx context.Context, userID, id uuid.UUID) ([]Folder, error) {
	rows, err := s.queries.ListFolderPathForOwner(ctx, dbgen.ListFolderPathForOwnerParams{UserID: userID, ID: id})
	if err != nil {
		return nil, fmt.Errorf("list folder path: %w", err)
	}
	path := make([]Folder, len(rows))
	for i, row := range rows {
		path[len(rows)-1-i] = toFolder(dbgen.Folder(row))
	}
	return path, nil
}

// RenameFolderForOwner changes a folder's name.
func (s *Store) RenameFolderForOwner(ctx context.Context, userID, id uuid.UUID, name string) (Folder, error) {
	row, err := s.queries.RenameFolderForOwner(ctx, dbgen.RenameFolderForOwnerParams{UserID: userID, ID: id, Name: name})
	if errors.Is(err, pgx.ErrNoRows) {
		return Folder{}, ErrFolderNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("rename folder: %w", err)
	}
	return toFolder(row), nil
}

// LockFolderTreeForOwner holds until the transaction ends, so that one user's
// folder moves happen one at a time.
func (s *Store) LockFolderTreeForOwner(ctx context.Context, userID uuid.UUID) error {
	if err := s.queries.LockFolderTreeForOwner(ctx, userID); err != nil {
		return fmt.Errorf("lock folder tree: %w", err)
	}
	return nil
}

// MoveFolderForOwner changes a folder's parent.
func (s *Store) MoveFolderForOwner(ctx context.Context, userID, id uuid.UUID, parentID *uuid.UUID) (Folder, error) {
	row, err := s.queries.MoveFolderForOwner(ctx, dbgen.MoveFolderForOwnerParams{UserID: userID, ID: id, ParentID: nullable(parentID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Folder{}, ErrFolderNotFound
	}
	if err != nil {
		return Folder{}, fmt.Errorf("move folder: %w", err)
	}
	return toFolder(row), nil
}

// SetFolderTrashedForOwner puts a folder in the trash, or takes it out if at is nil.
func (s *Store) SetFolderTrashedForOwner(ctx context.Context, userID, id uuid.UUID, at *time.Time) error {
	rows, err := s.queries.SetFolderTrashedForOwner(ctx, dbgen.SetFolderTrashedForOwnerParams{UserID: userID, ID: id, TrashedAt: at})
	if err != nil {
		return fmt.Errorf("set folder trashed: %w", err)
	}
	if rows == 0 {
		return ErrFolderNotFound
	}
	return nil
}

// DeleteFolderForOwner removes a folder and, through the database, the folders
// inside it.
func (s *Store) DeleteFolderForOwner(ctx context.Context, userID, id uuid.UUID) error {
	rows, err := s.queries.DeleteFolderForOwner(ctx, dbgen.DeleteFolderForOwnerParams{UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("delete folder: %w", err)
	}
	if rows == 0 {
		return ErrFolderNotFound
	}
	return nil
}

// TrashedFoldersForOwner lists a user's folders in the trash.
func (s *Store) TrashedFoldersForOwner(ctx context.Context, userID uuid.UUID) ([]Folder, error) {
	rows, err := s.queries.ListTrashedFoldersForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list trashed folders: %w", err)
	}
	folders := make([]Folder, 0, len(rows))
	for _, row := range rows {
		folders = append(folders, toFolder(row))
	}
	return folders, nil
}

func nullable(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func pointer(id uuid.NullUUID) *uuid.UUID {
	if !id.Valid {
		return nil
	}
	return &id.UUID
}

func toRecord(row dbgen.File) fileRecord {
	return fileRecord{
		File: File{
			ID:           row.ID,
			ConnectionID: row.ConnectionID,
			FolderID:     pointer(row.FolderID),
			Name:         row.Name,
			Size:         row.Size,
			ContentType:  row.ContentType,
			TrashedAt:    row.TrashedAt,
			CreatedAt:    row.CreatedAt,
			UpdatedAt:    row.UpdatedAt,
		},
		objectID: row.ObjectID,
	}
}

func toFiles(rows []dbgen.File) []File {
	files := make([]File, 0, len(rows))
	for _, row := range rows {
		files = append(files, toRecord(row).File)
	}
	return files
}

func toFolder(row dbgen.Folder) Folder {
	return Folder{
		ID:        row.ID,
		ParentID:  pointer(row.ParentID),
		Name:      row.Name,
		TrashedAt: row.TrashedAt,
		CreatedAt: row.CreatedAt,
		UpdatedAt: row.UpdatedAt,
	}
}

// ObjectsForConnection returns the IDs of a user's files in one account, by object ID.
func (s *Store) ObjectsForConnection(ctx context.Context, userID, connectionID uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := s.queries.ListObjectsForConnection(ctx, dbgen.ListObjectsForConnectionParams{UserID: userID, ConnectionID: connectionID})
	if err != nil {
		return nil, fmt.Errorf("list objects: %w", err)
	}
	objects := make(map[string]uuid.UUID, len(rows))
	for _, row := range rows {
		objects[row.ObjectID] = row.ID
	}
	return objects, nil
}

// ImportFile records an object found in an account, unless something already has.
// It says whether it added one.
func (s *Store) ImportFile(ctx context.Context, userID uuid.UUID, r fileRecord) (bool, error) {
	rows, err := s.queries.ImportFile(ctx, dbgen.ImportFileParams{
		ID:           uuid.New(),
		UserID:       userID,
		ConnectionID: r.ConnectionID,
		ObjectID:     r.objectID,
		Name:         r.Name,
		Size:         r.Size,
		ContentType:  r.ContentType,
	})
	if err != nil {
		return false, fmt.Errorf("import file: %w", err)
	}
	return rows > 0, nil
}
