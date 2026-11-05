package providertest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/swayam5342/dextro/internal/provider"
)

// Fake is an in-memory provider. Unlike real ones it holds whole files in
// memory, so it is for tests only.
type Fake struct {
	pageSize int

	mu sync.Mutex
	// CheckErr, if set, is what Check returns.
	CheckErr error
	nextID   int
	objects  map[string]fakeObject
}

type fakeObject struct {
	provider.Object
	body []byte
}

var _ provider.Provider = (*Fake)(nil)

// NewFake returns an empty provider that lists pageSize objects at a time.
func NewFake(pageSize int) *Fake {
	return &Fake{pageSize: pageSize, objects: map[string]fakeObject{}}
}

// Check implements provider.Provider.
func (f *Fake) Check(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.CheckErr
}

// Put implements provider.Provider.
func (f *Fake) Put(_ context.Context, upload provider.Upload) (provider.Object, error) {
	body, err := io.ReadAll(upload.Body)
	if err != nil {
		return provider.Object{}, fmt.Errorf("read upload: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextID++
	object := provider.Object{
		ID:         strconv.Itoa(f.nextID),
		Name:       upload.Name,
		Size:       int64(len(body)),
		ModifiedAt: time.Now(),
	}
	f.objects[object.ID] = fakeObject{Object: object, body: body}
	return object, nil
}

// Get implements provider.Provider.
func (f *Fake) Get(_ context.Context, id string) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	object, ok := f.objects[id]
	if !ok {
		return nil, provider.ErrObjectNotFound
	}
	return io.NopCloser(bytes.NewReader(object.body)), nil
}

// Delete implements provider.Provider.
func (f *Fake) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[id]; !ok {
		return provider.ErrObjectNotFound
	}
	delete(f.objects, id)
	return nil
}

// List implements provider.Provider.
func (f *Fake) List(_ context.Context, pageToken string) (provider.ObjectPage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ids := make([]int, 0, len(f.objects))
	for id := range f.objects {
		n, err := strconv.Atoi(id)
		if err != nil {
			return provider.ObjectPage{}, fmt.Errorf("bad id %q: %w", id, err)
		}
		ids = append(ids, n)
	}
	slices.Sort(ids)

	start := 0
	if pageToken != "" {
		var err error
		if start, err = strconv.Atoi(pageToken); err != nil {
			return provider.ObjectPage{}, fmt.Errorf("bad page token: %w", err)
		}
	}
	var page provider.ObjectPage
	for i := start; i < len(ids) && i < start+f.pageSize; i++ {
		page.Objects = append(page.Objects, f.objects[strconv.Itoa(ids[i])].Object)
	}
	if next := start + f.pageSize; next < len(ids) {
		page.NextPageToken = strconv.Itoa(next)
	}
	return page, nil
}
