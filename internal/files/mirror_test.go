package files

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/provider"
)

// inDrive finds an object by name in an account.
func (f *uploadFixture) inDrive(t *testing.T, email, name string) provider.Object {
	t.Helper()
	for _, object := range f.account(email).Snapshot() {
		if object.Name == name {
			return object
		}
	}
	t.Fatalf("%q isn't in %s", name, email)
	return provider.Object{}
}

func (f *uploadFixture) countInDrive(email, name string) int {
	n := 0
	for _, object := range f.account(email).Snapshot() {
		if object.Name == name {
			n++
		}
	}
	return n
}

func (f *uploadFixture) folder(t *testing.T, userID uuid.UUID, parent *uuid.UUID, name string) Folder {
	t.Helper()
	folder, err := f.service.CreateFolder(f.ctx, userID, parent, name)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	return folder
}

func (f *uploadFixture) put1(t *testing.T, userID uuid.UUID, folder *uuid.UUID, name string) File {
	t.Helper()
	file, err := f.service.Upload(f.ctx, userID, UploadInput{FolderID: folder, Name: name, ContentType: "text/plain", Size: 5, Body: strings.NewReader("hello")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	return file
}

// wipe loses the database's knowledge of a user's files and folders, as restoring
// an empty database and linking the account again would.
func (f *uploadFixture) wipe(t *testing.T, userID uuid.UUID) {
	t.Helper()
	for _, query := range []string{
		`DELETE FROM files WHERE user_id = $1`,
		`DELETE FROM folders WHERE user_id = $1`,
		`UPDATE storage_connections SET folders_synced_at = NULL WHERE user_id = $1`,
	} {
		if _, err := f.pool.Exec(f.ctx, query, userID); err != nil {
			t.Fatalf("wipe: %v", err)
		}
	}
}

func (f *uploadFixture) ageAll(t *testing.T, email string) {
	t.Helper()
	f.account(email).Age(longAgo)
	if _, err := f.pool.Exec(f.ctx, `UPDATE files SET created_at = $1`, time.Now().Add(-longAgo)); err != nil {
		t.Fatalf("age: %v", err)
	}
}

func TestRealFolders(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"a new folder appears in every account", testFolderInEveryAccount},
		{"folders nest in drive", testFoldersNest},
		{"an account that's down doesn't stop a new folder", testFolderSurvivesBrokenAccount},
		{"a sync creates what an account missed", testSyncCreatesPendingFolders},
		{"an upload lands in its folder", testUploadLandsInFolder},
		{"a chunked upload lands in its folder", testChunkedUploadLandsInFolder},
		{"two uploads don't make two folders", testConcurrentUploadsOneFolder},
		{"renaming a file changes drive", testRenameFileInDrive},
		{"a rename drive refuses changes nothing", testRenameFileFails},
		{"moving a file changes drive", testMoveFileInDrive},
		{"moving to the top changes drive", testMoveFileToTop},
		{"a move drive refuses changes nothing", testMoveFileFails},
		{"added files are left where they are", testExternalFilesStay},
		{"renaming a folder changes every account", testRenameFolderInDrive},
		{"moving a folder changes every account", testMoveFolderInDrive},
		{"a sync finishes a rename an account missed", testSyncFinishesStaleFolder},
		{"a restored folder goes back to the top in drive", testRestoredFolderMovesInDrive},
		{"deleting a folder sends it to drive's trash", testDeleteFolderTrashesInDrive},
		{"something added by hand survives in drive's trash", testHandAddedFileSurvives},
		{"a folder deleted in drive leaves the app", testFolderDeletedInDrive},
		{"a folder deleted in one account stays in the others", testFolderDeletedInOneAccount},
		{"a folder deleted in drive isn't made again", testDeletedFolderNotRecreated},
		{"it comes back when a file needs it", testDeletedFolderMadeOnDemand},
		{"folders come back after losing the database", testRebuildAfterDatabaseLoss},
		{"accounts merge into one folder tree", testRebuildMergesAccounts},
		{"existing flat files move into real folders", testBackfillExistingFiles},
		{"the backfill runs once", testBackfillRunsOnce},
		{"a new account gets the existing folders", testNewAccountGetsFolders},
		{"a sync says why it skipped an account", testSyncSaysWhy},
		{"a file removed from dextro doesn't come back", testRemovedFileStaysRemoved},
		{"a folder the listing missed stays", testLaggingFolderStays},
		{"a brand-new folder isn't adopted yet", testNewFolderNotAdopted},
		{"an emptied parent goes too", testEmptiedParentGoes},
		{"a folder with files in it stays", testFolderWithFilesStays},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
				t.Fatalf("reset database: %v", err)
			}
			tt.run(t, newUploadFixture(t, pool))
		})
	}
}

