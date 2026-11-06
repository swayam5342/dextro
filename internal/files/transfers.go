package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/audit"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/storage"
)

const (
	// maxTransferItems bounds what one request can name.
	maxTransferItems = 200
	// transferChunkBytes is how much is sent between saves of the progress. It streams,
	// so this doesn't cost memory, and it has to be a multiple of what Drive insists on.
	transferChunkBytes = 32 << 20
	// transferRetries is how many times in a row a copy can fail to get anywhere.
	transferRetries = 4
	// transferRunLimit is how long one run keeps taking new files. A file already going
	// is finished, however long that takes.
	transferRunLimit = 20 * time.Minute
	// removalRetryAfter is how long a copy that wouldn't delete is left alone.
	removalRetryAfter = 10 * time.Minute
)

// Stored values, which the handler turns into words.
const (
	failureSourceMissing = "source_missing"
	failureSource        = "source_unavailable"
	failureTarget        = "target_unavailable"
	failureCopy          = "copy_failed"
	failureVerify        = "verify_failed"
	failureShared        = "file_shared"
)

// Errors for moving files between accounts.
var (
	ErrInvalidTransfer  = errors.New("invalid transfer request")
	ErrTransferNotFound = errors.New("transfer not found")
	ErrExternalFile     = errors.New("file was added from drive")
	// errTransferGone means there's nothing left to do: the file went, moved some other
	// way, or its owner cancelled.
	errTransferGone = errors.New("transfer no longer wanted")
)

// transferFailure is a move that failed in a way its owner should hear about.
type transferFailure struct {
	code string
	err  error
}

func (f *transferFailure) Error() string { return f.code + ": " + f.err.Error() }
func (f *transferFailure) Unwrap() error { return f.err }

func failed(code string, err error) error {
	return &transferFailure{code: code, err: err}
}

// Transfer is a file moving between accounts, as its owner sees it. Failure is empty
// while it's waiting.
type Transfer struct {
	ID                 uuid.UUID
	FileID             uuid.UUID
	Name               string
	Size               int64
	Copied             int64
	SourceConnectionID uuid.UUID
	TargetConnectionID uuid.UUID
	Failure            string
}

// AccountFiles says what an account holds. External files are the ones that can't move.
type AccountFiles struct {
	ConnectionID uuid.UUID
	Files        int64
	External     int64
}

// TransferQueue is everything moving for one user.
type TransferQueue struct {
	Transfers    []Transfer
	Waiting      int64
	WaitingBytes int64
	Failed       int64
	Removing     int64
	Accounts     []AccountFiles
}

// TransferResult says how a request went. Skipped files stay where they are.
type TransferResult struct {
	Queued  int
	Skipped int
}

// TransferRun says what one run of the background job did.
type TransferRun struct {
	Moved    int
	Failed   int
	Removed  int
	Failures []error
}

// TransferFiles queues files to move to an account.
func (s *Service) TransferFiles(ctx context.Context, userID uuid.UUID, ids []uuid.UUID, target uuid.UUID, ip string) (TransferResult, error) {
	if len(ids) == 0 || len(ids) > maxTransferItems {
		return TransferResult{}, ErrInvalidTransfer
	}
	// One already queued holds its space, and counting it again could report no room.
	waiting, err := s.store.WaitingTransferFiles(ctx, userID)
	if err != nil {
		return TransferResult{}, err
	}
	var result TransferResult
	moving := make([]storage.TransferFile, 0, len(ids))
	for _, id := range ids {
		record, err := liveFile(ctx, s.store, userID, id)
		if err != nil {
			return TransferResult{}, err
		}
		if record.External {
			return TransferResult{}, ErrExternalFile
		}
		// A move gives a file a new address, so its link would silently stop working.
		if _, shared, err := s.store.ShareForOwner(ctx, userID, id); err != nil {
			return TransferResult{}, err
		} else if shared {
			return TransferResult{}, ErrFileShared
		}
		if record.ConnectionID == target || waiting[id] {
			result.Skipped++
			continue
		}
		moving = append(moving, storage.TransferFile{ID: record.ID, Source: record.ConnectionID, Size: record.Size})
	}
	if len(moving) == 0 {
		return result, nil
	}
	queued, err := s.providers.ReserveTransfers(ctx, userID, storage.TransferRequest{Target: &target, Files: moving})
	if err != nil {
		return TransferResult{}, err
	}
	result.Queued = queued
	result.Skipped += len(moving) - queued
	return result, s.store.Audit().Record(ctx, audit.Event{
		ActorID:  userID,
		Action:   audit.ActionFilesTransferred,
		IP:       ip,
		Metadata: map[string]any{"target_connection_id": target.String(), "queued": queued},
	})
}

