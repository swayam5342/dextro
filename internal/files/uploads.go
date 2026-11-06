package files

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/storage"
)

const (
	// maxChunkBytes bounds one request. Chunks stream, so this isn't about memory:
	// it keeps the work lost to a dropped connection small.
	maxChunkBytes = 64 << 20
	// uploadAbandonedAfter is how long an upload can sit idle. It holds its space on
	// the account until then, so this is shorter than Drive's week.
	uploadAbandonedAfter = 24 * time.Hour
)

// Errors for uploads sent in chunks.
var (
	ErrUploadNotFound = errors.New("upload not found")
	ErrUploadExpired  = errors.New("upload expired")
	ErrInvalidUpload  = errors.New("invalid upload")
	ErrInvalidChunk   = errors.New("invalid chunk")
	ErrOffsetMismatch = errors.New("chunk doesn't start where the upload is")
)

// BeginUploadInput describes a file about to be sent in chunks. An empty file has
// nothing to resume, so it goes through Upload instead.
type BeginUploadInput struct {
	ConnectionID *uuid.UUID
	FolderID     *uuid.UUID
	Name         string
	ContentType  string
	Size         int64
	LastModified int64
}

// UploadSession is an upload sent in chunks, as its owner sees it. File is set once
// it has finished.
type UploadSession struct {
	ID           uuid.UUID
	ConnectionID uuid.UUID
	FolderID     *uuid.UUID
	Name         string
	Size         int64
	Received     int64
	LastModified int64
	CreatedAt    time.Time
	File         *File
}

// BeginUpload chooses an account, holds the space on it, and opens a session.
func (s *Service) BeginUpload(ctx context.Context, userID uuid.UUID, in BeginUploadInput) (UploadSession, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return UploadSession{}, err
	}
	if in.Size <= 0 {
		return UploadSession{}, ErrInvalidUpload
	}
	if _, err := requireLiveFolder(ctx, s.store, userID, in.FolderID); err != nil {
		return UploadSession{}, err
	}
	limit, err := s.store.Settings().MaxUploadBytes(ctx)
	if err != nil {
		return UploadSession{}, err
	}
	if in.Size > limit {
		return UploadSession{}, ErrFileTooLarge
	}

	id := uuid.New()
	contentType := cleanContentType(in.ContentType)
	chosen, err := s.providers.Reserve(ctx, userID, in.ConnectionID, storage.Reservation{
		ID:           id,
		FolderID:     in.FolderID,
		Name:         name,
		ContentType:  contentType,
		Size:         in.Size,
		LastModified: in.LastModified,
	})
	if err != nil {
		return UploadSession{}, err
	}
	var address string
	parent, err := s.driveFolder(ctx, userID, chosen.ConnectionID, chosen.Provider, in.FolderID)
	if err == nil {
		address, err = chosen.Provider.BeginResumable(ctx, provider.Resumable{Name: name, ContentType: contentType, Size: in.Size, Parent: parent})
	}
	if err == nil {
		err = s.store.SetUploadSecretForOwner(ctx, userID, id, s.providers.SealUploadSession(id, address))
	}
	if err != nil {
		// Without an address the reservation only wastes space.
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		return UploadSession{}, errors.Join(fmt.Errorf("%w: %w", ErrProviderFailed, err), s.store.DeleteUnfinishedUploadForOwner(cleanup, userID, id))
	}
	return UploadSession{
		ID:           id,
		ConnectionID: chosen.ConnectionID,
		FolderID:     in.FolderID,
		Name:         name,
		Size:         in.Size,
		LastModified: in.LastModified,
		CreatedAt:    time.Now(),
	}, nil
}

// ListUploads returns a user's unfinished uploads, for picking up after a reload.
func (s *Service) ListUploads(ctx context.Context, userID uuid.UUID) ([]UploadSession, error) {
	records, err := s.store.ListUploadSessionsForOwner(ctx, userID)
	if err != nil {
		return nil, err
	}
	sessions := make([]UploadSession, 0, len(records))
	for _, r := range records {
		sessions = append(sessions, r.UploadSession)
	}
	return sessions, nil
}

// open reaches the account an upload is going to.
func (s *Service) openUpload(ctx context.Context, userID uuid.UUID, r uploadRecord) (provider.Provider, string, error) {
	if r.sealed == nil {
		return nil, "", ErrUploadExpired
	}
	stored, err := s.openAccount(ctx, userID, r.ConnectionID)
	if err != nil {
		return nil, "", err
	}
	address, err := s.providers.OpenUploadSession(r.ID, *r.sealed)
	if err != nil {
		return nil, "", err
	}
	return stored, address, nil
}

// settle saves what the account says about an upload. The account is the only
// one who knows how much it kept, and it may have finished the file even if the
// browser never heard.
func (s *Service) settle(ctx context.Context, userID uuid.UUID, r uploadRecord, stored provider.Provider, state provider.ResumeState) (UploadSession, error) {
	if state.Object != nil {
		return s.finishUpload(ctx, userID, r, stored, *state.Object)
	}
	if err := s.store.SetUploadReceivedForOwner(ctx, userID, r.ID, state.Received); err != nil {
		return UploadSession{}, err
	}
	session := r.UploadSession
	session.Received = state.Received
	return session, nil
}

func (s *Service) expireUpload(ctx context.Context, userID uuid.UUID, id uuid.UUID) error {
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()
	return errors.Join(ErrUploadExpired, s.store.DeleteUnfinishedUploadForOwner(cleanup, userID, id))
}