func testFolderInEveryAccount(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	f.folder(t, user, nil, "Photos")
	for _, email := range []string{"a@gmail.com", "b@gmail.com"} {
		got := f.inDrive(t, email, "Photos")
		if !got.Folder || got.Parent != f.account(email).RootID() {
			t.Errorf("%s: %+v", email, got)
		}
	}
}

func testFoldersNest(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	outer := f.folder(t, user, nil, "Outer")
	f.folder(t, user, &outer.ID, "Inner")
	if got := f.inDrive(t, "a@gmail.com", "Inner"); got.Parent != f.inDrive(t, "a@gmail.com", "Outer").ID {
		t.Errorf("inner = %+v", got)
	}
}

func testFolderSurvivesBrokenAccount(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	f.account("b@gmail.com").FolderErr = errors.New("token expired")
	f.folder(t, user, nil, "Photos")
	if !f.inDrive(t, "a@gmail.com", "Photos").Folder {
		t.Error("the working account missed out")
	}
}

func testSyncCreatesPendingFolders(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	broken := f.link(t, user, "b@gmail.com", 1<<30)
	account := f.account("b@gmail.com")
	account.FolderErr = errors.New("token expired")
	f.folder(t, user, nil, "Photos")
	account.FolderErr = nil
	f.sync(t, user, broken)
	if !f.inDrive(t, "b@gmail.com", "Photos").Folder {
		t.Error("the account never caught up")
	}
	f.sync(t, user, broken)
	if got := f.countInDrive("b@gmail.com", "Photos"); got != 1 {
		t.Errorf("folders = %d", got)
	}
}

func testUploadLandsInFolder(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Photos")
	f.put1(t, user, &folder.ID, "a.txt")
	if got := f.inDrive(t, "a@gmail.com", "a.txt"); got.Parent != f.inDrive(t, "a@gmail.com", "Photos").ID {
		t.Errorf("file = %+v", got)
	}
}

func testChunkedUploadLandsInFolder(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Photos")
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{FolderID: &folder.ID, Name: "c.txt", Size: 3})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	f.put(t, user, session.ID, 0, "abc")
	if got := f.inDrive(t, "a@gmail.com", "c.txt"); got.Parent != f.inDrive(t, "a@gmail.com", "Photos").ID {
		t.Errorf("file = %+v", got)
	}
}

