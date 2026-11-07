package files

import (
	"context"
	"crypto/md5" //nolint:gosec // Matches Drive's checksums, so a copy matches a plain file.
	"encoding"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/audit"
	"github.com/swayam5342/tributary/internal/provider"
	"github.com/swayam5342/tributary/internal/secrets"
)

// ErrFileEncrypted means something was asked of a file that an encrypted one can't do.
var ErrFileEncrypted = errors.New("file is encrypted")

// errNotSealed means an object marked as encrypted doesn't start with a header.
var errNotSealed = errors.New("object has no sealed header")

// encryptsNewFiles says whether new files here are encrypted. Path is a folder and
// everything above it, so any of them can decide.
func encryptsNewFiles(path []Folder) bool {
	return slices.ContainsFunc(path, func(f Folder) bool { return f.Encrypted })
}

// storedType is what an account was told about a file.
func storedType(record fileRecord) string {
	if record.Encrypted {
		return sealedContentType
	}
	return record.ContentType
}

// SetFolderEncryption turns encryption on or off for new files in a folder and the folders
// below it. Files already stored are left as they are, because changing them would mean
// reading each one back and sending it again.
func (s *Service) SetFolderEncryption(ctx context.Context, userID, id uuid.UUID, encrypted bool, ip string) (Folder, error) {
	if _, err := requireLiveFolder(ctx, s.store, userID, &id); err != nil {
		return Folder{}, err
	}
	var folder Folder
	err := s.store.InTx(ctx, func(tx *Store) error {
		var err error
		if folder, err = tx.SetFolderEncryptedForOwner(ctx, userID, id, encrypted); err != nil {
			return err
		}
		return tx.Audit().Record(ctx, audit.Event{
			ActorID:  userID,
			Action:   audit.ActionFolderEncryption,
			IP:       ip,
			Metadata: map[string]any{"folder_id": id.String(), "encrypted": encrypted},
		})
	})
	return folder, err
}

// cipherFor opens a file's key. Failing here means the key that sealed it isn't in the
// keyring any more, and there's no getting the file back without it.
func (s *Service) cipherFor(sealed secrets.Sealed) (*fileCipher, error) {
	key, err := s.providers.OpenFileKey(sealed, fileKeyContext)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrFileUnreadable, err)
	}
	return newFileCipher(key)
}

// pieceHash is the running hash of a file's real content, which the account can't report
// because it only sees encrypted bytes. It's what lets an encrypted file match its copies.
type pieceHash struct {
	sum hash.Hash
	// end is where the file will have got to once this piece is kept.
	end int64
}

func (h *pieceHash) contentHash() string {
	if h == nil || h.sum == nil {
		return ""
	}
	return hex.EncodeToString(h.sum.Sum(nil))
}

// stateAt is what to save after a piece. A hash can't rewind, so if the account kept
// anything other than the whole piece it's lost, and the file goes without one.
func (h *pieceHash) stateAt(received int64, r uploadRecord) []byte {
	if h == nil {
		// Only asking how far things got, so nothing was hashed.
		if received == r.Received {
			return r.hashState
		}
		return nil
	}
	if h.sum == nil || received != h.end {
		return nil
	}
	state, err := h.sum.(encoding.BinaryMarshaler).MarshalBinary()
	if err != nil {
		return nil
	}
	return state
}

// resumeHash picks up the hash where the last piece left it.
func resumeHash(r uploadRecord, offset int64) hash.Hash {
	sum := md5.New() //nolint:gosec // Matches Drive's checksums, so a copy matches a plain file.
	if offset == 0 {
		return sum
	}
	if len(r.hashState) == 0 || sum.(encoding.BinaryUnmarshaler).UnmarshalBinary(r.hashState) != nil {
		return nil
	}
	return sum
}

// sealChunk turns a piece of a file into the frames that go to the account.
func (s *Service) sealChunk(r uploadRecord, chunk provider.Chunk) (provider.Chunk, *pieceHash, error) {
	if r.fileKey == nil {
		return provider.Chunk{}, nil, ErrFileUnreadable
	}
	c, err := s.cipherFor(*r.fileKey)
	if err != nil {
		return provider.Chunk{}, nil, err
	}
	header, err := encodeSealedHeader(*r.fileKey, r.contentType)
	if err != nil {
		return provider.Chunk{}, nil, err
	}
	end := chunk.Offset + chunk.Length
	hashed := &pieceHash{sum: resumeHash(r, chunk.Offset), end: end}
	source := chunk.Body
	if hashed.sum != nil {
		source = io.TeeReader(source, hashed.sum)
	}
	storedEnd := storedSize(r.Size)
	if end < r.Size {
		storedEnd = storedOffset(end)
	}
	start := storedOffset(chunk.Offset)
	return provider.Chunk{
		Offset: start,
		Length: storedEnd - start,
		Total:  storedSize(r.Size),
		Body:   c.seal(source, header, chunk.Offset, r.Size),
	}, hashed, nil
}

// sealedUpload is a file being encrypted in one go.
type sealedUpload struct {
	key    secrets.Sealed
	reader io.Reader
	sum    hash.Hash
}

func (u *sealedUpload) contentHash() string {
	return hex.EncodeToString(u.sum.Sum(nil))
}

// sealUpload encrypts a body of any length with a new key.
func (s *Service) sealUpload(contentType string, body io.Reader) (*sealedUpload, error) {
	key := newFileKey()
	sealed := s.providers.SealFileKey(key, fileKeyContext)
	c, err := newFileCipher(key)
	if err != nil {
		return nil, err
	}
	header, err := encodeSealedHeader(sealed, contentType)
	if err != nil {
		return nil, err
	}
	sum := md5.New() //nolint:gosec // Matches Drive's checksums, so a copy matches a plain file.
	return &sealedUpload{key: sealed, sum: sum, reader: c.seal(io.TeeReader(body, sum), header, 0, -1)}, nil
}

// openSealed reads an encrypted file, or a range of it. Only the frames in the range are
// fetched. A frame that doesn't prove itself stops the read, so nothing wrong is returned.
func (s *Service) openSealed(ctx context.Context, stored provider.Provider, record fileRecord, rng *provider.ByteRange) (io.ReadCloser, error) {
	if record.fileKey == nil {
		return nil, ErrFileUnreadable
	}
	c, err := s.cipherFor(*record.fileKey)
	if err != nil {
		return nil, err
	}
	span := sealedSpan(record.Size, rng)
	reader, err := stored.Get(ctx, record.objectID, &span.stored)
	if errors.Is(err, provider.ErrObjectNotFound) {
		return nil, ErrObjectMissing
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrProviderFailed, err)
	}
	return c.open(reader, span), nil
}

// readSealedHeader fetches the start of an object.
func readSealedHeader(ctx context.Context, stored provider.Provider, objectID string) (secrets.Sealed, string, error) {
	reader, err := stored.Get(ctx, objectID, &provider.ByteRange{Start: 0, End: sealedHeaderBytes - 1})
	if err != nil {
		return secrets.Sealed{}, "", err
	}
	defer func() { _ = reader.Close() }()
	var header strings.Builder
	if _, err := io.Copy(&header, reader); err != nil {
		return secrets.Sealed{}, "", err
	}
	key, contentType, err := decodeSealedHeader([]byte(header.String()))
	if err != nil {
		return secrets.Sealed{}, "", errNotSealed
	}
	return key, contentType, nil
}