// UploadProgress asks the account how far an upload has really got.
func (s *Service) UploadProgress(ctx context.Context, userID, id uuid.UUID) (UploadSession, error) {
	r, err := s.store.UploadSessionForOwner(ctx, userID, id)
	if err != nil {
		return UploadSession{}, err
	}
	if r.File != nil {
		return r.UploadSession, nil
	}
	stored, address, err := s.openUpload(ctx, userID, r)
	if errors.Is(err, ErrUploadExpired) {
		return UploadSession{}, s.expireUpload(ctx, userID, id)
	}
	if err != nil {
		return UploadSession{}, err
	}
	state, err := stored.ResumableStatus(ctx, address, r.Size)
	if errors.Is(err, provider.ErrSessionExpired) {
		return UploadSession{}, s.expireUpload(ctx, userID, id)
	}
	if err != nil {
		return UploadSession{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return s.settle(ctx, userID, r, stored, state)
}

func checkChunk(r uploadRecord, offset, length int64) error {
	if offset < 0 || length <= 0 || length > maxChunkBytes || offset+length > r.Size {
		return ErrInvalidChunk
	}
	if offset+length < r.Size && length%provider.ChunkAlignment != 0 {
		return ErrInvalidChunk
	}
	if offset != r.Received {
		return ErrOffsetMismatch
	}
	return nil
}

// PutUploadChunk streams one piece to the account. The result says how much the
// account kept, which is where the next piece has to start.
func (s *Service) PutUploadChunk(ctx context.Context, userID, id uuid.UUID, offset, length int64, body io.Reader) (UploadSession, error) {
	r, err := s.store.UploadSessionForOwner(ctx, userID, id)
	if err != nil {
		return UploadSession{}, err
	}
	if r.File != nil {
		return r.UploadSession, nil
	}
	if err := checkChunk(r, offset, length); err != nil {
		return UploadSession{}, err
	}
	stored, address, err := s.openUpload(ctx, userID, r)
	if errors.Is(err, ErrUploadExpired) {
		return UploadSession{}, s.expireUpload(ctx, userID, id)
	}
	if err != nil {
		return UploadSession{}, err
	}
	state, err := stored.PutChunk(ctx, address, provider.Chunk{
		Offset: offset,
		Length: length,
		Total:  r.Size,
		Body:   io.LimitReader(body, length),
	})
	if errors.Is(err, provider.ErrSessionExpired) {
		return UploadSession{}, s.expireUpload(ctx, userID, id)
	}
	if err != nil {
		return UploadSession{}, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return s.settle(ctx, userID, r, stored, state)
}

// finishUpload records the file. A browser can ask about an upload while its last
// chunk is finishing, so the row is locked and the first one in wins.
func (s *Service) finishUpload(ctx context.Context, userID uuid.UUID, r uploadRecord, stored provider.Provider, object provider.Object) (UploadSession, error) {
	var finished UploadSession
	err := s.store.InTx(ctx, func(tx *Store) error {
		locked, err := tx.LockUploadSessionForOwner(ctx, userID, r.ID)
		if err != nil {
			return err
		}
		if locked.File != nil {
			finished = locked.UploadSession
			return nil
		}
		file, err := tx.CreateFile(ctx, userID, fileRecord{
			File: File{
				ConnectionID: r.ConnectionID,
				FolderID:     r.FolderID,
				Name:         r.Name,
				Size:         object.Size,
				ContentType:  r.contentType,
			},
			objectID: object.ID,
		})
		if err != nil {
			return err
		}
		finished = locked.UploadSession
		finished.Received = finished.Size
		finished.File = &file
		return tx.FinishUploadSessionForOwner(ctx, userID, r.ID, file.ID)
	})
	if err != nil {
		// Without a record nothing could ever find this object again.
		cleanup, cancel := cleanupContext(ctx)
		defer cancel()
		return UploadSession{}, errors.Join(err, stored.Delete(cleanup, object.ID))
	}
	return finished, nil
}

// CancelUpload abandons an upload and gives its space back. The account forgets
// the bytes in a week regardless, so a failure to tell it doesn't stop the cancel.
func (s *Service) CancelUpload(ctx context.Context, userID, id uuid.UUID) error {
	r, err := s.store.UploadSessionForOwner(ctx, userID, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteUnfinishedUploadForOwner(ctx, userID, id); err != nil {
		return err
	}
	s.tellAccountToForget(ctx, userID, r)
	return nil
}

func (s *Service) tellAccountToForget(ctx context.Context, userID uuid.UUID, r uploadRecord) {
	stored, address, err := s.openUpload(ctx, userID, r)
	if err != nil {
		return
	}
	cleanup, cancel := cleanupContext(ctx)
	defer cancel()
	_ = stored.CancelResumable(cleanup, address)
}

// CleanupUploads clears uploads nobody has touched for a while, which frees the
// space they held, along with the records of finished ones. The record has to go
// first: it only goes if the upload is still idle, so one that just came back to
// life is left alone.
func (s *Service) CleanupUploads(ctx context.Context) (int, error) {
	cutoff := time.Now().Add(-uploadAbandonedAfter)
	stale, err := s.store.ListStaleUploadSessions(ctx, cutoff)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, r := range stale {
		gone, err := s.store.DeleteStaleUploadSession(ctx, r.ID, cutoff)
		if err != nil {
			return removed, err
		}
		if !gone {
			continue
		}
		removed++
		if r.File == nil {
			s.tellAccountToForget(ctx, r.userID, r)
		}
	}
	return removed, nil
}
