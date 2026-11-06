package files

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/storage"
)

func (f *uploadFixture) allowLinks(t *testing.T) {
	t.Helper()
	if err := f.service.store.Settings().SetSetting(f.ctx, "google_links_enabled", []byte("true")); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
}

func (f *uploadFixture) share(t *testing.T, userID, id uuid.UUID, role string) Share {
	t.Helper()
	share, err := f.service.ShareFile(f.ctx, userID, id, ShareInput{Role: role}, "203.0.113.7")
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	return share
}

// public says what an object's public link allows, if it has one.
func (f *uploadFixture) public(email, object string) (provider.ShareRole, bool) {
	return f.account(email).SharedAs(object)
}

// sharedFile is the usual starting point: one account, links on, one file with a link.
func (f *uploadFixture) sharedFile(t *testing.T) (user, connection uuid.UUID, file File, object string) {
	t.Helper()
	f.allowLinks(t)
	user = f.user(t)
	connection = f.link(t, user, "a@gmail.com", 1<<30)
	file = f.putIn(t, user, connection, nil, "a.txt", "hello")
	f.share(t, user, file.ID, "viewer")
	return user, connection, file, f.objectID(t, user, file.ID)
}

func TestShares(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"a link is made with the chosen role", testShareRoles},
		{"changing a link keeps its address", testShareChange},
		{"links are off until the admin says", testShareOffByDefault},
		{"turning links off leaves a way to remove them", testShareTurnedOff},
		{"a bad request is refused", testShareBadRequest},
		{"an added file can't have a link", testShareExternal},
		{"a trashed file can't have a link", testShareTrashed},
		{"a file in a trashed folder can't have a link", testShareInTrashedFolder},
		{"a file on its way to another account can't have a link", testShareWhileMoving},
		{"google refusing leaves nothing behind", testShareRefused},
		{"google failing leaves a row to clear up", testShareFailure},
		{"a link made in a crash is found and removed", testShareAbandoned},
		{"a recent unfinished link is left alone", testShareInProgress},
		{"removing a link tells google first", testUnshare},
		{"a link google won't remove stays listed", testUnshareFails},
		{"trashing a file ends its link", testTrashEndsLink},
		{"a link that can't be ended stops the trash", testTrashNeedsLinkGone},
		{"restoring doesn't bring a link back", testRestoreLeavesLinkGone},
		{"trashing a folder ends the links inside it", testTrashFolderEndsLinks},
		{"a file trashed while google answers isn't left public", testShareRacesTrash},
		{"a link ends at its expiry", testShareExpires},
		{"a link not due yet stays", testShareNotDue},
		{"a link that won't expire is tried again", testShareExpiryRetried},
		{"a shared file can't move", testSharedFileCantMove},
		{"an account with links can't be emptied", testSharedAccountCantEmpty},
		{"a file shared after queueing doesn't move", testSharedAfterQueueing},
		{"an account with links can't be removed", testSharedAccountCantUnlink},
		{"a dead account with links can be removed", testDeadAccountUnlinks},
		{"users can't reach each other's links", testShareIsolation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			f := newUploadFixture(t, pool)
			f.service.transferWait = time.Millisecond
			tt.run(t, f)
		})
	}
}

func testShareRoles(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	for role, want := range map[string]provider.ShareRole{
		"viewer":    provider.ShareViewer,
		"commenter": provider.ShareCommenter,
		"editor":    provider.ShareEditor,
	} {
		file := f.putIn(t, user, a, nil, role+".txt", "x")
		share := f.share(t, user, file.ID, role)
		if share.URL == "" || share.Name != file.Name || share.Role != role {
			t.Errorf("share = %+v", share)
		}
		if got, ok := f.public("a@gmail.com", f.objectID(t, user, file.ID)); !ok || got != want {
			t.Errorf("%s: drive has %q, %v", role, got, ok)
		}
	}
	list, err := f.service.ListShares(f.ctx, user)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if !list.Enabled || len(list.Shares) != 3 {
		t.Errorf("list = %+v", list)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'file.shared'`); got != 3 {
		t.Errorf("%d audit events", got)
	}
}

func testShareChange(t *testing.T, f *uploadFixture) {
	user, _, file, object := f.sharedFile(t)
	before, _, err := f.service.store.ShareForOwner(f.ctx, user, file.ID)
	if err != nil {
		t.Fatalf("ShareForOwner: %v", err)
	}
	expires := time.Now().Add(time.Hour)
	after, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "editor", ExpiresAt: &expires}, "")
	if err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if after.URL != before.URL || after.Role != "editor" || after.ExpiresAt == nil {
		t.Errorf("share = %+v", after)
	}
	if got, _ := f.public("a@gmail.com", object); got != provider.ShareEditor {
		t.Errorf("drive has %q", got)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'file.share_changed'`); got != 1 {
		t.Errorf("%d audit events", got)
	}
}