func testConcurrentUploadsOneFolder(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Photos")
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM folder_objects`); err != nil {
		t.Fatalf("forget: %v", err)
	}
	f.account("a@gmail.com").Hide(f.inDrive(t, "a@gmail.com", "Photos").ID)
	// Everyone reaches the folder at the same moment, finding it missing.
	var arrived sync.WaitGroup
	arrived.Add(6)
	f.wrap = func(p provider.Provider) provider.Provider { return gate{p, &arrived} }
	done := make(chan error, 6)
	for range 6 {
		go func() {
			_, err := f.service.Upload(f.ctx, user, UploadInput{FolderID: &folder.ID, Name: "x.txt", Size: 1, Body: strings.NewReader("x")})
			done <- err
		}()
	}
	for range 6 {
		if err := <-done; err != nil {
			t.Errorf("Upload: %v", err)
		}
	}
	if got := f.countInDrive("a@gmail.com", "Photos"); got != 2 {
		t.Errorf("folders named Photos = %d, want the hidden one and one more", got)
	}
}

func testRenameFileInDrive(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	file := f.put1(t, user, nil, "before.txt")
	renamed, err := f.service.RenameFile(f.ctx, user, file.ID, "after.txt")
	if err != nil {
		t.Fatalf("RenameFile: %v", err)
	}
	if renamed.Name != "after.txt" || f.countInDrive("a@gmail.com", "after.txt") != 1 {
		t.Errorf("renamed = %+v", renamed)
	}
}

func testRenameFileFails(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	file := f.put1(t, user, nil, "before.txt")
	f.account("a@gmail.com").FolderErr = errors.New("backend error")
	if _, err := f.service.RenameFile(f.ctx, user, file.ID, "after.txt"); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	got, err := f.service.Get(f.ctx, user, file.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "before.txt" {
		t.Errorf("name = %q, dextro and Drive disagree", got.Name)
	}
}

func testMoveFileInDrive(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Photos")
	file := f.put1(t, user, nil, "a.txt")
	if _, err := f.service.MoveFile(f.ctx, user, file.ID, &folder.ID); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := f.inDrive(t, "a@gmail.com", "a.txt"); got.Parent != f.inDrive(t, "a@gmail.com", "Photos").ID {
		t.Errorf("file = %+v", got)
	}
}

func testMoveFileToTop(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Photos")
	file := f.put1(t, user, &folder.ID, "a.txt")
	if _, err := f.service.MoveFile(f.ctx, user, file.ID, nil); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if got := f.inDrive(t, "a@gmail.com", "a.txt"); got.Parent != f.account("a@gmail.com").RootID() {
		t.Errorf("file = %+v", got)
	}
}

func testMoveFileFails(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Photos")
	file := f.put1(t, user, nil, "a.txt")
	f.account("a@gmail.com").FolderErr = errors.New("backend error")
	if _, err := f.service.MoveFile(f.ctx, user, file.ID, &folder.ID); !errors.Is(err, ErrProviderFailed) {
		t.Fatalf("error = %v, want ErrProviderFailed", err)
	}
	got, err := f.service.Get(f.ctx, user, file.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.FolderID != nil {
		t.Errorf("folder = %v, dextro and Drive disagree", got.FolderID)
	}
}

func testExternalFilesStay(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	id, object := f.addedFile(t, user, connection)
	before := f.account("a@gmail.com").Snapshot()[object]
	folder := f.folder(t, user, nil, "Photos")
	moved, err := f.service.MoveFile(f.ctx, user, id, &folder.ID)
	if err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if moved.FolderID == nil {
		t.Errorf("moved = %+v, it should still move inside dextro", moved)
	}
	if _, err := f.service.RenameFile(f.ctx, user, id, "renamed.jpg"); err != nil {
		t.Fatalf("RenameFile: %v", err)
	}
	after := f.account("a@gmail.com").Snapshot()[object]
	if after.Name != before.Name || after.Parent != before.Parent {
		t.Errorf("someone's own file changed in Drive: %+v", after)
	}
}

func testRenameFolderInDrive(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Before")
	if _, err := f.service.RenameFolder(f.ctx, user, folder.ID, "After"); err != nil {
		t.Fatalf("RenameFolder: %v", err)
	}
	for _, email := range []string{"a@gmail.com", "b@gmail.com"} {
		if got := f.countInDrive(email, "After"); got != 1 {
			t.Errorf("%s: folders = %d", email, got)
		}
	}
}

func testMoveFolderInDrive(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	target := f.folder(t, user, nil, "Target")
	folder := f.folder(t, user, nil, "Moving")
	if _, err := f.service.MoveFolder(f.ctx, user, folder.ID, &target.ID); err != nil {
		t.Fatalf("MoveFolder: %v", err)
	}
	for _, email := range []string{"a@gmail.com", "b@gmail.com"} {
		if got := f.inDrive(t, email, "Moving"); got.Parent != f.inDrive(t, email, "Target").ID {
			t.Errorf("%s: folder = %+v", email, got)
		}
	}
}

func testSyncFinishesStaleFolder(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Before")
	account := f.account("a@gmail.com")
	account.FolderErr = errors.New("backend error")
	if _, err := f.service.RenameFolder(f.ctx, user, folder.ID, "After"); err != nil {
		t.Fatalf("RenameFolder: %v, a folder rename must survive a broken account", err)
	}
	account.FolderErr = nil
	f.sync(t, user, connection)
	if got := f.countInDrive("a@gmail.com", "After"); got != 1 {
		t.Errorf("folders = %d, the rename never reached Drive", got)
	}
}

func testRestoredFolderMovesInDrive(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	outer := f.folder(t, user, nil, "Outer")
	inner := f.folder(t, user, &outer.ID, "Inner")
	if err := f.service.TrashFolder(f.ctx, user, inner.ID, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if err := f.service.TrashFolder(f.ctx, user, outer.ID, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if err := f.service.RestoreFolder(f.ctx, user, inner.ID); err != nil {
		t.Fatalf("RestoreFolder: %v", err)
	}
	if got := f.inDrive(t, "a@gmail.com", "Inner"); got.Parent != f.account("a@gmail.com").RootID() {
		t.Errorf("folder = %+v", got)
	}
}

func (f *uploadFixture) deleteFolder(t *testing.T, userID, id uuid.UUID) {
	t.Helper()
	if err := f.service.TrashFolder(f.ctx, userID, id, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if err := f.service.DeleteFolder(f.ctx, userID, id, "ip"); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
}

func testDeleteFolderTrashesInDrive(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Photos")
	id := f.inDrive(t, "a@gmail.com", "Photos").ID
	f.deleteFolder(t, user, folder.ID)
	if exists, err := f.account("a@gmail.com").Exists(f.ctx, id); err != nil || exists {
		t.Errorf("exists = %v, error = %v", exists, err)
	}
	if got := f.countInDrive("a@gmail.com", "Photos"); got != 1 {
		t.Errorf("objects = %d, it should be in the trash, not deleted", got)
	}
}

func testHandAddedFileSurvives(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Photos")
	account := f.account("a@gmail.com")
	byHand, err := account.Put(f.ctx, provider.Upload{Name: "by-hand.txt", Parent: f.inDrive(t, "a@gmail.com", "Photos").ID, Body: strings.NewReader("mine")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	f.deleteFolder(t, user, folder.ID)
	if _, ok := account.Snapshot()[byHand.ID]; !ok {
		t.Error("deleting a folder destroyed a file dextro couldn't see")
	}
}

func testFolderDeletedInDrive(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.folder(t, user, nil, "Photos")
	f.account("a@gmail.com").Hide(f.inDrive(t, "a@gmail.com", "Photos").ID)
	f.sync(t, user, connection)
	if got := f.count(t, `SELECT count(*) FROM folders WHERE user_id = $1`, user); got != 0 {
		t.Errorf("folders = %d", got)
	}
}

func testFolderDeletedInOneAccount(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	f.folder(t, user, nil, "Photos")
	f.account("a@gmail.com").Hide(f.inDrive(t, "a@gmail.com", "Photos").ID)
	f.sync(t, user, connection)
	if got := f.count(t, `SELECT count(*) FROM folders WHERE user_id = $1`, user); got != 1 {
		t.Errorf("folders = %d, the other account still has it", got)
	}
}

func testDeletedFolderNotRecreated(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	f.folder(t, user, nil, "Photos")
	f.account("a@gmail.com").Hide(f.inDrive(t, "a@gmail.com", "Photos").ID)
	f.sync(t, user, connection)
	f.sync(t, user, connection)
	if got := f.countInDrive("a@gmail.com", "Photos"); got != 1 {
		t.Errorf("objects = %d, a folder you deleted came back", got)
	}
}

func testDeletedFolderMadeOnDemand(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<20)
	folder := f.folder(t, user, nil, "Photos")
	gone := f.inDrive(t, "a@gmail.com", "Photos").ID
	f.account("a@gmail.com").Hide(gone)
	f.sync(t, user, connection)
	f.put1(t, user, &folder.ID, "a.txt")
	got := f.inDrive(t, "a@gmail.com", "a.txt")
	if got.Parent == gone || got.Parent == f.account("a@gmail.com").RootID() {
		t.Errorf("file = %+v, it should be in a new Photos", got)
	}
}

func testRebuildAfterDatabaseLoss(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	outer := f.folder(t, user, nil, "Trips")
	inner := f.folder(t, user, &outer.ID, "Goa")
	file := f.put1(t, user, &inner.ID, "beach.txt")
	if _, err := f.service.RenameFile(f.ctx, user, file.ID, "sunset.txt"); err != nil {
		t.Fatalf("RenameFile: %v", err)
	}
	f.wipe(t, user)
	f.account("a@gmail.com").Age(longAgo)

	if got := f.sync(t, user, connection); got.Added != 1 {
		t.Fatalf("result = %+v", got)
	}
	top, err := f.service.List(f.ctx, user, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(top.Folders) != 1 || top.Folders[0].Name != "Trips" || len(top.Files) != 0 {
		t.Fatalf("top = %+v", top)
	}
	trips, err := f.service.List(f.ctx, user, &top.Folders[0].ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(trips.Folders) != 1 || trips.Folders[0].Name != "Goa" {
		t.Fatalf("trips = %+v", trips)
	}
	goa, err := f.service.List(f.ctx, user, &trips.Folders[0].ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(goa.Files) != 1 || goa.Files[0].Name != "sunset.txt" {
		t.Errorf("goa = %+v, the renamed file should come back renamed", goa)
	}
}

func testRebuildMergesAccounts(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	first := f.link(t, user, "a@gmail.com", 1<<30)
	second := f.link(t, user, "b@gmail.com", 1<<30)
	f.folder(t, user, nil, "Photos")
	f.wipe(t, user)
	f.account("a@gmail.com").Age(longAgo)
	f.account("b@gmail.com").Age(longAgo)
	f.sync(t, user, first)
	f.sync(t, user, second)
	if got := f.count(t, `SELECT count(*) FROM folders WHERE user_id = $1`, user); got != 1 {
		t.Errorf("folders = %d, the same folder in two accounts is one folder", got)
	}
	if got := f.count(t, `SELECT count(*) FROM folder_objects WHERE object_id IS NOT NULL`); got != 2 {
		t.Errorf("links = %d", got)
	}
}

func (f *uploadFixture) asBeforeRealFolders(t *testing.T) {
	t.Helper()
	for _, query := range []string{`DELETE FROM folder_objects`, `UPDATE storage_connections SET folders_synced_at = NULL`} {
		if _, err := f.pool.Exec(f.ctx, query); err != nil {
			t.Fatalf("reset: %v", err)
		}
	}
}

func testBackfillExistingFiles(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	folder, err := NewStore(f.pool).CreateFolder(f.ctx, user, nil, "Old")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	file := f.put1(t, user, nil, "stored-name.txt")
	if _, err := f.pool.Exec(f.ctx, `UPDATE files SET folder_id = $2, name = 'shown-name.txt' WHERE id = $1`, file.ID, folder.ID); err != nil {
		t.Fatalf("place: %v", err)
	}
	f.asBeforeRealFolders(t)
	f.ageAll(t, "a@gmail.com")

	if got := f.sync(t, user, connection); got.Added != 0 || got.Removed != 0 {
		t.Errorf("result = %+v", got)
	}
	moved := f.inDrive(t, "a@gmail.com", "shown-name.txt")
	if moved.Parent != f.inDrive(t, "a@gmail.com", "Old").ID {
		t.Errorf("file = %+v", moved)
	}
}

func testBackfillRunsOnce(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.put1(t, user, nil, "a.txt")
	f.ageAll(t, "a@gmail.com")
	f.sync(t, user, connection)
	object := f.objectID(t, user, file.ID)
	if err := f.account("a@gmail.com").Rename(f.ctx, object, "renamed-by-hand.txt"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	f.sync(t, user, connection)
	if got := f.countInDrive("a@gmail.com", "renamed-by-hand.txt"); got != 1 {
		t.Error("a sync undid a change made by hand in Drive")
	}
}

func testNewAccountGetsFolders(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	outer := f.folder(t, user, nil, "Outer")
	f.folder(t, user, &outer.ID, "Inner")
	second := f.link(t, user, "b@gmail.com", 1<<30)
	f.sync(t, user, second)
	if got := f.inDrive(t, "b@gmail.com", "Inner"); got.Parent != f.inDrive(t, "b@gmail.com", "Outer").ID {
		t.Errorf("inner = %+v", got)
	}
}

func testSyncSaysWhy(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.account("a@gmail.com").CheckErr = errors.New("token expired")
	result, err := f.service.SyncAll(f.ctx)
	if err != nil {
		t.Fatalf("SyncAll: %v", err)
	}
	if result.Skipped != 1 || len(result.Failures) != 1 || !strings.Contains(result.Failures[0].Error(), "token expired") {
		t.Errorf("result = %+v", result)
	}
}

// Drive lets Tributary keep seeing a file it was given, so a listing still shows it.
func testRemovedFileStaysRemoved(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	account := f.account("a@gmail.com")
	object, err := account.Put(f.ctx, provider.Upload{Name: "mine.jpg", ContentType: "image/jpeg", Parent: "elsewhere-in-drive", Body: strings.NewReader("mine")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	account.Age(longAgo)
	f.add(t, user, connection, object.ID)
	var id uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM files WHERE object_id = $1`, object.ID).Scan(&id); err != nil {
		t.Fatalf("find file: %v", err)
	}
	if err := f.service.TrashFile(f.ctx, user, id, ""); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.DeleteFile(f.ctx, user, id, "ip"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if got := f.sync(t, user, connection); got.Added != 0 {
		t.Errorf("result = %+v, it came back", got)
	}
}

