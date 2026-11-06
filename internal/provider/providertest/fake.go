package providertest

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // Drive's own checksum, for comparing copies.
	"encoding/hex"
	"errors"
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
	// QuotaErr, if set, is what Quota returns.
	QuotaErr error
	// Limit is the reported limit, and ExtraUsed is counted on top of stored files.
	Limit         int64
	ExtraUsed     int64
	nextID        int
	objects       map[string]fakeObject
	thumbnailType string
	thumbnail     []byte
	nextSession   int
	sessions      map[string]*fakeSession
	// DropBytes is how much a chunk loses, the way Drive can keep less than it's sent.
	DropBytes int64
	// ChunkErr, if set, is what PutChunk returns once the body is read.
	ChunkErr error
	// ListFailsAfter, if set, makes List fail after this many pages.
	ListFailsAfter int
	listed         int
	hidden         map[string]bool
	unlisted       map[string]bool
	// ExistsErr, if set, is what Exists returns.
	ExistsErr error
	// FolderErr, if set, is what folder and move operations return.
	FolderErr error
	// ShareErr, if set, is what Share and Unshare return.
	ShareErr error
	shares   map[string]provider.ShareRole
}

type fakeSession struct {
	upload   provider.Resumable
	body     []byte
	finished *provider.Object
}

type fakeObject struct {
	provider.Object
	body []byte
}

var _ provider.Provider = (*Fake)(nil)

func checksum(body []byte) string {
	sum := md5.Sum(body) //nolint:gosec // Drive's own checksum, for comparing copies.
	return hex.EncodeToString(sum[:])
}

// NewFake returns an empty provider that lists pageSize objects at a time.
func NewFake(pageSize int) *Fake {
	return &Fake{pageSize: pageSize, objects: map[string]fakeObject{}, sessions: map[string]*fakeSession{}}
}

// Check implements provider.Provider.
func (f *Fake) Check(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.CheckErr
}

// Quota implements provider.Provider.
func (f *Fake) Quota(context.Context) (provider.Quota, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.QuotaErr != nil {
		return provider.Quota{}, f.QuotaErr
	}
	used := f.ExtraUsed
	for _, object := range f.objects {
		used += object.Size
	}
	return provider.Quota{Limit: f.Limit, Used: used}, nil
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
		Checksum:   checksum(body),

		ContentType: upload.ContentType,
		Parent:      f.parent(upload.Parent),
	}
	f.objects[object.ID] = fakeObject{Object: object, body: body}
	return object, nil
}