func testShareOffByDefault(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer"}, ""); !errors.Is(err, ErrLinksDisabled) {
		t.Errorf("error = %v, want ErrLinksDisabled", err)
	}
	if _, ok := f.public("a@gmail.com", f.objectID(t, user, file.ID)); ok {
		t.Error("the file went public")
	}
	list, err := f.service.ListShares(f.ctx, user)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if list.Enabled {
		t.Error("links say they're on")
	}
}

func testShareTurnedOff(t *testing.T, f *uploadFixture) {
	user, _, file, object := f.sharedFile(t)
	if err := f.service.store.Settings().SetSetting(f.ctx, "google_links_enabled", []byte("false")); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "editor"}, ""); !errors.Is(err, ErrLinksDisabled) {
		t.Errorf("widening: error = %v", err)
	}
	if err := f.service.UnshareFile(f.ctx, user, file.ID, ""); err != nil {
		t.Errorf("UnshareFile: %v", err)
	}
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("still public")
	}
}

func testShareBadRequest(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	past := time.Now().Add(-time.Minute)
	for name, in := range map[string]ShareInput{
		"no role":  {},
		"bad role": {Role: "owner"},
		"past":     {Role: "viewer", ExpiresAt: &past},
	} {
		if _, err := f.service.ShareFile(f.ctx, user, file.ID, in, ""); !errors.Is(err, ErrInvalidShare) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

func testShareExternal(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	id, object := f.addedFile(t, user, a)
	if _, err := f.service.ShareFile(f.ctx, user, id, ShareInput{Role: "viewer"}, ""); !errors.Is(err, ErrExternalFile) {
		t.Errorf("error = %v, want ErrExternalFile", err)
	}
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("the file went public")
	}
}

func testShareTrashed(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	if err := f.service.TrashFile(f.ctx, user, file.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer"}, ""); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("error = %v, want ErrFileNotFound", err)
	}
}

func testShareInTrashedFolder(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Trip")
	file := f.putIn(t, user, a, &folder.ID, "a.txt", "x")
	if err := f.service.TrashFolder(f.ctx, user, folder.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer"}, ""); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("error = %v, want ErrFolderNotFound", err)
	}
	if _, ok := f.public("a@gmail.com", f.objectID(t, user, file.ID)); ok {
		t.Error("the file went public")
	}
}

func testShareWhileMoving(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	f.queue(t, user, b, file.ID)
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer"}, ""); !errors.Is(err, ErrFileMoving) {
		t.Errorf("error = %v, want ErrFileMoving", err)
	}
}

func testShareRefused(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	f.account("a@gmail.com").ShareErr = provider.ErrShareRefused
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer"}, ""); !errors.Is(err, provider.ErrShareRefused) {
		t.Errorf("error = %v, want ErrShareRefused", err)
	}
	if got := f.count(t, `SELECT count(*) FROM file_shares`); got != 0 {
		t.Errorf("%d rows", got)
	}
}

func testShareFailure(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	f.account("a@gmail.com").ShareErr = errors.New("backend error")
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer"}, ""); !errors.Is(err, ErrProviderFailed) {
		t.Errorf("error = %v, want ErrProviderFailed", err)
	}
	f.account("a@gmail.com").ShareErr = nil
	// It may have worked, so something has to remember to check.
	if got := f.count(t, `SELECT count(*) FROM file_shares WHERE url = ''`); got != 1 {
		t.Errorf("%d rows waiting", got)
	}
	list, err := f.service.ListShares(f.ctx, user)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(list.Shares) != 0 {
		t.Errorf("an unconfirmed link is listed: %+v", list.Shares)
	}
	// Trying again finishes the same row.
	if got := f.share(t, user, file.ID, "commenter"); got.URL == "" || got.Role != "commenter" {
		t.Errorf("share = %+v", got)
	}
}

// makeAbandoned leaves what a crash just after Google answered would: a public file, and
// a row that never heard.
func (f *uploadFixture) makeAbandoned(t *testing.T, user, file uuid.UUID, age time.Duration) string {
	t.Helper()
	object := f.objectID(t, user, file)
	if _, err := f.account("a@gmail.com").Share(f.ctx, object, provider.ShareEditor); err != nil {
		t.Fatalf("Share: %v", err)
	}
	_, err := f.pool.Exec(f.ctx, `INSERT INTO file_shares (file_id, user_id, role, updated_at) VALUES ($1, $2, 'editor', now() - $3::interval)`,
		file, user, age.String())
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	return object
}

