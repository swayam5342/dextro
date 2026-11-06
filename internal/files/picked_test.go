package files

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/storage"
)

// picked puts a file in an account that dextro didn't create, as someone's own
// files are. Listings leave it out, because it isn't in dextro's folder.
func (f *uploadFixture) picked(t *testing.T, email, name, contentType string) string {
	t.Helper()
	account := f.account(email)
	object, err := account.Put(f.ctx, provider.Upload{Name: name, ContentType: contentType, Body: strings.NewReader("mine")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	account.Age(longAgo)
	account.Unlist(object.ID)
	return object.ID
}

func (f *uploadFixture) add(t *testing.T, userID, connection uuid.UUID, ids ...string) ImportResult {
	t.Helper()
	result, err := f.service.ImportPicked(f.ctx, userID, connection, ids, "203.0.113.7")
	if err != nil {
		t.Fatalf("ImportPicked: %v", err)
	}
	return result
}

func (f *uploadFixture) stillInDrive(t *testing.T, email, id string) bool {
	t.Helper()
	exists, err := f.account(email).Exists(f.ctx, id)
	if err != nil {
		t.Fatalf("Exists: %v", err)
	}
	return exists
}

func TestPickedFiles(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"a picked file joins dextro", testPickedFileIsAdded},
		{"adding a file twice changes nothing", testPickedTwice},
		{"google docs can't be added", testPickedDocsSkipped},
		{"a file that's gone is skipped", testPickedMissingSkipped},
		{"the list of files is checked", testPickedValidates},
		{"someone else's account can't be used", testPickedIsScoped},
		{"a failure stops and says so", testPickedProviderFailure},
		{"adding files is audited", testPickedAudited},
		{"deleting forever leaves a picked file in drive", testDeletePickedKeepsDriveFile},
		{"deleting a folder leaves picked files in drive", testDeleteFolderKeepsPickedFile},
		{"emptying the trash leaves picked files in drive", testEmptyTrashKeepsPickedFile},
		{"files dextro uploaded are still deleted from drive", testDeleteUploadedStillDeletes},
		{"a sync keeps a picked file outside dextro's folder", testSyncKeepsPicked},
		{"a sync removes a picked file deleted in drive", testSyncRemovesPicked},
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

func testPickedFileIsAdded(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	id := f.picked(t, "a@gmail.com", "holiday.jpg", "image/jpeg")
	if got := f.add(t, user, connection, id); got.Added != 1 || got.Skipped != 0 {
		t.Fatalf("result = %+v", got)
	}
	contents, err := f.service.List(f.ctx, user, nil)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(contents.Files) != 1 {
		t.Fatalf("files = %+v", contents.Files)
	}
	file := contents.Files[0]
	if file.Name != "holiday.jpg" || file.ContentType != "image/jpeg" || file.Size != 4 || !file.External {
		t.Errorf("file = %+v", file)
	}
}

func testPickedTwice(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	id := f.picked(t, "a@gmail.com", "a.jpg", "image/jpeg")
	f.add(t, user, connection, id)
	if got := f.add(t, user, connection, id); got.Added != 0 || got.Skipped != 1 {
		t.Errorf("result = %+v", got)
	}
	if got := f.files(t, user); got != 1 {
		t.Errorf("files = %d", got)
	}
}

func testPickedDocsSkipped(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	doc := f.picked(t, "a@gmail.com", "Notes", "application/vnd.google-apps.document")
	real := f.picked(t, "a@gmail.com", "a.pdf", "application/pdf")
	if got := f.add(t, user, connection, doc, real); got.Added != 1 || got.Skipped != 1 {
		t.Errorf("result = %+v", got)
	}
}

func testPickedMissingSkipped(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	if got := f.add(t, user, connection, "no-such-file"); got.Added != 0 || got.Skipped != 1 {
		t.Errorf("result = %+v", got)
	}
}

func testPickedValidates(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	if _, err := f.service.ImportPicked(f.ctx, user, connection, nil, "ip"); !errors.Is(err, ErrInvalidImport) {
		t.Errorf("nothing picked: error = %v", err)
	}
	if _, err := f.service.ImportPicked(f.ctx, user, connection, make([]string, maxPicked+1), "ip"); !errors.Is(err, ErrInvalidImport) {
		t.Errorf("too many: error = %v", err)
	}
}

func testPickedIsScoped(t *testing.T, f *uploadFixture) {
	owner := f.user(t)
	connection := f.link(t, owner, "a@gmail.com", 1<<30)
	id := f.picked(t, "a@gmail.com", "a.jpg", "image/jpeg")
	stranger := f.user(t)
	if _, err := f.service.ImportPicked(f.ctx, stranger, connection, []string{id}, "ip"); !errors.Is(err, storage.ErrConnectionNotFound) {
		t.Errorf("error = %v, want ErrConnectionNotFound", err)
	}
	if got := f.files(t, stranger); got != 0 {
		t.Errorf("files = %d", got)
	}
}

func testPickedProviderFailure(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	id := f.picked(t, "a@gmail.com", "a.jpg", "image/jpeg")
	f.account("a@gmail.com").ExistsErr = errors.New("backend error")
	if _, err := f.service.ImportPicked(f.ctx, user, connection, []string{id}, "ip"); !errors.Is(err, ErrProviderFailed) {
		t.Errorf("error = %v, want ErrProviderFailed", err)
	}
}

func testPickedAudited(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.add(t, user, connection, f.picked(t, "a@gmail.com", "a.jpg", "image/jpeg"))
	got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'storage.files_added' AND actor_id = $1 AND metadata->>'added' = '1'`, user)
	if got != 1 {
		t.Errorf("audit events = %d, want 1", got)
	}
}

// addedFile adds a picked file and returns its ID in dextro and in the account.
func (f *uploadFixture) addedFile(t *testing.T, userID, connection uuid.UUID) (uuid.UUID, string) {
	t.Helper()
	object := f.picked(t, "a@gmail.com", "mine.jpg", "image/jpeg")
	f.add(t, userID, connection, object)
	var id uuid.UUID
	if err := f.pool.QueryRow(f.ctx, `SELECT id FROM files WHERE object_id = $1`, object).Scan(&id); err != nil {
		t.Fatalf("find file: %v", err)
	}
	return id, object
}

func testDeletePickedKeepsDriveFile(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	id, object := f.addedFile(t, user, connection)
	if err := f.service.TrashFile(f.ctx, user, id); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.DeleteFile(f.ctx, user, id, "ip"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if got := f.files(t, user); got != 0 {
		t.Errorf("files = %d, it should have left dextro", got)
	}
	if !f.stillInDrive(t, "a@gmail.com", object) {
		t.Error("deleting forever removed someone's own file from Drive")
	}
}

func testDeleteFolderKeepsPickedFile(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	id, object := f.addedFile(t, user, connection)
	folder, err := f.service.CreateFolder(f.ctx, user, nil, "Trip")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	if _, err := f.service.MoveFile(f.ctx, user, id, &folder.ID); err != nil {
		t.Fatalf("MoveFile: %v", err)
	}
	if err := f.service.TrashFolder(f.ctx, user, folder.ID); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	if err := f.service.DeleteFolder(f.ctx, user, folder.ID, "ip"); err != nil {
		t.Fatalf("DeleteFolder: %v", err)
	}
	if !f.stillInDrive(t, "a@gmail.com", object) {
		t.Error("deleting a folder removed someone's own file from Drive")
	}
}

func testEmptyTrashKeepsPickedFile(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	id, object := f.addedFile(t, user, connection)
	if err := f.service.TrashFile(f.ctx, user, id); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.EmptyTrash(f.ctx, user, "ip"); err != nil {
		t.Fatalf("EmptyTrash: %v", err)
	}
	if !f.stillInDrive(t, "a@gmail.com", object) {
		t.Error("emptying the trash removed someone's own file from Drive")
	}
}

func testDeleteUploadedStillDeletes(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "uploaded.txt")
	object := f.objectID(t, user, file.ID)
	if err := f.service.TrashFile(f.ctx, user, file.ID); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.DeleteFile(f.ctx, user, file.ID, "ip"); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if f.stillInDrive(t, "a@gmail.com", object) {
		t.Error("a file dextro uploaded stayed in Drive after deleting it forever")
	}
}

func testSyncKeepsPicked(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	f.addedFile(t, user, connection)
	if got := f.sync(t, user, connection); got.Removed != 0 {
		t.Errorf("result = %+v", got)
	}
}

func testSyncRemovesPicked(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	_, object := f.addedFile(t, user, connection)
	if err := f.account("a@gmail.com").Delete(f.ctx, object); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.sync(t, user, connection); got.Removed != 1 {
		t.Errorf("result = %+v", got)
	}
}