// Get implements provider.Provider.
func (f *Fake) Get(_ context.Context, id string, rng *provider.ByteRange) (io.ReadCloser, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	object, ok := f.objects[id]
	if !ok {
		return nil, provider.ErrObjectNotFound
	}
	body := object.body
	if rng != nil {
		body = body[rng.Start : rng.End+1]
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

// SetThumbnail makes every object report this preview image.
func (f *Fake) SetThumbnail(contentType string, body []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.thumbnailType, f.thumbnail = contentType, body
}

// Thumbnail implements provider.Provider.
func (f *Fake) Thumbnail(_ context.Context, id string) (provider.Thumbnail, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, ok := f.objects[id]; !ok {
		return provider.Thumbnail{}, provider.ErrObjectNotFound
	}
	if f.thumbnail == nil {
		return provider.Thumbnail{}, provider.ErrNoThumbnail
	}
	return provider.Thumbnail{ContentType: f.thumbnailType, Body: io.NopCloser(bytes.NewReader(f.thumbnail))}, nil
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
	if f.ListFailsAfter > 0 {
		if f.listed >= f.ListFailsAfter {
			return provider.ObjectPage{}, errors.New("listing failed")
		}
		f.listed++
	}
	ids := make([]int, 0, len(f.objects))
	for id := range f.objects {
		if f.isHidden(id) || f.unlisted[id] {
			continue
		}
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

// BeginResumable implements provider.Provider.
func (f *Fake) BeginResumable(_ context.Context, upload provider.Resumable) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextSession++
	session := "session-" + strconv.Itoa(f.nextSession)
	f.sessions[session] = &fakeSession{upload: upload}
	return session, nil
}

// PutChunk implements provider.Provider. It's as strict as Drive about where a
// chunk starts and how long it is.
func (f *Fake) PutChunk(_ context.Context, session string, chunk provider.Chunk) (provider.ResumeState, error) {
	body, err := io.ReadAll(chunk.Body)
	if err != nil {
		return provider.ResumeState{}, fmt.Errorf("read chunk: %w", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ChunkErr != nil {
		return provider.ResumeState{}, f.ChunkErr
	}
	s, ok := f.sessions[session]
	if !ok {
		return provider.ResumeState{}, provider.ErrSessionExpired
	}
	if s.finished != nil {
		return provider.ResumeState{Received: s.finished.Size, Object: s.finished}, nil
	}
	last := chunk.Offset+chunk.Length == chunk.Total
	if chunk.Offset != int64(len(s.body)) || int64(len(body)) != chunk.Length || chunk.Total != s.upload.Size ||
		(!last && chunk.Length%provider.ChunkAlignment != 0) {
		return provider.ResumeState{}, fmt.Errorf("bad chunk: offset %d, length %d, have %d", chunk.Offset, chunk.Length, len(s.body))
	}
	s.body = append(s.body, body...)
	if last {
		f.nextID++
		object := provider.Object{
			ID:          strconv.Itoa(f.nextID),
			Name:        s.upload.Name,
			Size:        int64(len(s.body)),
			Checksum:    checksum(s.body),
			ContentType: s.upload.ContentType,
			Parent:      f.parent(s.upload.Parent),
			ModifiedAt:  time.Now(),
		}
		f.objects[object.ID] = fakeObject{Object: object, body: s.body}
		s.finished = &object
		return provider.ResumeState{Received: object.Size, Object: &object}, nil
	}
	s.body = s.body[:max(int64(len(s.body))-f.DropBytes, 0)]
	return provider.ResumeState{Received: int64(len(s.body))}, nil
}

// ResumableStatus implements provider.Provider.
func (f *Fake) ResumableStatus(_ context.Context, session string, _ int64) (provider.ResumeState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s, ok := f.sessions[session]
	if !ok {
		return provider.ResumeState{}, provider.ErrSessionExpired
	}
	if s.finished != nil {
		return provider.ResumeState{Received: s.finished.Size, Object: s.finished}, nil
	}
	return provider.ResumeState{Received: int64(len(s.body))}, nil
}

// CancelResumable implements provider.Provider.
func (f *Fake) CancelResumable(_ context.Context, session string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.sessions, session)
	return nil
}

// OpenSessions counts uploads that were started and not cancelled.
func (f *Fake) OpenSessions() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sessions)
}

// ExpireSessions forgets every upload, as Drive does after a week.
func (f *Fake) ExpireSessions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = map[string]*fakeSession{}
}

// Age makes every object look this much older.
func (f *Fake) Age(by time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for id, object := range f.objects {
		object.ModifiedAt = object.ModifiedAt.Add(-by)
		f.objects[id] = object
	}
}

// Hide keeps an object out of listings, as trashing it in Drive does.
func (f *Fake) Hide(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.hidden == nil {
		f.hidden = map[string]bool{}
	}
	f.hidden[id] = true
}

// Unhide puts an object back in listings.
func (f *Fake) Unhide(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.hidden, id)
}

// SetPageSize changes how many objects a listing returns at a time.
func (f *Fake) SetPageSize(size int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pageSize = size
}

// Exists implements provider.Provider.
func (f *Fake) Exists(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ExistsErr != nil {
		return false, f.ExistsErr
	}
	_, ok := f.objects[id]
	return ok && !f.isHidden(id), nil
}

