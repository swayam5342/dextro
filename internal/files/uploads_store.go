package files

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/swayam5342/tributary/internal/db/dbgen"
	"github.com/swayam5342/tributary/internal/secrets"
)

// uploadRecord is a session plus what must not leave this package.
type uploadRecord struct {
	UploadSession
	userID      uuid.UUID
	contentType string
	sealed      *secrets.Sealed
	fileID      *uuid.UUID
	// fileKey and hashState are only for encrypted files. See sealed.go.
	fileKey   *secrets.Sealed
	hashState []byte
}

func toUploadRecord(row dbgen.UploadSession) uploadRecord {
	r := uploadRecord{
		UploadSession: UploadSession{
			ID:           row.ID,
			ConnectionID: row.ConnectionID,
			Name:         row.Name,
			Size:         row.Size,
			Received:     row.Received,
			LastModified: row.LastModified,
			CreatedAt:    row.CreatedAt,
			Encrypted:    row.Encrypted,
		},
		userID:      row.UserID,
		contentType: row.ContentType,
		fileKey:     sealedKey(row.KeyID, row.KeyCiphertext),
		hashState:   row.HashState,
	}
	if row.FolderID.Valid {
		r.FolderID = &row.FolderID.UUID
	}
	if row.FileID.Valid {
		r.fileID = &row.FileID.UUID
	}
	if row.SecretKeyID.Valid {
		r.sealed = &secrets.Sealed{KeyID: row.SecretKeyID.String, Ciphertext: row.SecretCiphertext}
	}
	return r
}

// withFile fills in the file of a finished upload.
func (s *Store) withFile(ctx context.Context, userID uuid.UUID, r uploadRecord) (uploadRecord, error) {
	if r.fileID == nil {
		return r, nil
	}
	file, err := s.FileForOwner(ctx, userID, *r.fileID)
	if err != nil {
		return uploadRecord{}, err
	}
	r.File = &file.File
	return r, nil
}

// UploadSessionForOwner returns one of a user's uploads.
func (s *Store) UploadSessionForOwner(ctx context.Context, userID, id uuid.UUID) (uploadRecord, error) {
	row, err := s.queries.GetUploadSessionForOwner(ctx, dbgen.GetUploadSessionForOwnerParams{UserID: userID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return uploadRecord{}, ErrUploadNotFound
	}
	if err != nil {
		return uploadRecord{}, fmt.Errorf("get upload: %w", err)
	}
	return s.withFile(ctx, userID, toUploadRecord(row))
}

// LockUploadSessionForOwner returns one of a user's uploads and holds it until the
// transaction ends.
func (s *Store) LockUploadSessionForOwner(ctx context.Context, userID, id uuid.UUID) (uploadRecord, error) {
	row, err := s.queries.LockUploadSessionForOwner(ctx, dbgen.LockUploadSessionForOwnerParams{UserID: userID, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return uploadRecord{}, ErrUploadNotFound
	}
	if err != nil {
		return uploadRecord{}, fmt.Errorf("lock upload: %w", err)
	}
	return s.withFile(ctx, userID, toUploadRecord(row))
}

// ListUploadSessionsForOwner returns a user's unfinished uploads, oldest first.
func (s *Store) ListUploadSessionsForOwner(ctx context.Context, userID uuid.UUID) ([]uploadRecord, error) {
	rows, err := s.queries.ListUploadSessionsForOwner(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("list uploads: %w", err)
	}
	records := make([]uploadRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, toUploadRecord(row))
	}
	return records, nil
}

// SetUploadSecretForOwner saves a session's sealed address.
func (s *Store) SetUploadSecretForOwner(ctx context.Context, userID, id uuid.UUID, sealed secrets.Sealed) error {
	rows, err := s.queries.SetUploadSecretForOwner(ctx, dbgen.SetUploadSecretForOwnerParams{
		UserID:           userID,
		ID:               id,
		SecretKeyID:      pgtype.Text{String: sealed.KeyID, Valid: true},
		SecretCiphertext: sealed.Ciphertext,
	})
	if err != nil {
		return fmt.Errorf("save upload address: %w", err)
	}
	if rows == 0 {
		return ErrUploadNotFound
	}
	return nil
}

// SetUploadReceivedForOwner saves how much the account has kept.
func (s *Store) SetUploadReceivedForOwner(ctx context.Context, userID, id uuid.UUID, received int64, hashState []byte) error {
	err := s.queries.SetUploadReceivedForOwner(ctx, dbgen.SetUploadReceivedForOwnerParams{UserID: userID, ID: id, Received: received, HashState: hashState})
	if err != nil {
		return fmt.Errorf("save upload progress: %w", err)
	}
	return nil
}

// FinishUploadSessionForOwner ties an upload to its file and forgets its address.
func (s *Store) FinishUploadSessionForOwner(ctx context.Context, userID, id, fileID uuid.UUID) error {
	err := s.queries.FinishUploadSessionForOwner(ctx, dbgen.FinishUploadSessionForOwnerParams{
		UserID: userID,
		ID:     id,
		FileID: uuid.NullUUID{UUID: fileID, Valid: true},
	})
	if err != nil {
		return fmt.Errorf("finish upload: %w", err)
	}
	return nil
}

// DeleteUnfinishedUploadForOwner removes an upload that hasn't finished.
func (s *Store) DeleteUnfinishedUploadForOwner(ctx context.Context, userID, id uuid.UUID) error {
	rows, err := s.queries.DeleteUnfinishedUploadForOwner(ctx, dbgen.DeleteUnfinishedUploadForOwnerParams{UserID: userID, ID: id})
	if err != nil {
		return fmt.Errorf("delete upload: %w", err)
	}
	if rows == 0 {
		return ErrUploadNotFound
	}
	return nil
}

// ListStaleUploadSessions returns every user's uploads idle since cutoff. It is
// unscoped on purpose and is for the background cleanup only.
func (s *Store) ListStaleUploadSessions(ctx context.Context, cutoff time.Time) ([]uploadRecord, error) {
	rows, err := s.queries.ListStaleUploadSessions(ctx, cutoff)
	if err != nil {
		return nil, fmt.Errorf("list stale uploads: %w", err)
	}
	records := make([]uploadRecord, 0, len(rows))
	for _, row := range rows {
		records = append(records, toUploadRecord(row))
	}
	return records, nil
}

// DeleteStaleUploadSession removes an upload if it's still idle, and says whether
// it did. It is for the background cleanup only.
func (s *Store) DeleteStaleUploadSession(ctx context.Context, id uuid.UUID, cutoff time.Time) (bool, error) {
	rows, err := s.queries.DeleteStaleUploadSession(ctx, dbgen.DeleteStaleUploadSessionParams{ID: id, UpdatedAt: cutoff})
	if err != nil {
		return false, fmt.Errorf("delete stale upload: %w", err)
	}
	return rows > 0, nil
}
