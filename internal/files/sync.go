package files

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/provider"
)

const (
	// syncGrace is how old an object has to be before it's added. An upload stores its
	// object a moment before saving its record, and adding it in that moment would make
	// the upload's own save fail, which would then delete the object.
	syncGrace = 10 * time.Minute
	// importedFallbackName stands in for a name Tributary can't use.
	importedFallbackName = "Recovered file"
	// jobActor stands in for a client address on events from the background jobs.
	jobActor = "job"
)

// SyncResult says what a sync changed. Skipped counts accounts it couldn't reach, and
// Failures says why, for the logs.
type SyncResult struct {
	Added    int
	Removed  int
	Skipped  int
	Failures []error
}

// listing is everything an account lets Tributary see.
type listing struct {
	folders map[string]provider.Object
	files   map[string]provider.Object
}

func listEverything(ctx context.Context, stored provider.Provider) (listing, error) {
	found := listing{folders: map[string]provider.Object{}, files: map[string]provider.Object{}}
	token := ""
	for {
		page, err := stored.List(ctx, token)
		if err != nil {
			return listing{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
		}
		for _, object := range page.Objects {
			if object.Folder {
				found.folders[object.ID] = object
			} else {
				found.files[object.ID] = object
			}
		}
		if token = page.NextPageToken; token == "" {
			return found, nil
		}
	}
}

// syncRun is one sync of one account.
type syncRun struct {
	s            *Service
	userID       uuid.UUID
	connectionID uuid.UUID
	stored       provider.Provider
	cutoff       time.Time
	found        listing
	// leaving is old copies of moved files, which aren't new files however they look.
	leaving map[string]bool
	// mapped says which Tributary folder each of the account's folders is.
	mapped map[string]uuid.UUID
}

// SyncConnection makes Tributary's records match what's really in an account: files and
// folders it has no record of come back, which is how a lost database recovers, and
// records of files that are gone are removed.
//
// Removing is the dangerous half, because a failure can look like an empty account.
// So nothing is removed unless the account answers for its folder, the listing
// reaches the end, and the account confirms that this one object is gone. This only
// removes records of objects already gone, so it doesn't break the rule that deleting
// is an explicit action.
func (s *Service) SyncConnection(ctx context.Context, userID, connectionID uuid.UUID, ip string) (SyncResult, error) {
	stored, err := s.providers.Provider(ctx, userID, connectionID)
	if err != nil {
		return SyncResult{}, err
	}
	// A trashed folder lists as empty, which must not read as everything deleted.
	if err := stored.Check(ctx); err != nil {
		return SyncResult{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	// This comes before reading the account's folders, so that one deleted in Drive
	// is seen as deleted rather than waiting to be made.
	sorted, err := s.store.FoldersSynced(ctx, userID, connectionID)
	if err != nil {
		return SyncResult{}, err
	}
	if !sorted {
		if err := s.store.AddPendingFolderObjectsForConnection(ctx, userID, connectionID); err != nil {
			return SyncResult{}, err
		}
	}
	// Read before listing, so that everything here existed when the listing began.
	known, err := s.store.ObjectsForConnection(ctx, userID, connectionID)
	if err != nil {
		return SyncResult{}, err
	}
	// Also before listing. A move writes its record and this in one step, so an old copy
	// is in one of the two, and a copy listed after its removal finished is already gone.
	leaving, err := s.store.PendingRemovalsForConnection(ctx, userID, connectionID)
	if err != nil {
		return SyncResult{}, err
	}
	rows, err := s.store.FolderObjectsForConnection(ctx, userID, connectionID)
	if err != nil {
		return SyncResult{}, err
	}
	found, err := listEverything(ctx, stored)
	if err != nil {
		return SyncResult{}, err
	}

	run := &syncRun{
		s:            s,
		userID:       userID,
		connectionID: connectionID,
		stored:       stored,
		cutoff:       time.Now().Add(-syncGrace),
		found:        found,
		leaving:      leaving,
		mapped:       make(map[string]uuid.UUID, len(rows)),
	}
	for _, row := range rows {
		if row.objectID != "" {
			run.mapped[row.objectID] = row.folderID
		}
	}

	var result SyncResult
	err = run.importEverything(ctx, known, &result)
	if err == nil {
		err = run.removeGoneFiles(ctx, known, &result)
	}
	if err == nil {
		err = run.removeGoneFolders(ctx, rows)
	}
	if err == nil {
		err = run.catchUp(ctx, known, sorted)
	}
	return result, errors.Join(err, s.recordSync(ctx, userID, connectionID, ip, result))
}

// place finds the Tributary folder for one of the account's folders, adopting any it
// doesn't know yet. False means it's outside Tributary's folder, or too new to judge.
func (r *syncRun) place(ctx context.Context, objectID string) (*uuid.UUID, bool, error) {
	if objectID == r.stored.RootID() {
		return nil, true, nil
	}
	if id, ok := r.mapped[objectID]; ok {
		return &id, true, nil
	}
	folder, ok := r.found.folders[objectID]
	if !ok || folder.ModifiedAt.After(r.cutoff) {
		return nil, false, nil
	}
	parent, inside, err := r.place(ctx, folder.Parent)
	if err != nil || !inside {
		return nil, false, err
	}
	adopted, err := r.s.adoptFolder(ctx, r.userID, parent, folder.Name)
	if err != nil {
		return nil, false, err
	}
	if err := r.s.store.SetFolderObject(ctx, adopted, r.connectionID, objectID); err != nil {
		return nil, false, err
	}
	r.mapped[objectID] = adopted
	return &adopted, true, nil
}

// importedName is what to call something found in an account, whose name may not be one
// Tributary accepts.
func importedName(raw string) string {
	name, err := cleanName(raw)
	if err != nil {
		return importedFallbackName
	}
	return name
}

// adoptFolder finds the Tributary folder at this place or makes it, so that the same
// folder in several accounts becomes one folder.
func (s *Service) adoptFolder(ctx context.Context, userID uuid.UUID, parent *uuid.UUID, rawName string) (uuid.UUID, error) {
	name := importedName(rawName)
	var id uuid.UUID
	err := s.store.InTx(ctx, func(tx *Store) error {
		if err := tx.LockFolderTreeForOwner(ctx, userID); err != nil {
			return err
		}
		existing, err := tx.FindFolderByName(ctx, userID, parent, name)
		if err != nil {
			return err
		}
		if existing != nil {
			id = existing.ID
			return nil
		}
		created, err := tx.CreateFolder(ctx, userID, parent, name)
		id = created.ID
		return err
	})
	return id, err
}

func (r *syncRun) importEverything(ctx context.Context, known map[string]fileRecord, result *SyncResult) error {
	for id := range r.found.folders {
		if _, _, err := r.place(ctx, id); err != nil {
			return err
		}
	}
	for id, object := range r.found.files {
		if _, ok := known[id]; ok || r.leaving[id] || object.ModifiedAt.After(r.cutoff) {
			continue
		}
		folder, inside, err := r.place(ctx, object.Parent)
		if err != nil {
			return err
		}
		// Outside Tributary's folder is a file someone added and then removed from Tributary.
		if !inside {
			continue
		}
		added, err := r.s.importObject(ctx, r.userID, r.connectionID, object, folder, false)
		if err != nil {
			return err
		}
		if added {
			result.Added++
		}
	}
	return nil
}

func (r *syncRun) removeGoneFiles(ctx context.Context, known map[string]fileRecord, result *SyncResult) error {
	for objectID, record := range known {
		if _, seen := r.found.files[objectID]; seen {
			continue
		}
		// A listing can lag behind a new file, so being missing from it proves nothing.
		exists, err := r.stored.Exists(ctx, objectID)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrProviderFailed, err)
		}
		if exists {
			continue
		}
		// By its object, because a file that moved during this sync left this one behind.
		err = r.s.store.DeleteFileForObject(ctx, r.userID, record.ID, r.connectionID, objectID)
		if errors.Is(err, ErrFileNotFound) {
			continue
		}
		if err != nil {
			return err
		}
		result.Removed++
	}
	return nil
}

// removeGoneFolders forgets folders deleted in Drive. A folder leaves Tributary once it's
// empty and no account has it, and that can free its parent too.
func (r *syncRun) removeGoneFolders(ctx context.Context, rows []folderObject) error {
	for _, row := range rows {
		if row.objectID == "" {
			continue
		}
		if _, seen := r.found.folders[row.objectID]; seen {
			continue
		}
		exists, err := r.stored.Exists(ctx, row.objectID)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrProviderFailed, err)
		}
		if exists {
			continue
		}
		if err := r.s.store.DeleteFolderObject(ctx, row.folderID, r.connectionID); err != nil {
			return err
		}
		if err := r.s.deleteUnusedFolders(ctx, r.userID, row.folderID); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) deleteUnusedFolders(ctx context.Context, userID, folderID uuid.UUID) error {
	for {
		folder, err := s.store.FolderForOwner(ctx, userID, folderID)
		if errors.Is(err, ErrFolderNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		deleted, err := s.store.DeleteFolderIfUnused(ctx, userID, folderID)
		if err != nil || !deleted || folder.ParentID == nil {
			return err
		}
		folderID = *folder.ParentID
	}
}

// catchUp makes what an account missed: a first sort into real folders, folders that
// were waiting, and changes that didn't reach it.
func (r *syncRun) catchUp(ctx context.Context, known map[string]fileRecord, sorted bool) error {
	store := r.s.store
	rows, err := store.FolderObjectsForConnection(ctx, r.userID, r.connectionID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.objectID == "" {
			if _, err := r.s.driveFolder(ctx, r.userID, r.connectionID, r.stored, &row.folderID); err != nil {
				return err
			}
		}
		if row.stale {
			if err := r.finishStale(ctx, row); err != nil {
				return err
			}
		}
	}
	if sorted {
		return nil
	}
	for objectID, record := range known {
		if err := r.sortFile(ctx, objectID, record); err != nil {
			return err
		}
	}
	return store.SetFoldersSynced(ctx, r.userID, r.connectionID)
}

func (r *syncRun) finishStale(ctx context.Context, row folderObject) error {
	folder, err := r.s.store.FolderForOwner(ctx, r.userID, row.folderID)
	if err != nil {
		return err
	}
	parent, err := r.s.driveFolder(ctx, r.userID, r.connectionID, r.stored, folder.ParentID)
	if err != nil {
		return err
	}
	if err := r.stored.Rename(ctx, row.objectID, folder.Name); err != nil {
		return fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	if err := r.stored.Move(ctx, row.objectID, parent); err != nil {
		return fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return r.s.store.ClearFolderObjectStale(ctx, row.folderID, r.connectionID)
}

// sortFile puts a file from before real folders where Tributary shows it. This happens
// once per account, so that a change made by hand in Drive later isn't undone.
func (r *syncRun) sortFile(ctx context.Context, objectID string, record fileRecord) error {
	object, listed := r.found.files[objectID]
	if !listed || record.External {
		return nil
	}
	parent, err := r.s.driveFolder(ctx, r.userID, r.connectionID, r.stored, record.FolderID)
	if err != nil {
		return err
	}
	want := parent
	if want == "" {
		want = r.stored.RootID()
	}
	if object.Parent != want {
		if err := r.stored.Move(ctx, objectID, parent); err != nil {
			return fmt.Errorf("%w: %w", ErrProviderFailed, err)
		}
	}
	if object.Name != record.Name {
		if err := r.stored.Rename(ctx, objectID, record.Name); err != nil {
			return fmt.Errorf("%w: %w", ErrProviderFailed, err)
		}
	}
	return nil
}

func (s *Service) importObject(ctx context.Context, userID, connectionID uuid.UUID, object provider.Object, folder *uuid.UUID, external bool) (bool, error) {
	return s.store.ImportFile(ctx, userID, fileRecord{
		File: File{
			ConnectionID: connectionID,
			FolderID:     folder,
			Name:         importedName(object.Name),
			Size:         object.Size,
			ContentType:  cleanContentType(object.ContentType),
			External:     external,
		},
		objectID: object.ID,
	})
}

func (s *Service) recordSync(ctx context.Context, userID, connectionID uuid.UUID, ip string, result SyncResult) error {
	if result.Added == 0 && result.Removed == 0 {
		return nil
	}
	return s.store.Audit().Record(ctx, audit.Event{
		ActorID: userID,
		Action:  audit.ActionStorageSynced,
		IP:      ip,
		Metadata: map[string]any{
			"connection_id": connectionID.String(),
			"added":         result.Added,
			"removed":       result.Removed,
		},
	})
}

// SyncAll syncs every linked account. One that can't be reached is skipped: its
// token may have expired, which shouldn't stop anyone else's.
func (s *Service) SyncAll(ctx context.Context) (SyncResult, error) {
	accounts, err := s.providers.AllAccounts(ctx)
	if err != nil {
		return SyncResult{}, err
	}
	var total SyncResult
	for _, account := range accounts {
		result, err := s.SyncConnection(ctx, account.UserID, account.ID, jobActor)
		total.Added += result.Added
		total.Removed += result.Removed
		if ctx.Err() != nil {
			return total, ctx.Err()
		}
		if err != nil {
			total.Skipped++
			total.Failures = append(total.Failures, fmt.Errorf("account %s: %w", account.ID, err))
		}
	}
	return total, nil
}