func testShareAbandoned(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	object := f.makeAbandoned(t, user, file.ID, time.Hour)
	if _, err := f.service.ExpireShares(f.ctx); err != nil {
		t.Fatalf("ExpireShares: %v", err)
	}
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("the crash left a public file nothing knows about")
	}
	if got := f.count(t, `SELECT count(*) FROM file_shares`); got != 0 {
		t.Errorf("%d rows", got)
	}
}

func testShareInProgress(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	object := f.makeAbandoned(t, user, file.ID, time.Second)
	if _, err := f.service.ExpireShares(f.ctx); err != nil {
		t.Fatalf("ExpireShares: %v", err)
	}
	if _, ok := f.public("a@gmail.com", object); !ok {
		t.Error("the job undid a link being made right now")
	}
}

func testUnshare(t *testing.T, f *uploadFixture) {
	user, _, file, object := f.sharedFile(t)
	if err := f.service.UnshareFile(f.ctx, user, file.ID, "203.0.113.7"); err != nil {
		t.Fatalf("UnshareFile: %v", err)
	}
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("still public")
	}
	if err := f.service.UnshareFile(f.ctx, user, file.ID, ""); !errors.Is(err, ErrShareNotFound) {
		t.Errorf("twice: error = %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'file.unshared'`); got != 1 {
		t.Errorf("%d audit events", got)
	}
}

func testUnshareFails(t *testing.T, f *uploadFixture) {
	user, _, file, object := f.sharedFile(t)
	f.account("a@gmail.com").ShareErr = errors.New("backend error")
	if err := f.service.UnshareFile(f.ctx, user, file.ID, ""); !errors.Is(err, ErrProviderFailed) {
		t.Errorf("error = %v, want ErrProviderFailed", err)
	}
	f.account("a@gmail.com").ShareErr = nil
	list, err := f.service.ListShares(f.ctx, user)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(list.Shares) != 1 {
		t.Errorf("a working link went missing from the list: %+v", list.Shares)
	}
	if _, ok := f.public("a@gmail.com", object); !ok {
		t.Error("the fake should still be public here")
	}
}

func testTrashEndsLink(t *testing.T, f *uploadFixture) {
	user, _, file, object := f.sharedFile(t)
	if err := f.service.TrashFile(f.ctx, user, file.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("a trashed file is still public")
	}
	if got := f.count(t, `SELECT count(*) FROM file_shares`); got != 0 {
		t.Errorf("%d rows", got)
	}
}

func testTrashNeedsLinkGone(t *testing.T, f *uploadFixture) {
	user, _, file, _ := f.sharedFile(t)
	f.account("a@gmail.com").ShareErr = errors.New("backend error")
	if err := f.service.TrashFile(f.ctx, user, file.ID); !errors.Is(err, ErrProviderFailed) {
		t.Errorf("error = %v, want ErrProviderFailed", err)
	}
	f.account("a@gmail.com").ShareErr = nil
	if _, err := liveFile(f.ctx, f.service.store, user, file.ID); err != nil {
		t.Errorf("the file went to the trash with a working link: %v", err)
	}
}

func testRestoreLeavesLinkGone(t *testing.T, f *uploadFixture) {
	user, _, file, object := f.sharedFile(t)
	if err := f.service.TrashFile(f.ctx, user, file.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.RestoreFile(f.ctx, user, file.ID); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("restoring made a file public again without anyone choosing that")
	}
}

func testTrashFolderEndsLinks(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	top := f.folder(t, user, nil, "Top")
	deep := f.folder(t, user, &top.ID, "Deep")
	near := f.putIn(t, user, a, &top.ID, "near.txt", "x")
	far := f.putIn(t, user, a, &deep.ID, "far.txt", "y")
	outside := f.putIn(t, user, a, nil, "outside.txt", "z")
	for _, id := range []uuid.UUID{near.ID, far.ID, outside.ID} {
		f.share(t, user, id, "viewer")
	}
	if err := f.service.TrashFolder(f.ctx, user, top.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	for _, id := range []uuid.UUID{near.ID, far.ID} {
		if _, ok := f.public("a@gmail.com", f.objectID(t, user, id)); ok {
			t.Errorf("%s is still public inside the trash", id)
		}
	}
	if _, ok := f.public("a@gmail.com", f.objectID(t, user, outside.ID)); !ok {
		t.Error("a link outside the folder went too")
	}
}

// shareHook runs something once, just before Google's answer lands, which is the order
// that leaves nobody else to clean up.
type shareHook struct {
	provider.Provider
	once *sync.Once
	run  func()
}

func (h shareHook) Share(ctx context.Context, id string, role provider.ShareRole) (string, error) {
	h.once.Do(h.run)
	return h.Provider.Share(ctx, id, role)
}

func testShareRacesTrash(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	object := f.objectID(t, user, file.ID)
	var once sync.Once
	f.only("a@gmail.com", func(p provider.Provider) provider.Provider {
		return shareHook{p, &once, func() {
			if err := f.service.TrashFile(f.ctx, user, file.ID); err != nil {
				t.Errorf("TrashFile: %v", err)
			}
		}}
	})
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer"}, ""); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("error = %v, want ErrFileNotFound", err)
	}
	f.wrap = nil
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("a trashed file was left public")
	}
}