func testLaggingFolderStays(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.folder(t, user, nil, "Photos")
	f.account("a@gmail.com").Unlist(f.inDrive(t, "a@gmail.com", "Photos").ID)
	f.sync(t, user, connection)
	if got := f.count(t, `SELECT count(*) FROM folders WHERE user_id = $1`, user); got != 1 {
		t.Errorf("folders = %d, a slow listing removed a real folder", got)
	}
}

func testNewFolderNotAdopted(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	if _, err := f.account("a@gmail.com").CreateFolder(f.ctx, "", "Just made"); err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	f.sync(t, user, connection)
	if got := f.count(t, `SELECT count(*) FROM folders WHERE user_id = $1`, user); got != 0 {
		t.Errorf("folders = %d", got)
	}
}

func testEmptiedParentGoes(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	outer := f.folder(t, user, nil, "Outer")
	f.folder(t, user, &outer.ID, "Inner")
	f.account("a@gmail.com").Hide(f.inDrive(t, "a@gmail.com", "Outer").ID)
	f.sync(t, user, connection)
	if got := f.count(t, `SELECT count(*) FROM folders WHERE user_id = $1`, user); got != 0 {
		t.Errorf("folders = %d, deleting a folder in Drive takes what's inside it", got)
	}
}

func testFolderWithFilesStays(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	second := f.link(t, user, "b@gmail.com", 1<<20)
	folder := f.folder(t, user, nil, "Photos")
	f.put1(t, user, &folder.ID, "a.txt")
	f.account("b@gmail.com").Hide(f.inDrive(t, "b@gmail.com", "Photos").ID)
	f.sync(t, user, second)
	if got := f.count(t, `SELECT count(*) FROM folders WHERE user_id = $1`, user); got != 1 {
		t.Errorf("folders = %d", got)
	}
	f.sync(t, user, connection)
	if got := f.files(t, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}
