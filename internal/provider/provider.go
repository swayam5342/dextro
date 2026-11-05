// Package provider defines what dextro needs from a storage service.
package provider

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrObjectNotFound means an object doesn't exist in the provider.
var ErrObjectNotFound = errors.New("object not found")

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
	// Put stores a new object. It never replaces an existing one, even with the
	// same name.
	Put(ctx context.Context, upload Upload) (Object, error)
	// Get opens an object for reading. The caller closes the result.
	Get(ctx context.Context, id string) (io.ReadCloser, error)
	// Delete removes an object permanently. Moving files to the trash is dextro's
	// job, above this interface.
	Delete(ctx context.Context, id string) error
	// List returns one page of objects. An empty token starts from the beginning.
	List(ctx context.Context, pageToken string) (ObjectPage, error)
}
