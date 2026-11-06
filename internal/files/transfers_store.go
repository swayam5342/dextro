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

// transferRecord is a queued move, with the owner the job needs to act for them.
type transferRecord struct {
	ID                 uuid.UUID
	UserID             uuid.UUID
	FileID             uuid.UUID
	SourceConnectionID uuid.UUID
	TargetConnectionID uuid.UUID
}

// pendingRemoval is an old copy still to be deleted.
type pendingRemoval struct {
	id           uuid.UUID
	userID       uuid.UUID
	connectionID uuid.UUID
	objectID     string
}

// NextTransfer returns the oldest move waiting. It is unscoped on purpose and is for
// the background job only.
func (s *Store) NextTransfer(ctx context.Context) (transferRecord, bool, error) {
	row, err := s.queries.NextFileTransfer(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return transferRecord{}, false, nil
	}
	if err != nil {
		return transferRecord{}, false, fmt.Errorf("next transfer: %w", err)
	}
	return transferRecord{
		ID:                 row.ID,
		UserID:             row.UserID,
		FileID:             row.FileID,
		SourceConnectionID: row.SourceConnectionID,
		TargetConnectionID: row.TargetConnectionID,
	}, true, nil
}

// SetTransferCopied saves how far a copy has got. A row that's gone means its owner
// cancelled, or the file went, so the copy should stop.
func (s *Store) SetTransferCopied(ctx context.Context, id uuid.UUID, copied int64) error {
	rows, err := s.queries.SetFileTransferCopied(ctx, dbgen.SetFileTransferCopiedParams{ID: id, Copied: copied})
	if err != nil {
		return fmt.Errorf("save transfer progress: %w", err)
	}
	if rows == 0 {
		return errTransferGone
	}
	return nil
}

// FailTransfer records why a move stopped, which also gives its space back.
func (s *Store) FailTransfer(ctx context.Context, id uuid.UUID, code string) error {
	if err := s.queries.FailFileTransfer(ctx, dbgen.FailFileTransferParams{ID: id, Failure: code}); err != nil {
		return fmt.Errorf("fail transfer: %w", err)
	}
	return nil
}

// DeleteTransfer drops a move nobody wants any more.
func (s *Store) DeleteTransfer(ctx context.Context, id uuid.UUID) error {
	if _, err := s.queries.DeleteFileTransfer(ctx, id); err != nil {
		return fmt.Errorf("delete transfer: %w", err)
	}
	return nil
}

// FinishTransfer points a file at its new copy, in one step with noting that the old
// copy is owed a delete. A crash can't leave one without the other, so an old copy is
// never mistaken for a new file, and is never forgotten.
func (s *Store) FinishTransfer(ctx context.Context, t transferRecord, oldObject, newObject string) error {
	return s.InTx(ctx, func(tx *Store) error {
		rows, err := tx.queries.DeleteFileTransfer(ctx, t.ID)
		if err != nil {
			return fmt.Errorf("finish transfer: %w", err)
		}
		if rows == 0 {
			return errTransferGone
		}
		rows, err = tx.queries.SwapFileObjectForOwner(ctx, dbgen.SwapFileObjectForOwnerParams{
			TargetConnectionID: t.TargetConnectionID,
			NewObjectID:        newObject,
			UserID:             t.UserID,
			ID:                 t.FileID,
			SourceConnectionID: t.SourceConnectionID,
			OldObjectID:        oldObject,
		})
		if err != nil {
			return fmt.Errorf("swap object: %w", err)
		}
		if rows == 0 {
			return errTransferGone
		}
		err = tx.queries.CreatePendingRemoval(ctx, dbgen.CreatePendingRemovalParams{
			UserID:       t.UserID,
			ConnectionID: t.SourceConnectionID,
			ObjectID:     oldObject,
		})
		if err != nil {
			return fmt.Errorf("note old copy: %w", err)
		}
		return nil
	})
}

// DeleteTransferForOwner cancels a move.
func (s *Store) DeleteTransferForOwner(ctx context.Context, userID, id uuid.UUID) error {
	rows, err := s.queries.DeleteFileTransferForOwner(ctx, dbgen.DeleteFileTransferForOwnerParams{UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("delete transfer: %w", err)
	}
	if rows == 0 {
		return ErrTransferNotFound
	}
	return nil
}

// DeleteTransfersFromConnectionForOwner drops every move out of an account.
func (s *Store) DeleteTransfersFromConnectionForOwner(ctx context.Context, userID, connectionID uuid.UUID) error {
	_, err := s.queries.DeleteFileTransfersFromConnectionForOwner(ctx, dbgen.DeleteFileTransfersFromConnectionForOwnerParams{
		UserID:             userID,
		SourceConnectionID: connectionID,
	})
	if err != nil {
		return fmt.Errorf("delete transfers: %w", err)
	}
	return nil
}

// RetryTransfersForOwner puts failed moves back in the queue.
func (s *Store) RetryTransfersForOwner(ctx context.Context, userID uuid.UUID) error {
	if _, err := s.queries.RetryFileTransfersForOwner(ctx, userID); err != nil {
		return fmt.Errorf("retry transfers: %w", err)
	}
	return nil
}

// WaitingTransferFiles says which of a user's files are already queued.
func (s *Store) WaitingTransferFiles(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]bool, error) {
	ids, err := s.queries.ListWaitingTransferFilesForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list waiting transfers: %w", err)
	}
	waiting := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		waiting[id] = true
	}
	return waiting, nil
}

