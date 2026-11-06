// Package provider defines what dextro needs from a storage service.
package provider

import (
	"context"
	"errors"
	"io"
	"time"
)

// Errors a provider reports.
var (
	ErrObjectNotFound = errors.New("object not found")
	// ErrNoThumbnail means the object exists but has no preview image, at least yet.
	ErrNoThumbnail = errors.New("no thumbnail")
	// ErrShareRefused means the account won't allow a public link, which a work or
	// school account's admin can forbid.
	ErrShareRefused = errors.New("public link refused")
)

// Object describes one stored file.
type Object struct {
	// ID is what the provider knows the object by. It is opaque to callers.
	ID   string
	Name string
	Size int64
	// ContentType is what the object was stored as, which a listing needs to bring a
	// file back. Empty if the account doesn't say.
	ContentType string
	// Parent is the folder it's in, as the provider knows it.
	Parent string
	// Checksum identifies the content, the same way in every account. Empty if the
	// account doesn't say.
	Checksum string
	// Folder marks a folder rather than a file.
	Folder     bool
	ModifiedAt time.Time
}

// ObjectPage is one page of a listing. NextPageToken is empty on the last page.
type ObjectPage struct {
	Objects       []Object
	NextPageToken string
}

// ByteRange is a span of an object. Both ends are inclusive and must lie inside it.
type ByteRange struct {
	Start int64
	End   int64
}

// Quota is how much of an account is in use. A Limit of 0 means no limit.
type Quota struct {
	Limit int64
	Used  int64
}

// Free returns the bytes left, and false if the account has no limit. An account
// can be over its limit, which counts as no room rather than negative room.
func (q Quota) Free() (int64, bool) {
	if q.Limit == 0 {
		return 0, false
	}
	return max(q.Limit-q.Used, 0), true
}

// Thumbnail is a small preview image. The caller closes Body.
type Thumbnail struct {
	ContentType string
	Body        io.ReadCloser
}

// Upload is a file to store. Body is read as a stream and its length need not
// be known, so memory use must not depend on the file's size.
type Upload struct {
	Name        string
	ContentType string
	// Parent is the folder to store it in. Empty means dextro's own folder.
	Parent string
	Body   io.Reader
}

// ChunkAlignment is what every chunk but the last must be a multiple of.
const ChunkAlignment = 256 << 10

// ErrSessionExpired means a resumable upload is gone, so what it held is lost.
var ErrSessionExpired = errors.New("upload session expired")

// Resumable describes a file about to be sent in chunks.
type Resumable struct {
	Name        string
	ContentType string
	Size        int64
	// Parent is the folder to store it in. Empty means dextro's own folder.
	Parent string
}

// Chunk is one piece of a resumable upload.
type Chunk struct {
	Offset int64
	Length int64
	Total  int64
	Body   io.Reader
}

// ResumeState is how far a resumable upload has got. Object is set once it's
// finished. Received can be less than what was sent: the account decides what it
// keeps, and the next chunk has to start from there.
type ResumeState struct {
	Received int64
	Object   *Object
}

// ShareRole is what anyone with a public link can do with a file.
type ShareRole string

// The roles a link can have.
const (
	ShareViewer    ShareRole = "reader"
	ShareCommenter ShareRole = "commenter"
	ShareEditor    ShareRole = "writer"
)

// Provider is a storage account. Implementations see only what the account lets dextro
// see, keep new objects in dextro's folder, touch permissions only in Share and Unshare, and keep no state
// between calls that would tie them to one request.
type Provider interface {
	// Check confirms the account is reachable with its credentials.
	Check(ctx context.Context) error
	// Quota reports how much of the account is in use.
	Quota(ctx context.Context) (Quota, error)
	// Put stores a new object. It never replaces an existing one, even with the
	// same name.
	Put(ctx context.Context, upload Upload) (Object, error)
	// Get opens an object, or the part of it in rng if that isn't nil. The caller
	// closes the result.
	Get(ctx context.Context, id string, rng *ByteRange) (io.ReadCloser, error)
	// Thumbnail opens a small preview image of an object.
	Thumbnail(ctx context.Context, id string) (Thumbnail, error)
	// Delete removes an object permanently. Moving files to the trash is dextro's
	// job, above this interface.
	Delete(ctx context.Context, id string) error
	// Exists asks about one object directly. Something in the account's trash counts
	// as gone. Unlike a listing, this doesn't lag behind a change.
	Exists(ctx context.Context, id string) (bool, error)
	// Describe returns one object's details, or ErrObjectNotFound if it's gone or in
	// the account's trash.
	Describe(ctx context.Context, id string) (Object, error)
	// AccessToken returns a short-lived token for the account, for Google's picker to
	// run with in the browser. It's a secret: it must never be logged or stored.
	AccessToken(ctx context.Context) (string, error)
	// RootID names dextro's own folder, the top of everything it stores.
	RootID() string
	// CreateFolder makes a folder inside parent, or in dextro's own folder if parent is
	// empty, and returns its ID.
	CreateFolder(ctx context.Context, parent, name string) (string, error)
	// Rename changes an object's name.
	Rename(ctx context.Context, id, name string) error
	// Move puts an object inside parent, or in dextro's own folder if parent is empty.
	Move(ctx context.Context, id, parent string) error
	// Trash moves an object to the account's trash, where its owner can still get it
	// back. Folders go this way because deleting one takes its contents with it, and
	// dextro can't see anything added to it by hand.
	Trash(ctx context.Context, id string) error
	// List returns one page of everything the account lets dextro see, folders
	// included, but not dextro's own folder. An empty token starts from the beginning.
	List(ctx context.Context, pageToken string) (ObjectPage, error)
	// BeginResumable starts an upload sent in chunks. The result is the session's
	// address, which grants access to the upload and must be kept secret.
	BeginResumable(ctx context.Context, upload Resumable) (string, error)
	// PutChunk sends the next piece, which must start at what was last received.
	PutChunk(ctx context.Context, session string, chunk Chunk) (ResumeState, error)
	// ResumableStatus asks how far an upload has really got.
	ResumableStatus(ctx context.Context, session string, total int64) (ResumeState, error)
	// CancelResumable abandons an upload. One that's already gone is no error.
	CancelResumable(ctx context.Context, session string) error
	// Share lets anyone with the link reach one object, and returns the link. Calling it
	// again changes the role and keeps the link. It's the only way anything here becomes
	// public, and only ever for a person's explicit choice.
	Share(ctx context.Context, id string, role ShareRole) (string, error)
	// Unshare ends public access to an object. An object that has none, or is gone, is
	// no error, so it's safe to repeat.
	Unshare(ctx context.Context, id string) error
}
