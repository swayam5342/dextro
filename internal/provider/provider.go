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
)

// Object describes one stored file.
type Object struct {
	// ID is what the provider knows the object by. It is opaque to callers.
	ID         string
	Name       string
	Size       int64
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
	Body        io.Reader
}

// Provider is a storage account. Implementations see only the area dextro owns
// in that account, never set public permissions on anything, and keep no state
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
	// List returns one page of objects. An empty token starts from the beginning.
	List(ctx context.Context, pageToken string) (ObjectPage, error)
}