func (f *uploadFixture) expireAll(t *testing.T) {
	t.Helper()
	if _, err := f.pool.Exec(f.ctx, `UPDATE file_shares SET expires_at = now() - interval '1 second'`); err != nil {
		t.Fatalf("expire: %v", err)
	}
}

func testShareExpires(t *testing.T, f *uploadFixture) {
	_, _, _, object := f.sharedFile(t)
	f.expireAll(t)
	run, err := f.service.ExpireShares(f.ctx)
	if err != nil {
		t.Fatalf("ExpireShares: %v", err)
	}
	if run.Expired != 1 {
		t.Errorf("run = %+v", run)
	}
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("an expired link still works")
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'file.share_expired'`); got != 1 {
		t.Errorf("%d audit events", got)
	}
}

func testShareNotDue(t *testing.T, f *uploadFixture) {
	user, _, file, object := f.sharedFile(t)
	later := time.Now().Add(time.Hour)
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer", ExpiresAt: &later}, ""); err != nil {
		t.Fatalf("ShareFile: %v", err)
	}
	if run, err := f.service.ExpireShares(f.ctx); err != nil || run.Expired != 0 {
		t.Fatalf("run = %+v, error %v", run, err)
	}
	if _, ok := f.public("a@gmail.com", object); !ok {
		t.Error("a link ended early")
	}
}

func testShareExpiryRetried(t *testing.T, f *uploadFixture) {
	user, _, _, object := f.sharedFile(t)
	f.expireAll(t)
	f.account("a@gmail.com").ShareErr = errors.New("backend error")
	run, err := f.service.ExpireShares(f.ctx)
	if err != nil {
		t.Fatalf("ExpireShares: %v", err)
	}
	if run.Expired != 0 || len(run.Failures) != 1 {
		t.Errorf("run = %+v", run)
	}
	list, err := f.service.ListShares(f.ctx, user)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(list.Shares) != 1 {
		t.Error("a link that's still working left the list")
	}
	f.account("a@gmail.com").ShareErr = nil
	if run, err := f.service.ExpireShares(f.ctx); err != nil || run.Expired != 1 {
		t.Fatalf("run = %+v, error %v", run, err)
	}
	if _, ok := f.public("a@gmail.com", object); ok {
		t.Error("still public")
	}
}

func testSharedFileCantMove(t *testing.T, f *uploadFixture) {
	user, _, file, _ := f.sharedFile(t)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	if _, err := f.service.TransferFiles(f.ctx, user, []uuid.UUID{file.ID}, b, ""); !errors.Is(err, ErrFileShared) {
		t.Errorf("error = %v, want ErrFileShared", err)
	}
}

func testSharedAccountCantEmpty(t *testing.T, f *uploadFixture) {
	user, a, _, _ := f.sharedFile(t)
	f.link(t, user, "b@gmail.com", 1<<30)
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); !errors.Is(err, ErrFileShared) {
		t.Errorf("error = %v, want ErrFileShared", err)
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections WHERE draining`); got != 0 {
		t.Errorf("%d draining", got)
	}
}

func testSharedAfterQueueing(t *testing.T, f *uploadFixture) {
	f.allowLinks(t)
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	f.queue(t, user, b, file.ID)
	if _, err := f.pool.Exec(f.ctx, `INSERT INTO file_shares (file_id, user_id, role, url) VALUES ($1, $2, 'viewer', 'https://x')`, file.ID, user); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if run := f.runMoves(t); run.Moved != 0 || run.Failed != 1 {
		t.Fatalf("run = %+v", run)
	}
	if got := f.failure(t, user); got != failureShared {
		t.Errorf("failure = %q", got)
	}
	if got := f.connectionOf(t, user, file.ID); got != a {
		t.Errorf("moved to %s", got)
	}
}