// EmptyConnection queues everything in an account, the trash included, and keeps new
// files out of it. It's refused if they won't all fit. Files added from Drive stay:
// they're the person's own, and they're already in the place they belong.
func (s *Service) EmptyConnection(ctx context.Context, userID, connectionID uuid.UUID, ip string) (TransferResult, error) {
	stored, err := s.providers.Provider(ctx, userID, connectionID)
	if err != nil {
		return TransferResult{}, err
	}
	// Nothing can be copied out of an account that doesn't answer.
	if err := stored.Check(ctx); err != nil {
		return TransferResult{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	links, err := s.store.CountSharesForConnection(ctx, userID, connectionID)
	if err != nil {
		return TransferResult{}, err
	}
	if links > 0 {
		return TransferResult{}, ErrFileShared
	}
	objects, err := s.store.ObjectsForConnection(ctx, userID, connectionID)
	if err != nil {
		return TransferResult{}, err
	}
	waiting, err := s.store.WaitingTransferFiles(ctx, userID)
	if err != nil {
		return TransferResult{}, err
	}
	var result TransferResult
	moving := make([]storage.TransferFile, 0, len(objects))
	for _, record := range objects {
		switch {
		case record.External:
			result.Skipped++
		case !waiting[record.ID]:
			moving = append(moving, storage.TransferFile{ID: record.ID, Source: connectionID, Size: record.Size})
		}
	}
	result.Queued, err = s.providers.ReserveTransfers(ctx, userID, storage.TransferRequest{Drain: &connectionID, Files: moving})
	if err != nil {
		return TransferResult{}, err
	}
	return result, s.store.Audit().Record(ctx, audit.Event{
		ActorID:  userID,
		Action:   audit.ActionStorageEmptied,
		IP:       ip,
		Metadata: map[string]any{"connection_id": connectionID.String(), "queued": result.Queued},
	})
}

// StopEmptying puts an account back in use and drops what hasn't moved yet. A copy in
// progress is thrown away. Files that have moved stay moved.
func (s *Service) StopEmptying(ctx context.Context, userID, connectionID uuid.UUID) error {
	if err := s.providers.StopDraining(ctx, userID, connectionID); err != nil {
		return err
	}
	return s.store.DeleteTransfersFromConnectionForOwner(ctx, userID, connectionID)
}

// ListTransfers says what's moving.
func (s *Service) ListTransfers(ctx context.Context, userID uuid.UUID) (TransferQueue, error) {
	return s.store.TransferQueueForOwner(ctx, userID)
}

// RetryTransfers puts every failed move back in the queue.
func (s *Service) RetryTransfers(ctx context.Context, userID uuid.UUID) error {
	return s.store.RetryTransfersForOwner(ctx, userID)
}

// CancelTransfer leaves a file where it is. It also clears one that failed.
func (s *Service) CancelTransfer(ctx context.Context, userID, id uuid.UUID) error {
	return s.store.DeleteTransferForOwner(ctx, userID, id)
}

// RunTransfers moves what's waiting, one file at a time, and deletes old copies that
// are due. It's the background job's, so it goes through every user's queue.
func (s *Service) RunTransfers(ctx context.Context) (TransferRun, error) {
	var run TransferRun
	stop := time.Now().Add(transferRunLimit)
	for {
		if err := s.finishRemovals(ctx, &run); err != nil {
			return run, err
		}
		next, found, err := s.store.NextTransfer(ctx)
		if err != nil || !found {
			return run, err
		}
		if err := s.runTransfer(ctx, next, &run); err != nil {
			return run, err
		}
		if time.Now().After(stop) {
			return run, nil
		}
	}
}

func (s *Service) runTransfer(ctx context.Context, t transferRecord, run *TransferRun) error {
	err := s.moveFile(ctx, t)
	var failure *transferFailure
	switch {
	case err == nil:
		run.Moved++
		return nil
	case ctx.Err() != nil:
		// Shutting down. It's still waiting, so the next run starts it again.
		return ctx.Err()
	case errors.Is(err, errTransferGone):
		return s.store.DeleteTransfer(ctx, t.ID)
	case errors.As(err, &failure):
		run.Failed++
		run.Failures = append(run.Failures, fmt.Errorf("transfer %s: %w", t.ID, err))
		return s.store.FailTransfer(ctx, t.ID, failure.code)
	default:
		return err
	}
}

// moveFile copies a file, proves the copy, and only then points the record at it. Up
// to that moment the old copy is untouched, so failing anywhere loses nothing.
func (s *Service) moveFile(ctx context.Context, t transferRecord) error {
	record, err := s.store.FileForOwner(ctx, t.UserID, t.FileID)
	if errors.Is(err, ErrFileNotFound) {
		return errTransferGone
	}
	if err != nil {
		return err
	}
	if record.ConnectionID != t.SourceConnectionID || record.External {
		return errTransferGone
	}
	if _, shared, err := s.store.ShareForOwner(ctx, t.UserID, t.FileID); err != nil {
		return err
	} else if shared {
		return failed(failureShared, ErrFileShared)
	}
	from, err := s.providers.Provider(ctx, t.UserID, t.SourceConnectionID)
	if err != nil {
		return failed(failureSource, err)
	}
	to, err := s.providers.Provider(ctx, t.UserID, t.TargetConnectionID)
	if err != nil {
		return failed(failureTarget, err)
	}
	source, err := from.Describe(ctx, record.objectID)
	if errors.Is(err, provider.ErrObjectNotFound) {
		return failed(failureSourceMissing, err)
	}
	if err != nil {
		return failed(failureSource, err)
	}
	parent, err := s.driveFolder(ctx, t.UserID, t.TargetConnectionID, to, record.FolderID)
	if err != nil {
		return failed(failureTarget, err)
	}
	copied, err := s.copyObject(ctx, t, record, from, to, source, parent)
	if err != nil {
		return err
	}
	if err := checkCopy(ctx, to, source, copied); err != nil {
		return errors.Join(err, discard(ctx, to, copied.ID))
	}
	err = s.store.FinishTransfer(ctx, t, record.objectID, copied.ID)
	// Only when the answer is certain. A commit that reports failure may still have gone
	// through, and then this is the file's only copy. A spare copy is the safe mistake:
	// the worst a sync does with it is show a duplicate.
	if errors.Is(err, errTransferGone) {
		return errors.Join(err, discard(ctx, to, copied.ID))
	}
	return err
}

// checkCopy asks the account what it really has. Checksums come from each account, so
// a match proves the whole trip. An account that doesn't give one leaves only the size.
func checkCopy(ctx context.Context, to provider.Provider, source, copied provider.Object) error {
	stored, err := to.Describe(ctx, copied.ID)
	if err != nil {
		return failed(failureVerify, err)
	}
	if stored.Size != source.Size {
		return failed(failureVerify, fmt.Errorf("size %d, want %d", stored.Size, source.Size))
	}
	if stored.Checksum != "" && source.Checksum != "" && stored.Checksum != source.Checksum {
		return failed(failureVerify, errors.New("checksums differ"))
	}
	return nil
}

// discard throws away a copy nothing will use, even while shutting down: a sync would
// otherwise bring it back as a file.
func discard(ctx context.Context, to provider.Provider, id string) error {
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()
	return deleteObject(cleanup, to, id)
}

func (s *Service) copyObject(ctx context.Context, t transferRecord, record fileRecord, from, to provider.Provider, source provider.Object, parent string) (provider.Object, error) {
	if source.Size == 0 {
		object, err := to.Put(ctx, provider.Upload{Name: record.Name, ContentType: record.ContentType, Parent: parent, Body: strings.NewReader("")})
		if err != nil {
			return provider.Object{}, failed(failureTarget, err)
		}
		return object, nil
	}
	session, err := to.BeginResumable(ctx, provider.Resumable{Name: record.Name, ContentType: record.ContentType, Size: source.Size, Parent: parent})
	if err != nil {
		return provider.Object{}, failed(failureTarget, err)
	}
	object, err := s.sendChunks(ctx, t, from, to, source, session)
	if err != nil {
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		return provider.Object{}, errors.Join(err, to.CancelResumable(cleanup, session))
	}
	return object, nil
}

// sendChunks streams a file across. The stream stays open between chunks, because
// opening one costs about a second at Google. It's reopened only when the account kept
// something other than what it was sent, since the next chunk has to start from there.
func (s *Service) sendChunks(ctx context.Context, t transferRecord, from, to provider.Provider, source provider.Object, session string) (provider.Object, error) {
	var stream io.ReadCloser
	closeStream := func() {
		if stream != nil {
			_ = stream.Close()
			stream = nil
		}
	}
	defer closeStream()

	send := func(offset, length int64) (provider.ResumeState, error) {
		if stream == nil {
			opened, err := from.Get(ctx, source.ID, &provider.ByteRange{Start: offset, End: source.Size - 1})
			if err != nil {
				return provider.ResumeState{}, err
			}
			stream = opened
		}
		return to.PutChunk(ctx, session, provider.Chunk{Offset: offset, Length: length, Total: source.Size, Body: io.LimitReader(stream, length)})
	}

	offset, stuck := int64(0), 0
	for {
		length := min(s.transferChunk, source.Size-offset)
		state, err := send(offset, length)
		if err != nil {
			closeStream()
			stuck++
			if ctx.Err() != nil {
				return provider.Object{}, ctx.Err()
			}
			if stuck >= transferRetries || errors.Is(err, provider.ErrSessionExpired) {
				return provider.Object{}, failed(failureCopy, err)
			}
			select {
			case <-time.After(s.transferWait << stuck):
			case <-ctx.Done():
				return provider.Object{}, ctx.Err()
			}
			// A failed chunk can still have partly arrived, so ask.
			status, statusErr := to.ResumableStatus(ctx, session, source.Size)
			if statusErr != nil {
				continue
			}
			if status.Object != nil {
				return *status.Object, nil
			}
			offset = status.Received
			continue
		}
		if state.Object != nil {
			return *state.Object, nil
		}
		if state.Received != offset+length {
			closeStream()
		}
		if state.Received > offset {
			stuck = 0
		}
		offset = state.Received
		if err := s.store.SetTransferCopied(ctx, t.ID, offset); err != nil {
			return provider.Object{}, err
		}
	}
}

// finishRemovals deletes old copies of moved files. The record already points at the
// new copy, which has been proved, so this is the one permanent delete that isn't a
// person's explicit choice: the file isn't going anywhere, only a spare copy of it.
func (s *Service) finishRemovals(ctx context.Context, run *TransferRun) error {
	due, err := s.store.DueRemovals(ctx)
	if err != nil {
		return err
	}
	for _, removal := range due {
		// Deleting can't be undone, so nothing a record points at goes, whatever led here.
		recorded, err := s.store.IsObjectRecorded(ctx, removal.connectionID, removal.objectID)
		if err != nil {
			return err
		}
		if !recorded {
			err = s.removeObject(ctx, removal)
		}
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			run.Failures = append(run.Failures, fmt.Errorf("remove old copy from account %s: %w", removal.connectionID, err))
			if err := s.store.DelayRemoval(ctx, removal.id, time.Now().Add(removalRetryAfter)); err != nil {
				return err
			}
			continue
		}
		if err := s.store.DeleteRemoval(ctx, removal.id); err != nil {
			return err
		}
		if !recorded {
			run.Removed++
		}
	}
	return nil
}

func (s *Service) removeObject(ctx context.Context, removal pendingRemoval) error {
	stored, err := s.providers.Provider(ctx, removal.userID, removal.connectionID)
	if err != nil {
		return err
	}
	return deleteObject(ctx, stored, removal.objectID)
}
