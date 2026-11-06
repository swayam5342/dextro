package files

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/provider"
)

// connectionLock stops two uploads at once from each making the same Drive folder.
func (s *Service) connectionLock(connectionID uuid.UUID) *sync.Mutex {
	lock, _ := s.locks.LoadOrStore(connectionID, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

// driveFolder returns an account's folder for a Tributary folder, making it and any
// parents that are missing. Empty means Tributary's own folder, the top level.
func (s *Service) driveFolder(ctx context.Context, userID, connectionID uuid.UUID, stored provider.Provider, folderID *uuid.UUID) (string, error) {
	if folderID == nil {
		return "", nil
	}
	found, err := s.store.FolderObjectForOwner(ctx, userID, *folderID, connectionID)
	if err != nil || found != "" {
		return found, err
	}
	folder, err := s.store.FolderForOwner(ctx, userID, *folderID)
	if err != nil {
		return "", err
	}
	parent, err := s.driveFolder(ctx, userID, connectionID, stored, folder.ParentID)
	if err != nil {
		return "", err
	}

	lock := s.connectionLock(connectionID)
	lock.Lock()
	defer lock.Unlock()
	// Someone else may have made it while this waited.
	found, err = s.store.FolderObjectForOwner(ctx, userID, *folderID, connectionID)
	if err != nil || found != "" {
		return found, err
	}
	created, err := stored.CreateFolder(ctx, parent, folder.Name)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	if err := s.store.SetFolderObject(ctx, *folderID, connectionID, created); err != nil {
		return "", err
	}
	return created, nil
}

// mirrorNewFolder makes a folder in every linked account. An account that can't be
// reached doesn't stop it: the row left waiting tells the next sync to finish the job.
func (s *Service) mirrorNewFolder(ctx context.Context, userID uuid.UUID, folder Folder) error {
	accounts, err := s.providers.AccountsFor(ctx, userID)
	if err != nil {
		return err
	}
	var wg sync.WaitGroup
	for _, account := range accounts {
		if err := s.store.AddPendingFolderObject(ctx, folder.ID, account.ID); err != nil {
			return err
		}
		wg.Go(func() {
			stored, err := s.providers.Provider(ctx, userID, account.ID)
			if err == nil {
				_, _ = s.driveFolder(ctx, userID, account.ID, stored, &folder.ID)
			}
		})
	}
	wg.Wait()
	return nil
}

// mirrorFolderChange applies a change to a folder in every account that has it. One
// that fails is marked stale, and the next sync catches it up.
func (s *Service) mirrorFolderChange(ctx context.Context, userID, folderID uuid.UUID, change func(stored provider.Provider, connectionID uuid.UUID, objectID string) error) error {
	rows, err := s.store.FolderObjectsForFolder(ctx, userID, folderID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		stored, err := s.providers.Provider(ctx, userID, row.connectionID)
		if err == nil {
			err = change(stored, row.connectionID, row.objectID)
		}
		if err != nil {
			if err := s.store.MarkFolderObjectStale(ctx, folderID, row.connectionID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Service) mirrorFolderMove(ctx context.Context, userID, folderID uuid.UUID, target *uuid.UUID) error {
	return s.mirrorFolderChange(ctx, userID, folderID, func(stored provider.Provider, connectionID uuid.UUID, objectID string) error {
		parent, err := s.driveFolder(ctx, userID, connectionID, stored, target)
		if err != nil {
			return err
		}
		return stored.Move(ctx, objectID, parent)
	})
}

// changeFileInDrive runs a change on a file's object, unless it's someone's own Drive
// file, which Tributary doesn't touch. It comes first so that a change Drive refuses
// isn't made in Tributary either, and the two can't drift apart.
func (s *Service) changeFileInDrive(ctx context.Context, userID uuid.UUID, record fileRecord, change func(stored provider.Provider) error) error {
	if record.External {
		return nil
	}
	stored, err := s.openAccount(ctx, userID, record.ConnectionID)
	if err != nil {
		return err
	}
	err = change(stored)
	if errors.Is(err, provider.ErrObjectNotFound) {
		return ErrObjectMissing
	}
	if err != nil && !errors.Is(err, ErrProviderFailed) {
		return fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return err
}

// trashDriveFolders sends a folder to each account's trash. Not deleted outright:
// that would take its contents with it, including anything added by hand in Drive,
// which Tributary can't see.
func (s *Service) trashDriveFolders(ctx context.Context, userID, folderID uuid.UUID) error {
	rows, err := s.store.FolderObjectsForFolder(ctx, userID, folderID)
	if err != nil {
		return err
	}
	for _, row := range rows {
		stored, err := s.providers.Provider(ctx, userID, row.connectionID)
		if err != nil {
			return fmt.Errorf("%w: %w", ErrProviderFailed, err)
		}
		if err := stored.Trash(ctx, row.objectID); err != nil && !errors.Is(err, provider.ErrObjectNotFound) {
			return fmt.Errorf("%w: %w", ErrProviderFailed, err)
		}
	}
	return nil
}