// IsFileWaitingToMove says whether one file is queued.
func (s *Store) IsFileWaitingToMove(ctx context.Context, userID, fileID uuid.UUID) (bool, error) {
	waiting, err := s.queries.IsFileWaitingToMoveForOwner(ctx, dbgen.IsFileWaitingToMoveForOwnerParams{UserID: userID, FileID: fileID})
	if err != nil {
		return false, fmt.Errorf("check transfer: %w", err)
	}
	return waiting, nil
}

// TransferQueueForOwner reads everything the Storage page shows.
func (s *Store) TransferQueueForOwner(ctx context.Context, userID uuid.UUID) (TransferQueue, error) {
	rows, err := s.queries.ListFileTransfersForOwner(ctx, userID)
	if err != nil {
		return TransferQueue{}, fmt.Errorf("list transfers: %w", err)
	}
	totals, err := s.queries.GetFileTransferTotalsForOwner(ctx, userID)
	if err != nil {
		return TransferQueue{}, fmt.Errorf("total transfers: %w", err)
	}
	removing, err := s.queries.CountPendingRemovalsForOwner(ctx, userID)
	if err != nil {
		return TransferQueue{}, fmt.Errorf("count removals: %w", err)
	}
	counts, err := s.queries.ListConnectionFileCountsForOwner(ctx, userID)
	if err != nil {
		return TransferQueue{}, fmt.Errorf("count files: %w", err)
	}
	queue := TransferQueue{
		Transfers:    make([]Transfer, 0, len(rows)),
		Waiting:      totals.Waiting,
		WaitingBytes: totals.WaitingBytes,
		Failed:       totals.Failed,
		Removing:     removing,
		Accounts:     make([]AccountFiles, 0, len(counts)),
	}
	for _, row := range rows {
		queue.Transfers = append(queue.Transfers, Transfer{
			ID:                 row.ID,
			FileID:             row.FileID,
			Name:               row.Name,
			Size:               row.Size,
			Copied:             row.Copied,
			SourceConnectionID: row.SourceConnectionID,
			TargetConnectionID: row.TargetConnectionID,
			Failure:            row.Failure,
		})
	}
	for _, row := range counts {
		queue.Accounts = append(queue.Accounts, AccountFiles{ConnectionID: row.ConnectionID, Files: row.Files, External: row.ExternalFiles})
	}
	return queue, nil
}

// DueRemovals returns old copies ready to be deleted. It is unscoped on purpose and is
// for the background job only.
func (s *Store) DueRemovals(ctx context.Context) ([]pendingRemoval, error) {
	rows, err := s.queries.ListDuePendingRemovals(ctx)
	if err != nil {
		return nil, fmt.Errorf("list removals: %w", err)
	}
	removals := make([]pendingRemoval, 0, len(rows))
	for _, row := range rows {
		removals = append(removals, pendingRemoval{id: row.ID, userID: row.UserID, connectionID: row.ConnectionID, objectID: row.ObjectID})
	}
	return removals, nil
}

// DeleteRemoval drops a removal that's done.
func (s *Store) DeleteRemoval(ctx context.Context, id uuid.UUID) error {
	if err := s.queries.DeletePendingRemoval(ctx, id); err != nil {
		return fmt.Errorf("delete removal: %w", err)
	}
	return nil
}

// DelayRemoval leaves a removal for later.
func (s *Store) DelayRemoval(ctx context.Context, id uuid.UUID, until time.Time) error {
	if err := s.queries.DelayPendingRemoval(ctx, dbgen.DelayPendingRemovalParams{ID: id, RetryAt: until}); err != nil {
		return fmt.Errorf("delay removal: %w", err)
	}
	return nil
}

// IsObjectRecorded says whether any file record points at an object.
func (s *Store) IsObjectRecorded(ctx context.Context, connectionID uuid.UUID, objectID string) (bool, error) {
	recorded, err := s.queries.IsObjectRecorded(ctx, dbgen.IsObjectRecordedParams{ConnectionID: connectionID, ObjectID: objectID})
	if err != nil {
		return false, fmt.Errorf("check object: %w", err)
	}
	return recorded, nil
}

// PendingRemovalsForConnection lists the old copies an account still holds, which a
// sync has to leave alone.
func (s *Store) PendingRemovalsForConnection(ctx context.Context, userID, connectionID uuid.UUID) (map[string]bool, error) {
	ids, err := s.queries.ListPendingRemovalObjectsForOwner(ctx, dbgen.ListPendingRemovalObjectsForOwnerParams{UserID: userID, ConnectionID: connectionID})
	if err != nil {
		return nil, fmt.Errorf("list pending removals: %w", err)
	}
	pending := make(map[string]bool, len(ids))
	for _, id := range ids {
		pending[id] = true
	}
	return pending, nil
}

// DeleteFileForObject removes a record only if it still points at this object. A sync
// can be working from an older view, and a file that moved in the meantime is fine.
func (s *Store) DeleteFileForObject(ctx context.Context, userID, id, connectionID uuid.UUID, objectID string) error {
	rows, err := s.queries.DeleteFileForObjectForOwner(ctx, dbgen.DeleteFileForObjectForOwnerParams{
		UserID:       userID,
		ID:           id,
		ConnectionID: connectionID,
		ObjectID:     objectID,
	})
	if err != nil {
		return fmt.Errorf("delete file: %w", err)
	}
	if rows == 0 {
		return ErrFileNotFound
	}
	return nil
}