func testSharedAccountCantUnlink(t *testing.T, f *uploadFixture) {
	user, a, file, _ := f.sharedFile(t)
	if err := f.storage.Delete(f.ctx, user, a, ""); !errors.Is(err, storage.ErrConnectionHasLinks) {
		t.Fatalf("error = %v, want ErrConnectionHasLinks", err)
	}
	if err := f.service.UnshareFile(f.ctx, user, file.ID, ""); err != nil {
		t.Fatalf("UnshareFile: %v", err)
	}
	if err := f.storage.Delete(f.ctx, user, a, ""); err != nil {
		t.Errorf("Delete: %v", err)
	}
}

func testDeadAccountUnlinks(t *testing.T, f *uploadFixture) {
	user, a, _, _ := f.sharedFile(t)
	f.account("a@gmail.com").QuotaErr = errors.New("token expired")
	if err := f.storage.Delete(f.ctx, user, a, ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'storage.removed' AND metadata->>'links_left' = '1'`); got != 1 {
		t.Errorf("%d audit events recording the link left behind", got)
	}
}

func testShareIsolation(t *testing.T, f *uploadFixture) {
	owner, _, file, object := f.sharedFile(t)
	stranger := f.user(t)
	f.link(t, stranger, "s@gmail.com", 1<<30)
	if _, err := f.service.ShareFile(f.ctx, stranger, file.ID, ShareInput{Role: "editor"}, ""); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("changing theirs: error = %v", err)
	}
	if err := f.service.UnshareFile(f.ctx, stranger, file.ID, ""); !errors.Is(err, ErrShareNotFound) {
		t.Errorf("removing theirs: error = %v", err)
	}
	list, err := f.service.ListShares(f.ctx, stranger)
	if err != nil {
		t.Fatalf("ListShares: %v", err)
	}
	if len(list.Shares) != 0 {
		t.Errorf("a stranger sees %+v", list.Shares)
	}
	if got, ok := f.public("a@gmail.com", object); !ok || got != provider.ShareViewer {
		t.Errorf("the link changed: %q, %v", got, ok)
	}
	if _, found, err := f.service.store.ShareForOwner(f.ctx, stranger, file.ID); err != nil || found {
		t.Errorf("found %v, error %v", found, err)
	}
	if _, found, err := f.service.store.ShareForOwner(f.ctx, owner, file.ID); err != nil || !found {
		t.Errorf("the owner lost it: found %v, error %v", found, err)
	}
}

func TestShareHandlers(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	f := newUploadFixture(t, pool)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpx.NewRouter(httpx.Deps{
		Logger:       logger,
		DB:           pool,
		PublicOrigin: testOrigin,
		Sessions:     sessions{},
		APIKeys:      keys{},
		Auth:         apigen.Unimplemented{},
		Admin:        apigen.Unimplemented{},
		Storage:      apigen.Unimplemented{},
		Files:        NewHandler(f.service, logger),
	})
	owner := f.user(t)
	a := f.link(t, owner, "a@gmail.com", 1<<30)
	file := f.putIn(t, owner, a, nil, "a.txt", "hello")
	path := "/api/v1/files/" + file.ID.String() + "/share"
	call := func(user uuid.UUID, method, target, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, target, strings.NewReader(body))
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: user.String()})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}

	if rec := call(owner, http.MethodPut, path, `{"role":"viewer"}`); rec.Code != http.StatusForbidden {
		t.Errorf("off: status = %d", rec.Code)
	}
	f.allowLinks(t)
	if rec := call(f.user(t), http.MethodPut, path, `{"role":"viewer"}`); rec.Code != http.StatusNotFound {
		t.Errorf("a stranger: status = %d", rec.Code)
	}
	if rec := call(owner, http.MethodPut, path, `{"role":"owner"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("bad role: status = %d", rec.Code)
	}
	rec := call(owner, http.MethodPut, path, `{"role":"commenter","expires_at":"2099-01-01T00:00:00Z"}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"role":"commenter"`) {
		t.Fatalf("share: status = %d, body %s", rec.Code, rec.Body)
	}
	rec = call(owner, http.MethodGet, "/api/v1/shares", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"a.txt"`) || !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Errorf("list: status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := call(f.user(t), http.MethodDelete, path, ""); rec.Code != http.StatusNotFound {
		t.Errorf("removing as a stranger: status = %d", rec.Code)
	}
	if rec := call(owner, http.MethodDelete, path, ""); rec.Code != http.StatusNoContent {
		t.Errorf("remove: status = %d", rec.Code)
	}
	if rec := call(owner, http.MethodDelete, path, ""); rec.Code != http.StatusNotFound {
		t.Errorf("remove twice: status = %d", rec.Code)
	}
}
