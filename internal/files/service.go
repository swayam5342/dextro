// Package files manages users' files and folders.
package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/audit"
	"github.com/swayam5342/tributary/internal/provider"
	"github.com/swayam5342/tributary/internal/secrets"
	"github.com/swayam5342/tributary/internal/storage"
)

const (
	maxNameRunes   = 255
	defaultType    = "application/octet-stream"
	cleanupTimeout = 30 * time.Second
)

// Errors the handlers turn into stable API error codes.
var (
	ErrInvalidName     = errors.New("invalid name")
	ErrFileNotFound    = errors.New("file not found")
	ErrProblemNotFound = errors.New("file problem not found")
	ErrFolderNotFound  = errors.New("folder not found")
	ErrFileTooLarge    = errors.New("file too large")
	ErrInvalidMove     = errors.New("a folder can't move into itself")
	ErrNotInTrash      = errors.New("not in the trash")
	ErrProviderFailed  = errors.New("storage account failed")
	ErrObjectMissing   = errors.New("stored object is missing")
	ErrInvalidSearch   = errors.New("invalid search")
)

// File is a stored file as its owner sees it.
type File struct {
	ID           uuid.UUID
	ConnectionID uuid.UUID
	FolderID     *uuid.UUID
	Name         string
	Size         int64
	ContentType  string
	// External marks someone's own Drive file. Deleting it leaves it in Drive.
	External  bool
	Starred   bool
	TrashedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Folder is a folder as its owner sees it.
type Folder struct {
	ID        uuid.UUID
	ParentID  *uuid.UUID
	Name      string
	TrashedAt *time.Time
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Contents is what's in a folder. Path runs from the top level down to it.
type Contents struct {
	Path    []Folder
	Folders []Folder
	Files   []File
}

// Listing is a set of folders and files, as the trash and search return. In the
// trash, a trashed folder's contents aren't listed, because they travel with it.
type Listing struct {
	Folders []Folder
	Files   []File
}

// UploadInput is a file to store. Size is -1 if the length isn't known up front.
// ConnectionID picks an account; left nil, the pool chooses one.
type UploadInput struct {
	ConnectionID *uuid.UUID
	FolderID     *uuid.UUID
	Name         string
	ContentType  string
	Size         int64
	Body         io.Reader
}

// Usage is how much of a user's storage each kind of file takes, in bytes.
type Usage struct {
	Photo    int64
	Video    int64
	Document int64
}

// Providers opens a user's linked accounts.
type Providers interface {
	Provider(ctx context.Context, userID, connectionID uuid.UUID) (provider.Provider, error)
	Choose(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID, size int64) (storage.Chosen, error)
	Reserve(ctx context.Context, userID uuid.UUID, preferred *uuid.UUID, r storage.Reservation) (storage.Chosen, error)
	SealUploadSession(id uuid.UUID, address string) secrets.Sealed
	OpenUploadSession(id uuid.UUID, sealed secrets.Sealed) (string, error)
	AllAccounts(ctx context.Context) ([]storage.Account, error)
	AccountsFor(ctx context.Context, userID uuid.UUID) ([]storage.Account, error)
	ReserveTransfers(ctx context.Context, userID uuid.UUID, request storage.TransferRequest) (int, error)
	StopDraining(ctx context.Context, userID, id uuid.UUID) error
}

// Service holds the rules for files and folders.
type Service struct {
	store     *Store
	providers Providers
	// locks holds a mutex per account, for making its folders one at a time.
	locks sync.Map
	// transferChunk and transferWait are how a move sends, and how long it first waits
	// after a failure.
	transferChunk int64
	transferWait  time.Duration
}

// NewService returns a Service.
func NewService(store *Store, providers Providers) *Service {
	return &Service{store: store, providers: providers, transferChunk: transferChunkBytes, transferWait: time.Second}
}

func cleanName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > maxNameRunes ||
		strings.ContainsAny(name, `/\`) || strings.ContainsFunc(name, unicode.IsControl) {
		return "", ErrInvalidName
	}
	return name, nil
}

func cleanContentType(raw string) string {
	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil || !strings.Contains(mediaType, "/") {
		return defaultType
	}
	return mediaType
}

// requireLiveFolder confirms a folder belongs to the user and that neither it
// nor anything above it is in the trash. A nil folder is the top level.
func requireLiveFolder(ctx context.Context, store *Store, userID uuid.UUID, id *uuid.UUID) ([]Folder, error) {
	if id == nil {
		return []Folder{}, nil
	}
	path, err := store.FolderPathForOwner(ctx, userID, *id)
	if err != nil {
		return nil, err
	}
	if len(path) == 0 || slices.ContainsFunc(path, func(f Folder) bool { return f.TrashedAt != nil }) {
		return nil, ErrFolderNotFound
	}
	return path, nil
}

// List returns what's in a folder, or at the top level if id is nil.
func (s *Service) List(ctx context.Context, userID uuid.UUID, id *uuid.UUID) (Contents, error) {
	path, err := requireLiveFolder(ctx, s.store, userID, id)
	if err != nil {
		return Contents{}, err
	}
	folders, err := s.store.FoldersInFolderForOwner(ctx, userID, id)
	if err != nil {
		return Contents{}, err
	}
	files, err := s.store.FilesInFolderForOwner(ctx, userID, id)
	if err != nil {
		return Contents{}, err
	}
	return Contents{Path: path, Folders: folders, Files: files}, nil
}

// limitedReader fails as soon as a body passes the limit. The size limit has to
// be enforced here, while streaming, because a chunked upload doesn't declare
// its length and a declared length can be a lie.
type limitedReader struct {
	r        io.Reader
	limit    int64
	read     int64
	exceeded bool
}

func (l *limitedReader) Read(p []byte) (int, error) {
	n, err := l.r.Read(p)
	l.read += int64(n)
	if l.read > l.limit {
		l.exceeded = true
		return 0, ErrFileTooLarge
	}
	return n, err
}

// Upload streams a file into a storage account and records it. Everything that
// can refuse the upload runs first, so a refused file is never read.
func (s *Service) Upload(ctx context.Context, userID uuid.UUID, in UploadInput) (File, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return File{}, err
	}
	if _, err := requireLiveFolder(ctx, s.store, userID, in.FolderID); err != nil {
		return File{}, err
	}
	limit, err := s.store.Settings().MaxUploadBytes(ctx)
	if err != nil {
		return File{}, err
	}
	if in.Size > limit {
		return File{}, ErrFileTooLarge
	}
	chosen, err := s.providers.Choose(ctx, userID, in.ConnectionID, in.Size)
	if err != nil {
		return File{}, err
	}
	stored := chosen.Provider
	parent, err := s.driveFolder(ctx, userID, chosen.ConnectionID, stored, in.FolderID)
	if err != nil {
		return File{}, err
	}

	contentType := cleanContentType(in.ContentType)
	body := &limitedReader{r: in.Body, limit: limit}
	object, err := stored.Put(ctx, provider.Upload{Name: name, ContentType: contentType, Parent: parent, Body: body})
	if body.exceeded {
		return File{}, ErrFileTooLarge
	}
	if err != nil {
		return File{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}

	file, err := s.store.CreateFile(ctx, userID, fileRecord{
		File: File{
			ConnectionID: chosen.ConnectionID,
			FolderID:     in.FolderID,
			Name:         name,
			Size:         object.Size,
			ContentType:  contentType,
		},
		objectID:    object.ID,
		checksum:    object.Checksum,
		contentHash: object.Checksum,
	})
	if err != nil {
		// Without a record nothing could ever find this object again. The request
		// may be why the save failed, so the cleanup doesn't depend on it.
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		return File{}, errors.Join(err, stored.Delete(cleanup, object.ID))
	}
	return file, nil
}

// Get returns one of a user's files.
func (s *Service) Get(ctx context.Context, userID, id uuid.UUID) (File, error) {
	record, err := s.store.FileForOwner(ctx, userID, id)
	if err != nil {
		return File{}, err
	}
	return record.File, nil
}

// Open streams a file's content, or part of it if rng isn't nil.
func (s *Service) Open(ctx context.Context, userID, id uuid.UUID, rng *provider.ByteRange) (io.ReadCloser, error) {
	record, err := s.store.FileForOwner(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	stored, err := s.openAccount(ctx, userID, record.ConnectionID)
	if err != nil {
		return nil, err
	}
	reader, err := stored.Get(ctx, record.objectID, rng)
	if errors.Is(err, provider.ErrObjectNotFound) {
		return nil, ErrObjectMissing
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return reader, nil
}

// Thumbnail opens a small preview image of a file.
func (s *Service) Thumbnail(ctx context.Context, userID, id uuid.UUID) (provider.Thumbnail, error) {
	record, err := s.store.FileForOwner(ctx, userID, id)
	if err != nil {
		return provider.Thumbnail{}, err
	}
	stored, err := s.openAccount(ctx, userID, record.ConnectionID)
	if err != nil {
		return provider.Thumbnail{}, err
	}
	thumbnail, err := stored.Thumbnail(ctx, record.objectID)
	if errors.Is(err, provider.ErrNoThumbnail) || errors.Is(err, provider.ErrObjectNotFound) {
		return provider.Thumbnail{}, provider.ErrNoThumbnail
	}
	if err != nil {
		return provider.Thumbnail{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return thumbnail, nil
}

// cleanupContext is for tidying up after a failure, which has to happen even if the
// request or the server is going away.
func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
}

// openAccount reaches an account a record says the file is in.
func (s *Service) openAccount(ctx context.Context, userID, connectionID uuid.UUID) (provider.Provider, error) {
	stored, err := s.providers.Provider(ctx, userID, connectionID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return stored, nil
}

// deleteObject removes an object. One that's already gone counts as success, which is
// what makes a failed delete safe to try again.
func deleteObject(ctx context.Context, stored provider.Provider, id string) error {
	if err := stored.Delete(ctx, id); err != nil && !errors.Is(err, provider.ErrObjectNotFound) {
		return fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return nil
}

// Usage adds up a user's files by kind.
func (s *Service) Usage(ctx context.Context, userID uuid.UUID) (Usage, error) {
	return s.store.UsageForOwner(ctx, userID)
}

// liveFile returns a file that isn't in the trash.
func liveFile(ctx context.Context, store *Store, userID, id uuid.UUID) (fileRecord, error) {
	record, err := store.FileForOwner(ctx, userID, id)
	if err != nil {
		return fileRecord{}, err
	}
	if record.TrashedAt != nil {
		return fileRecord{}, ErrFileNotFound
	}
	return record, nil
}

// RenameFile changes a file's name. Only Tributary's record changes.
func (s *Service) RenameFile(ctx context.Context, userID, id uuid.UUID, rawName string) (File, error) {
	name, err := cleanName(rawName)
	if err != nil {
		return File{}, err
	}
	record, err := liveFile(ctx, s.store, userID, id)
	if err != nil {
		return File{}, err
	}
	err = s.changeFileInDrive(ctx, userID, record, func(stored provider.Provider) error {
		return stored.Rename(ctx, record.objectID, name)
	})
	if err != nil {
		return File{}, err
	}
	return s.store.RenameFileForOwner(ctx, userID, id, name)
}

// MoveFile puts a file in another folder, or at the top level if target is nil.
func (s *Service) MoveFile(ctx context.Context, userID, id uuid.UUID, target *uuid.UUID) (File, error) {
	record, err := liveFile(ctx, s.store, userID, id)
	if err != nil {
		return File{}, err
	}
	if _, err := requireLiveFolder(ctx, s.store, userID, target); err != nil {
		return File{}, err
	}
	err = s.changeFileInDrive(ctx, userID, record, func(stored provider.Provider) error {
		parent, err := s.driveFolder(ctx, userID, record.ConnectionID, stored, target)
		if err != nil {
			return err
		}
		return stored.Move(ctx, record.objectID, parent)
	})
	if err != nil {
		return File{}, err
	}
	return s.store.MoveFileForOwner(ctx, userID, id, target)
}

// TrashFile moves a file to the trash. Its stored object isn't touched.
func (s *Service) TrashFile(ctx context.Context, userID, id uuid.UUID, ip string) error {
	// The trash exists only here, so the file is still in Drive and its link still works.
	if _, err := s.revokeShare(ctx, userID, id, ip); err != nil {
		return err
	}
	now := time.Now()
	return s.store.SetFileTrashedForOwner(ctx, userID, id, &now)
}

// RestoreFile takes a file out of the trash. If its folder is gone or trashed,
// the file goes to the top level rather than back out of sight.
func (s *Service) RestoreFile(ctx context.Context, userID, id uuid.UUID) error {
	var moved *fileRecord
	err := s.store.InTx(ctx, func(tx *Store) error {
		record, err := tx.FileForOwner(ctx, userID, id)
		if err != nil {
			return err
		}
		if _, err := requireLiveFolder(ctx, tx, userID, record.FolderID); errors.Is(err, ErrFolderNotFound) {
			if _, err := tx.MoveFileForOwner(ctx, userID, id, nil); err != nil {
				return err
			}
			moved = &record
		} else if err != nil {
			return err
		}
		return tx.SetFileTrashedForOwner(ctx, userID, id, nil)
	})
	if err != nil || moved == nil {
		return err
	}
	// The file is back either way, so a failure here isn't worth undoing that for.
	_ = s.changeFileInDrive(ctx, userID, *moved, func(stored provider.Provider) error {
		return stored.Move(ctx, moved.objectID, "")
	})
	return nil
}

// DeleteFile permanently deletes a file that's in the trash.
func (s *Service) DeleteFile(ctx context.Context, userID, id uuid.UUID, ip string) error {
	record, err := s.store.FileForOwner(ctx, userID, id)
	if err != nil {
		return err
	}
	if record.TrashedAt == nil {
		return ErrNotInTrash
	}
	return s.destroy(ctx, userID, record, ip)
}

// destroy removes the stored object first and the record second. If the provider
// fails, the record stays so the delete can be tried again; an object that's
// already gone counts as success, which is what makes a retry safe.
func (s *Service) destroy(ctx context.Context, userID uuid.UUID, record fileRecord, ip string) error {
	// Someone's own Drive file isn't Tributary's to delete, so only the record goes.
	if !record.External {
		stored, err := s.openAccount(ctx, userID, record.ConnectionID)
		if err != nil {
			return err
		}
		if err := deleteObject(ctx, stored, record.objectID); err != nil {
			return err
		}
	}
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.DeleteFileForOwner(ctx, userID, record.ID); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionFileDeleted,
			IP:       ip,
			Metadata: map[string]any{"file_id": record.ID, "name": record.Name, "size": record.Size},
		})
	})
}

// CreateFolder adds a folder, at the top level if parent is nil.
func (s *Service) CreateFolder(ctx context.Context, userID uuid.UUID, parent *uuid.UUID, rawName string) (Folder, error) {
	name, err := cleanName(rawName)
	if err != nil {
		return Folder{}, err
	}
	if _, err := requireLiveFolder(ctx, s.store, userID, parent); err != nil {
		return Folder{}, err
	}
	folder, err := s.store.CreateFolder(ctx, userID, parent, name)
	if err != nil {
		return Folder{}, err
	}
	return folder, s.mirrorNewFolder(ctx, userID, folder)
}

// RenameFolder changes a folder's name.
func (s *Service) RenameFolder(ctx context.Context, userID, id uuid.UUID, rawName string) (Folder, error) {
	name, err := cleanName(rawName)
	if err != nil {
		return Folder{}, err
	}
	if _, err := requireLiveFolder(ctx, s.store, userID, &id); err != nil {
		return Folder{}, err
	}
	folder, err := s.store.RenameFolderForOwner(ctx, userID, id, name)
	if err != nil {
		return Folder{}, err
	}
	return folder, s.mirrorFolderChange(ctx, userID, id, func(stored provider.Provider, _ uuid.UUID, objectID string) error {
		return stored.Rename(ctx, objectID, name)
	})
}

// MoveFolder puts a folder inside another, or at the top level if target is nil.
// The lock matters: two moves that are each fine alone could together make a
// loop, and nothing inside a loop can be reached.
func (s *Service) MoveFolder(ctx context.Context, userID, id uuid.UUID, target *uuid.UUID) (Folder, error) {
	var moved Folder
	err := s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.LockFolderTreeForOwner(ctx, userID); err != nil {
			return err
		}
		if _, err := requireLiveFolder(ctx, tx, userID, &id); err != nil {
			return err
		}
		path, err := requireLiveFolder(ctx, tx, userID, target)
		if err != nil {
			return err
		}
		if slices.ContainsFunc(path, func(f Folder) bool { return f.ID == id }) {
			return ErrInvalidMove
		}
		moved, err = tx.MoveFolderForOwner(ctx, userID, id, target)
		return err
	})
	if err != nil {
		return Folder{}, err
	}
	return moved, s.mirrorFolderMove(ctx, userID, id, target)
}

// TrashFolder moves a folder to the trash. Only the folder is marked: what's
// inside goes with it, and comes back with it.
func (s *Service) TrashFolder(ctx context.Context, userID, id uuid.UUID, ip string) error {
	if err := s.revokeSharesUnderFolder(ctx, userID, id, ip); err != nil {
		return err
	}
	now := time.Now()
	return s.store.SetFolderTrashedForOwner(ctx, userID, id, &now)
}

// RestoreFolder takes a folder out of the trash. If its parent is gone or trashed,
// the folder goes to the top level rather than back out of sight.
func (s *Service) RestoreFolder(ctx context.Context, userID, id uuid.UUID) error {
	movedToTop := false
	err := s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.LockFolderTreeForOwner(ctx, userID); err != nil {
			return err
		}
		folder, err := tx.FolderForOwner(ctx, userID, id)
		if err != nil {
			return err
		}
		if _, err := requireLiveFolder(ctx, tx, userID, folder.ParentID); errors.Is(err, ErrFolderNotFound) {
			if _, err := tx.MoveFolderForOwner(ctx, userID, id, nil); err != nil {
				return err
			}
			movedToTop = true
		} else if err != nil {
			return err
		}
		return tx.SetFolderTrashedForOwner(ctx, userID, id, nil)
	})
	if err != nil || !movedToTop {
		return err
	}
	return s.mirrorFolderMove(ctx, userID, id, nil)
}

// DeleteFolder permanently deletes a trashed folder and everything under it.
// Files go one at a time, so if a provider fails partway the folder stays in the
// trash holding what's left, and deleting it again finishes the job.
func (s *Service) DeleteFolder(ctx context.Context, userID, id uuid.UUID, ip string) error {
	folder, err := s.store.FolderForOwner(ctx, userID, id)
	if err != nil {
		return err
	}
	if folder.TrashedAt == nil {
		return ErrNotInTrash
	}
	records, err := s.store.FilesUnderFolderForOwner(ctx, userID, id)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := s.destroy(ctx, userID, record, ip); err != nil {
			return err
		}
	}
	if err := s.trashDriveFolders(ctx, userID, id); err != nil {
		return err
	}
	return s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.DeleteFolderForOwner(ctx, userID, id); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionFolderDeleted,
			IP:       ip,
			Metadata: map[string]any{"folder_id": id, "name": folder.Name},
		})
	})
}

// ListTrash returns what's in a user's trash.
func (s *Service) ListTrash(ctx context.Context, userID uuid.UUID) (Listing, error) {
	folders, err := s.store.TrashedFoldersForOwner(ctx, userID)
	if err != nil {
		return Listing{}, err
	}
	records, err := s.store.TrashedFilesForOwner(ctx, userID)
	if err != nil {
		return Listing{}, err
	}
	files := make([]File, 0, len(records))
	for _, record := range records {
		files = append(files, record.File)
	}
	return Listing{Folders: folders, Files: files}, nil
}

// EmptyTrash permanently deletes everything in a user's trash.
func (s *Service) EmptyTrash(ctx context.Context, userID uuid.UUID, ip string) error {
	records, err := s.store.TrashedFilesForOwner(ctx, userID)
	if err != nil {
		return err
	}
	for _, record := range records {
		if err := s.destroy(ctx, userID, record, ip); err != nil {
			return err
		}
	}
	folders, err := s.store.TrashedFoldersForOwner(ctx, userID)
	if err != nil {
		return err
	}
	for _, folder := range folders {
		// A trashed folder inside another may already have gone with its parent.
		if err := s.DeleteFolder(ctx, userID, folder.ID, ip); err != nil && !errors.Is(err, ErrFolderNotFound) {
			return err
		}
	}
	return nil
}