// Unlist keeps an object out of listings while it's still there, the way a listing
// can lag behind a new file.
func (f *Fake) Unlist(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.unlisted == nil {
		f.unlisted = map[string]bool{}
	}
	f.unlisted[id] = true
}

// Describe implements provider.Provider.
func (f *Fake) Describe(_ context.Context, id string) (provider.Object, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ExistsErr != nil {
		return provider.Object{}, f.ExistsErr
	}
	object, ok := f.objects[id]
	if !ok || f.isHidden(id) {
		return provider.Object{}, provider.ErrObjectNotFound
	}
	return object.Object, nil
}

// Share implements provider.Provider.
func (f *Fake) Share(_ context.Context, id string, role provider.ShareRole) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ShareErr != nil {
		return "", f.ShareErr
	}
	if _, ok := f.objects[id]; !ok {
		return "", provider.ErrObjectNotFound
	}
	if f.shares == nil {
		f.shares = map[string]provider.ShareRole{}
	}
	f.shares[id] = role
	return "https://drive.example/" + id, nil
}

// Unshare implements provider.Provider.
func (f *Fake) Unshare(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ShareErr != nil {
		return f.ShareErr
	}
	delete(f.shares, id)
	return nil
}

// SharedAs says what role an object's public link has, if it has one.
func (f *Fake) SharedAs(id string) (provider.ShareRole, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	role, ok := f.shares[id]
	return role, ok
}

// AccessToken implements provider.Provider.
func (f *Fake) AccessToken(context.Context) (string, error) {
	return "fake-access-token", f.CheckErr
}

// rootID stands in for Tributary's own folder.
const rootID = "root"

func (f *Fake) parent(id string) string {
	if id == "" {
		return rootID
	}
	return id
}

// isHidden reports whether an object or any folder above it is in the trash, because
// trashing a folder takes its contents with it.
func (f *Fake) isHidden(id string) bool {
	for id != "" && id != rootID {
		if f.hidden[id] {
			return true
		}
		id = f.objects[id].Parent
	}
	return false
}

// RootID implements provider.Provider.
func (f *Fake) RootID() string {
	return rootID
}

// CreateFolder implements provider.Provider.
func (f *Fake) CreateFolder(_ context.Context, parent, name string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FolderErr != nil {
		return "", f.FolderErr
	}
	f.nextID++
	object := provider.Object{ID: strconv.Itoa(f.nextID), Name: name, Folder: true, Parent: f.parent(parent), ModifiedAt: time.Now()}
	f.objects[object.ID] = fakeObject{Object: object}
	return object.ID, nil
}

// Rename implements provider.Provider.
func (f *Fake) Rename(_ context.Context, id, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FolderErr != nil {
		return f.FolderErr
	}
	object, ok := f.objects[id]
	if !ok {
		return provider.ErrObjectNotFound
	}
	object.Name = name
	f.objects[id] = object
	return nil
}

// Move implements provider.Provider.
func (f *Fake) Move(_ context.Context, id, parent string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FolderErr != nil {
		return f.FolderErr
	}
	object, ok := f.objects[id]
	if !ok {
		return provider.ErrObjectNotFound
	}
	object.Parent = f.parent(parent)
	f.objects[id] = object
	return nil
}

// Trash implements provider.Provider.
func (f *Fake) Trash(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.FolderErr != nil {
		return f.FolderErr
	}
	if _, ok := f.objects[id]; !ok {
		return provider.ErrObjectNotFound
	}
	if f.hidden == nil {
		f.hidden = map[string]bool{}
	}
	f.hidden[id] = true
	return nil
}

// Snapshot returns what's stored, by ID, for tests to look at.
func (f *Fake) Snapshot() map[string]provider.Object {
	f.mu.Lock()
	defer f.mu.Unlock()
	objects := make(map[string]provider.Object, len(f.objects))
	for id, object := range f.objects {
		objects[id] = object.Object
	}
	return objects
}
