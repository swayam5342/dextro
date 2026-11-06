package files

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
	"github.com/swayam5342/tributary/internal/provider"
	"github.com/swayam5342/tributary/internal/storage"
)

func (f *uploadFixture) putIn(t *testing.T, userID, connection uuid.UUID, folder *uuid.UUID, name, body string) File {
	t.Helper()
	file, err := f.service.Upload(f.ctx, userID, UploadInput{
		ConnectionID: &connection,
		FolderID:     folder,
		Name:         name,
		ContentType:  "text/plain",
		Size:         int64(len(body)),
		Body:         strings.NewReader(body),
	})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	return file
}

func (f *uploadFixture) queue(t *testing.T, userID uuid.UUID, target uuid.UUID, ids ...uuid.UUID) TransferResult {
	t.Helper()
	result, err := f.service.TransferFiles(f.ctx, userID, ids, target, "203.0.113.7")
	if err != nil {
		t.Fatalf("TransferFiles: %v", err)
	}
	return result
}

func (f *uploadFixture) runMoves(t *testing.T) TransferRun {
	t.Helper()
	run, err := f.service.RunTransfers(f.ctx)
	if err != nil {
		t.Fatalf("RunTransfers: %v", err)
	}
	return run
}

func (f *uploadFixture) contentOf(t *testing.T, userID, id uuid.UUID) string {
	t.Helper()
	reader, err := f.service.Open(f.ctx, userID, id, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return string(body)
}

func (f *uploadFixture) connectionOf(t *testing.T, userID, id uuid.UUID) uuid.UUID {
	t.Helper()
	record, err := f.service.store.FileForOwner(f.ctx, userID, id)
	if err != nil {
		t.Fatalf("FileForOwner: %v", err)
	}
	return record.ConnectionID
}

// objects counts an account's files, leaving out folders.
func (f *uploadFixture) objects(email string) int {
	count := 0
	for _, object := range f.account(email).Snapshot() {
		if !object.Folder {
			count++
		}
	}
	return count
}

func (f *uploadFixture) failure(t *testing.T, userID uuid.UUID) string {
	t.Helper()
	queue, err := f.service.ListTransfers(f.ctx, userID)
	if err != nil {
		t.Fatalf("ListTransfers: %v", err)
	}
	if len(queue.Transfers) != 1 {
		t.Fatalf("queue = %+v", queue)
	}
	return queue.Transfers[0].Failure
}

// only applies a change to one account.
func (f *uploadFixture) only(email string, change func(provider.Provider) provider.Provider) {
	f.wrap = func(p provider.Provider) provider.Provider {
		if p == provider.Provider(f.account(email)) {
			return change(p)
		}
		return p
	}
}

func TestTransfers(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"a file moves whole, into the same folder", testTransferMovesFile},
		{"a trashed file stays in the trash", testTransferKeepsTrash},
		{"an empty file moves", testTransferEmptyFile},
		{"a large file goes in chunks", testTransferChunks},
		{"an added file can't move", testTransferRefusesExternal},
		{"a file already there is skipped", testTransferSkipsSameAccount},
		{"a bad request is refused", testTransferBadRequest},
		{"a copy that differs is thrown away", testTransferBadChecksum},
		{"a copy of the wrong size is thrown away", testTransferBadSize},
		{"a chunk that fails is tried again", testTransferRetriesChunk},
		{"an account that keeps less is read again from there", testTransferKeptLess},
		{"a copy that can't finish leaves the file alone", testTransferGivesUp},
		{"a file gone from its account fails", testTransferSourceMissing},
		{"an old copy that won't delete is tried again", testTransferRemovalRetried},
		{"an old copy waiting to go isn't imported", testTransferSyncLeavesOldCopy},
		{"a sync during a move doesn't lose the file", testTransferDuringSync},
		{"a file deleted during its copy takes the copy with it", testTransferFileDeleted},
		{"a move cancelled during its copy stops", testTransferCancelled},
		{"a rename during a copy is kept", testTransferRenamed},
		{"a shutdown leaves the file waiting", testTransferShutdown},
		{"a shutdown before the copy isn't a failure", testTransferShutdownEarly},
		{"a file that changed during its copy keeps its old copy", testTransferFileChanged},
		{"an object a record points at is never removed", testRemovalGuard},
		{"waiting files hold their space", testTransferHoldsSpace},
		{"files that won't fit are refused", testTransferNoRoom},
		{"asking twice doesn't need twice the room", testTransferAskedTwice},
		{"a failed file holds no space", testFailedTransferHoldsNothing},
		{"failed files can be tried again", testTransferRetry},
		{"unlinking the target drops its moves", testTransferUnlinkTarget},
		{"emptying moves everything but added files", testEmptyConnection},
		{"emptying spreads files by room", testEmptySpreads},
		{"emptying is refused if the files won't fit", testEmptyNoRoom},
		{"emptying needs another account", testEmptyOnlyAccount},
		{"an unreachable account can't be emptied", testEmptyUnreachable},
		{"an account being emptied gets no new files", testEmptyKeepsFilesOut},
		{"emptying can stop", testStopEmptying},
		{"emptying again picks up what failed", testEmptyAgain},
		{"memory doesn't grow with a move", testTransferMemoryStaysFlat},
		{"users can't reach each other's moves", testTransferIsolation},
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

func testTransferMovesFile(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Trip")
	file := f.putIn(t, user, a, &folder.ID, "a.txt", "hello")

	if got := f.queue(t, user, b, file.ID); got.Queued != 1 {
		t.Fatalf("result = %+v", got)
	}
	if run := f.runMoves(t); run.Moved != 1 || run.Failed != 0 || run.Removed != 1 {
		t.Fatalf("run = %+v", run)
	}

	if got := f.connectionOf(t, user, file.ID); got != b {
		t.Errorf("connection = %s, want b", got)
	}
	if got := f.contentOf(t, user, file.ID); got != "hello" {
		t.Errorf("content = %q", got)
	}
	if got := f.objects("a@gmail.com"); got != 0 {
		t.Errorf("a still holds %d files", got)
	}
	placed := f.inDrive(t, "b@gmail.com", "a.txt")
	if want := f.inDrive(t, "b@gmail.com", "Trip"); placed.Parent != want.ID {
		t.Errorf("parent = %q, want the Trip folder in b", placed.Parent)
	}
	if got := f.count(t, `SELECT count(*) FROM file_transfers`); got != 0 {
		t.Errorf("%d moves left", got)
	}
	if got := f.count(t, `SELECT count(*) FROM pending_removals`); got != 0 {
		t.Errorf("%d removals left", got)
	}
}

func testTransferKeepsTrash(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	if err := f.service.TrashFile(f.ctx, user, file.ID, ""); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); err != nil {
		t.Fatalf("EmptyConnection: %v", err)
	}
	f.runMoves(t)
	trash, err := f.service.ListTrash(f.ctx, user)
	if err != nil {
		t.Fatalf("ListTrash: %v", err)
	}
	if len(trash.Files) != 1 || trash.Files[0].ConnectionID == a {
		t.Errorf("trash = %+v", trash.Files)
	}
}

func testTransferEmptyFile(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "empty.txt", "")
	f.queue(t, user, b, file.ID)
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("run = %+v", run)
	}
	if got := f.connectionOf(t, user, file.ID); got != b {
		t.Errorf("connection = %s", got)
	}
}

func testTransferChunks(t *testing.T, f *uploadFixture) {
	f.service.transferChunk = align
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	body := strings.Repeat("0123456789", align/2)
	file := f.putIn(t, user, a, nil, "big.bin", body)
	f.queue(t, user, b, file.ID)
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("run = %+v", run)
	}
	if got := f.contentOf(t, user, file.ID); got != body {
		t.Errorf("content differs, %d bytes", len(got))
	}
}

func testTransferRefusesExternal(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	id, _ := f.addedFile(t, user, a)
	if _, err := f.service.TransferFiles(f.ctx, user, []uuid.UUID{id}, b, ""); !errors.Is(err, ErrExternalFile) {
		t.Errorf("error = %v, want ErrExternalFile", err)
	}
}

func testTransferSkipsSameAccount(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	here := f.putIn(t, user, a, nil, "here.txt", "x")
	there := f.putIn(t, user, b, nil, "there.txt", "y")
	if got := f.queue(t, user, b, here.ID, there.ID); got.Queued != 1 || got.Skipped != 1 {
		t.Errorf("result = %+v", got)
	}
	if got := f.queue(t, user, b, here.ID); got.Queued != 0 || got.Skipped != 1 {
		t.Errorf("queued twice: %+v", got)
	}
}

func testTransferBadRequest(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	if _, err := f.service.TransferFiles(f.ctx, user, nil, uuid.New(), ""); !errors.Is(err, ErrInvalidTransfer) {
		t.Errorf("nothing: error = %v", err)
	}
	many := make([]uuid.UUID, maxTransferItems+1)
	if _, err := f.service.TransferFiles(f.ctx, user, many, uuid.New(), ""); !errors.Is(err, ErrInvalidTransfer) {
		t.Errorf("too many: error = %v", err)
	}
}

// lying is an account that says it stored something other than it did.
type lying struct {
	provider.Provider
	checksum string
	size     int64
}

func (l lying) Describe(ctx context.Context, id string) (provider.Object, error) {
	object, err := l.Provider.Describe(ctx, id)
	if l.checksum != "" {
		object.Checksum = l.checksum
	}
	object.Size += l.size
	return object, err
}

func testBadCopy(t *testing.T, f *uploadFixture, bad lying) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	f.only("b@gmail.com", func(p provider.Provider) provider.Provider {
		bad.Provider = p
		return bad
	})
	if run := f.runMoves(t); run.Failed != 1 || run.Moved != 0 {
		t.Fatalf("run = %+v", run)
	}
	f.wrap = nil
	if got := f.failure(t, user); got != failureVerify {
		t.Errorf("failure = %q", got)
	}
	if got := f.connectionOf(t, user, file.ID); got != a {
		t.Errorf("record moved to %s", got)
	}
	if got := f.contentOf(t, user, file.ID); got != "hello" {
		t.Errorf("content = %q", got)
	}
	if got := f.objects("b@gmail.com"); got != 0 {
		t.Errorf("b kept %d bad copies", got)
	}
}

func testTransferBadChecksum(t *testing.T, f *uploadFixture) {
	testBadCopy(t, f, lying{checksum: "bad"})
}
func testTransferBadSize(t *testing.T, f *uploadFixture) { testBadCopy(t, f, lying{size: 1}) }

// chunkFailer fails a number of chunks after reading them.
type chunkFailer struct {
	provider.Provider
	fails *atomic.Int32
}

func (l chunkFailer) PutChunk(ctx context.Context, session string, chunk provider.Chunk) (provider.ResumeState, error) {
	if l.fails.Add(-1) >= 0 {
		_, _ = io.Copy(io.Discard, chunk.Body)
		return provider.ResumeState{}, errors.New("connection reset")
	}
	return l.Provider.PutChunk(ctx, session, chunk)
}

func testTransferRetriesChunk(t *testing.T, f *uploadFixture) {
	f.service.transferChunk = align
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	body := strings.Repeat("abcdefgh", align/2)
	file := f.putIn(t, user, a, nil, "big.bin", body)
	f.queue(t, user, b, file.ID)
	var fails atomic.Int32
	fails.Store(2)
	f.only("b@gmail.com", func(p provider.Provider) provider.Provider { return chunkFailer{p, &fails} })
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("run = %+v", run)
	}
	f.wrap = nil
	if got := f.contentOf(t, user, file.ID); got != body {
		t.Errorf("content differs after a retry")
	}
}

func testTransferKeptLess(t *testing.T, f *uploadFixture) {
	f.service.transferChunk = align
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	var body strings.Builder
	for i := range align * 2 {
		body.WriteByte(byte(i % 251))
	}
	file := f.putIn(t, user, a, nil, "big.bin", body.String())
	f.queue(t, user, b, file.ID)
	f.account("b@gmail.com").DropBytes = 1000
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("run = %+v", run)
	}
	f.account("b@gmail.com").DropBytes = 0
	if got := f.contentOf(t, user, file.ID); got != body.String() {
		t.Errorf("content differs, %d bytes", len(got))
	}
}

func testTransferGivesUp(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	f.account("b@gmail.com").ChunkErr = errors.New("backend error")
	if run := f.runMoves(t); run.Failed != 1 {
		t.Fatalf("run = %+v", run)
	}
	f.account("b@gmail.com").ChunkErr = nil
	if got := f.failure(t, user); got != failureCopy {
		t.Errorf("failure = %q", got)
	}
	if got := f.contentOf(t, user, file.ID); got != "hello" {
		t.Errorf("content = %q", got)
	}
	if got := f.account("b@gmail.com").OpenSessions(); got != 0 {
		t.Errorf("%d sessions left open", got)
	}
}

func testTransferSourceMissing(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	if err := f.account("a@gmail.com").Delete(f.ctx, f.objectID(t, user, file.ID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if run := f.runMoves(t); run.Failed != 1 {
		t.Fatalf("run = %+v", run)
	}
	if got := f.failure(t, user); got != failureSourceMissing {
		t.Errorf("failure = %q", got)
	}
}

// stubborn is an account that won't delete anything.
type stubborn struct{ provider.Provider }

func (stubborn) Delete(context.Context, string) error { return errors.New("backend error") }

func testTransferRemovalRetried(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	f.only("a@gmail.com", func(p provider.Provider) provider.Provider { return stubborn{p} })
	run := f.runMoves(t)
	if run.Moved != 1 || run.Removed != 0 {
		t.Fatalf("run = %+v", run)
	}
	if len(run.Failures) != 1 {
		t.Fatalf("failures = %v, want the delete reported", run.Failures)
	}
	if got := f.objects("a@gmail.com"); got != 1 {
		t.Fatalf("a holds %d", got)
	}
	queue, err := f.service.ListTransfers(f.ctx, user)
	if err != nil {
		t.Fatalf("ListTransfers: %v", err)
	}
	if queue.Removing != 1 {
		t.Errorf("removing = %d", queue.Removing)
	}
	// It isn't tried on every run.
	if run := f.runMoves(t); len(run.Failures) != 0 {
		t.Errorf("tried again straight away: %v", run.Failures)
	}
	f.wrap = nil
	if _, err := f.pool.Exec(f.ctx, `UPDATE pending_removals SET retry_at = now()`); err != nil {
		t.Fatalf("age removal: %v", err)
	}
	if run := f.runMoves(t); run.Removed != 1 {
		t.Fatalf("run = %+v", run)
	}
	if got := f.objects("a@gmail.com"); got != 0 {
		t.Errorf("a still holds %d", got)
	}
}

func testTransferSyncLeavesOldCopy(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	f.only("a@gmail.com", func(p provider.Provider) provider.Provider { return stubborn{p} })
	f.runMoves(t)
	f.runMoves(t)
	f.wrap = nil
	f.ageAll(t, "a@gmail.com")
	if result := f.sync(t, user, a); result.Added != 0 {
		t.Errorf("result = %+v, the old copy came back as a new file", result)
	}
	if got := f.files(t, user); got != 1 {
		t.Errorf("%d files", got)
	}
}

// listHook runs something once, in the gap between a sync reading its records and
// listing the account.
type listHook struct {
	provider.Provider
	once *sync.Once
	run  func()
}

func (h listHook) List(ctx context.Context, token string) (provider.ObjectPage, error) {
	h.once.Do(h.run)
	return h.Provider.List(ctx, token)
}

func testTransferDuringSync(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	var once sync.Once
	f.only("a@gmail.com", func(p provider.Provider) provider.Provider {
		return listHook{p, &once, func() {
			f.runMoves(t)
			f.runMoves(t)
		}}
	})
	f.sync(t, user, a)
	f.wrap = nil
	if got := f.files(t, user); got != 1 {
		t.Fatalf("%d files: the sync took the moved file for one that was deleted", got)
	}
	if got := f.contentOf(t, user, file.ID); got != "hello" {
		t.Errorf("content = %q", got)
	}
}

// chunkHook runs something once, while a chunk is being sent.
type chunkHook struct {
	provider.Provider
	once  *sync.Once
	run   func()
	calls *atomic.Int32
}

func (h chunkHook) PutChunk(ctx context.Context, session string, chunk provider.Chunk) (provider.ResumeState, error) {
	h.calls.Add(1)
	h.once.Do(h.run)
	return h.Provider.PutChunk(ctx, session, chunk)
}

func (f *uploadFixture) duringCopy(email string, run func()) *atomic.Int32 {
	var once sync.Once
	var calls atomic.Int32
	f.only(email, func(p provider.Provider) provider.Provider { return chunkHook{p, &once, run, &calls} })
	return &calls
}

func testTransferFileDeleted(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	f.duringCopy("b@gmail.com", func() {
		if err := f.service.TrashFile(f.ctx, user, file.ID, ""); err != nil {
			t.Errorf("TrashFile: %v", err)
		}
		if err := f.service.DeleteFile(f.ctx, user, file.ID, ""); err != nil {
			t.Errorf("DeleteFile: %v", err)
		}
	})
	if run := f.runMoves(t); run.Moved != 0 {
		t.Fatalf("run = %+v", run)
	}
	if got := f.objects("b@gmail.com"); got != 0 {
		t.Errorf("b kept %d orphans", got)
	}
	if got := f.count(t, `SELECT count(*) FROM pending_removals`); got != 0 {
		t.Errorf("%d removals for nothing", got)
	}
}

func testTransferCancelled(t *testing.T, f *uploadFixture) {
	f.service.transferChunk = align
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "big.bin", strings.Repeat("x", align*3))
	f.queue(t, user, b, file.ID)
	queue, err := f.service.ListTransfers(f.ctx, user)
	if err != nil {
		t.Fatalf("ListTransfers: %v", err)
	}
	chunks := f.duringCopy("b@gmail.com", func() {
		if err := f.service.CancelTransfer(f.ctx, user, queue.Transfers[0].ID); err != nil {
			t.Errorf("CancelTransfer: %v", err)
		}
	})
	if run := f.runMoves(t); run.Moved != 0 {
		t.Fatalf("run = %+v", run)
	}
	f.wrap = nil
	if got := chunks.Load(); got != 1 {
		t.Errorf("%d chunks sent, the copy should stop at the next one", got)
	}
	if got := f.connectionOf(t, user, file.ID); got != a {
		t.Errorf("connection = %s", got)
	}
	if got := f.objects("b@gmail.com"); got != 0 {
		t.Errorf("b kept %d", got)
	}
	if got := f.account("b@gmail.com").OpenSessions(); got != 0 {
		t.Errorf("%d sessions left open", got)
	}
}

func testTransferRenamed(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	f.duringCopy("b@gmail.com", func() {
		if _, err := f.service.RenameFile(f.ctx, user, file.ID, "new.txt"); err != nil {
			t.Errorf("RenameFile: %v", err)
		}
	})
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("run = %+v", run)
	}
	f.wrap = nil
	got, err := f.service.Get(f.ctx, user, file.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Name != "new.txt" || got.ConnectionID != b {
		t.Errorf("file = %+v", got)
	}
}

func testTransferShutdown(t *testing.T, f *uploadFixture) {
	f.service.transferChunk = align
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	body := strings.Repeat("x", align*3)
	file := f.putIn(t, user, a, nil, "big.bin", body)
	f.queue(t, user, b, file.ID)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	var fails atomic.Int32
	fails.Store(1)
	f.only("b@gmail.com", func(p provider.Provider) provider.Provider {
		return shutdownHook{chunkFailer{p, &fails}, cancel}
	})
	if _, err := f.service.RunTransfers(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancelled", err)
	}
	f.wrap = nil
	if got := f.failure(t, user); got != "" {
		t.Errorf("failure = %q, a shutdown isn't a failure", got)
	}
	if got := f.account("b@gmail.com").OpenSessions(); got != 0 {
		t.Errorf("%d sessions left open", got)
	}
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("after restart: run = %+v", run)
	}
	if got := f.contentOf(t, user, file.ID); got != body {
		t.Errorf("content differs")
	}
}

// shutdownHook stops the server during a chunk.
type shutdownHook struct {
	chunkFailer
	cancel context.CancelFunc
}

func (h shutdownHook) PutChunk(ctx context.Context, session string, chunk provider.Chunk) (provider.ResumeState, error) {
	h.cancel()
	return h.chunkFailer.PutChunk(ctx, session, chunk)
}

// describeHook stops the server while an account is being asked about a file, which
// comes back as an ordinary account failure.
type describeHook struct {
	provider.Provider
	cancel context.CancelFunc
}

func (h describeHook) Describe(ctx context.Context, _ string) (provider.Object, error) {
	h.cancel()
	return provider.Object{}, ctx.Err()
}

func testTransferShutdownEarly(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	ctx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	f.only("a@gmail.com", func(p provider.Provider) provider.Provider { return describeHook{p, cancel} })
	if _, err := f.service.RunTransfers(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancelled", err)
	}
	f.wrap = nil
	if got := f.failure(t, user); got != "" {
		t.Errorf("failure = %q, a shutdown isn't a failure", got)
	}
}

func testTransferFileChanged(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	object := f.objectID(t, user, file.ID)
	other, err := f.account("a@gmail.com").Put(f.ctx, provider.Upload{Name: "other.txt", Body: strings.NewReader("other")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	f.duringCopy("b@gmail.com", func() {
		if _, err := f.pool.Exec(f.ctx, `UPDATE files SET object_id = $1 WHERE id = $2`, other.ID, file.ID); err != nil {
			t.Errorf("change file: %v", err)
		}
	})
	if run := f.runMoves(t); run.Moved != 0 {
		t.Fatalf("run = %+v", run)
	}
	f.wrap = nil
	if got := f.count(t, `SELECT count(*) FROM pending_removals`); got != 0 {
		t.Errorf("%d removals, for a copy that was never replaced", got)
	}
	if exists, err := f.account("a@gmail.com").Exists(f.ctx, object); err != nil || !exists {
		t.Errorf("the old copy is gone: exists %v, error %v", exists, err)
	}
	if got := f.objects("b@gmail.com"); got != 0 {
		t.Errorf("b kept %d", got)
	}
}

func testRemovalGuard(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	_, err := f.pool.Exec(f.ctx, `INSERT INTO pending_removals (user_id, connection_id, object_id) VALUES ($1, $2, $3)`,
		user, a, f.objectID(t, user, file.ID))
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	f.runMoves(t)
	if got := f.contentOf(t, user, file.ID); got != "hello" {
		t.Errorf("content = %q", got)
	}
	if got := f.count(t, `SELECT count(*) FROM pending_removals`); got != 0 {
		t.Errorf("%d removals left", got)
	}
}

func testTransferHoldsSpace(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 8)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	pool, err := f.storage.Pool(f.ctx, user)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	for _, account := range pool.Accounts {
		if account.ID == b && account.Reserved != 5 {
			t.Errorf("reserved = %d", account.Reserved)
		}
	}
	_, err = f.service.BeginUpload(f.ctx, user, BeginUploadInput{ConnectionID: &b, Name: "x", Size: 5})
	if !errors.Is(err, storage.ErrStorageFull) {
		t.Errorf("error = %v, want ErrStorageFull", err)
	}
}

func testTransferAskedTwice(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 8)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	if got := f.queue(t, user, b, file.ID); got.Queued != 0 || got.Skipped != 1 {
		t.Errorf("result = %+v", got)
	}
}

func testTransferNoRoom(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 8)
	one := f.putIn(t, user, a, nil, "one.txt", "hello")
	two := f.putIn(t, user, a, nil, "two.txt", "world")
	_, err := f.service.TransferFiles(f.ctx, user, []uuid.UUID{one.ID, two.ID}, b, "")
	if !errors.Is(err, storage.ErrStorageFull) {
		t.Errorf("error = %v, want ErrStorageFull", err)
	}
	if got := f.count(t, `SELECT count(*) FROM file_transfers`); got != 0 {
		t.Errorf("%d queued", got)
	}
}

func testFailedTransferHoldsNothing(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 8)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	f.account("b@gmail.com").ChunkErr = errors.New("backend error")
	f.runMoves(t)
	f.account("b@gmail.com").ChunkErr = nil
	if _, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{ConnectionID: &b, Name: "x", Size: 5}); err != nil {
		t.Errorf("BeginUpload: %v", err)
	}
}

func testTransferRetry(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	f.account("b@gmail.com").ChunkErr = errors.New("backend error")
	f.runMoves(t)
	f.account("b@gmail.com").ChunkErr = nil
	if run := f.runMoves(t); run.Moved != 0 {
		t.Fatalf("a failed move ran by itself: %+v", run)
	}
	if err := f.service.RetryTransfers(f.ctx, user); err != nil {
		t.Fatalf("RetryTransfers: %v", err)
	}
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("run = %+v", run)
	}
}

func testTransferUnlinkTarget(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	file := f.putIn(t, user, a, nil, "a.txt", "hello")
	f.queue(t, user, b, file.ID)
	if err := f.storage.Delete(f.ctx, user, b, ""); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.count(t, `SELECT count(*) FROM file_transfers`); got != 0 {
		t.Errorf("%d moves left", got)
	}
	if got := f.contentOf(t, user, file.ID); got != "hello" {
		t.Errorf("content = %q", got)
	}
}

func testEmptyConnection(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 1<<30)
	folder := f.folder(t, user, nil, "Trip")
	one := f.putIn(t, user, a, nil, "one.txt", "1")
	two := f.putIn(t, user, a, &folder.ID, "two.txt", "22")
	added, _ := f.addedFile(t, user, a)
	result, err := f.service.EmptyConnection(f.ctx, user, a, "203.0.113.7")
	if err != nil {
		t.Fatalf("EmptyConnection: %v", err)
	}
	if result.Queued != 2 || result.Skipped != 1 {
		t.Errorf("result = %+v", result)
	}
	if run := f.runMoves(t); run.Moved != 2 {
		t.Fatalf("run = %+v", run)
	}
	for _, id := range []uuid.UUID{one.ID, two.ID} {
		if got := f.connectionOf(t, user, id); got != b {
			t.Errorf("%s is in %s", id, got)
		}
	}
	if got := f.connectionOf(t, user, added); got != a {
		t.Errorf("the added file moved to %s", got)
	}
	queue, err := f.service.ListTransfers(f.ctx, user)
	if err != nil {
		t.Fatalf("ListTransfers: %v", err)
	}
	for _, account := range queue.Accounts {
		if account.ConnectionID == a && (account.Files != 0 || account.External != 1) {
			t.Errorf("a holds %+v", account)
		}
	}
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'storage.emptied'`); got != 1 {
		t.Errorf("%d audit events", got)
	}
}

func testEmptySpreads(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 6)
	c := f.link(t, user, "c@gmail.com", 7)
	big := f.putIn(t, user, a, nil, "big.txt", "123456")
	small := f.putIn(t, user, a, nil, "small.txt", "1234")
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); err != nil {
		t.Fatalf("EmptyConnection: %v", err)
	}
	if run := f.runMoves(t); run.Moved != 2 {
		t.Fatalf("run = %+v", run)
	}
	// Neither account holds both, so the largest had to go first.
	if f.connectionOf(t, user, big.ID) == f.connectionOf(t, user, small.ID) {
		t.Errorf("both went to one account")
	}
	if f.connectionOf(t, user, big.ID) != c {
		t.Errorf("the largest went to the smaller room")
	}
	if f.connectionOf(t, user, small.ID) != b {
		t.Errorf("the smaller file has no room there")
	}
}

func testEmptyNoRoom(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 6)
	f.link(t, user, "c@gmail.com", 6)
	// There's room in total, but a file is stored whole.
	f.putIn(t, user, a, nil, "big.txt", "12345678")
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); !errors.Is(err, storage.ErrStorageFull) {
		t.Fatalf("error = %v, want ErrStorageFull", err)
	}
	if got := f.count(t, `SELECT count(*) FROM file_transfers`); got != 0 {
		t.Errorf("%d queued", got)
	}
	if got := f.count(t, `SELECT count(*) FROM storage_connections WHERE draining`); got != 0 {
		t.Errorf("%d accounts left draining", got)
	}
}

func testEmptyOnlyAccount(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	f.putIn(t, user, a, nil, "a.txt", "x")
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); !errors.Is(err, storage.ErrNoOtherAccount) {
		t.Errorf("error = %v, want ErrNoOtherAccount", err)
	}
}

func testEmptyUnreachable(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	f.putIn(t, user, a, nil, "a.txt", "x")
	f.account("a@gmail.com").CheckErr = errors.New("token expired")
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); !errors.Is(err, ErrProviderFailed) {
		t.Errorf("error = %v, want ErrProviderFailed", err)
	}
}

func testEmptyKeepsFilesOut(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	b := f.link(t, user, "b@gmail.com", 100)
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); err != nil {
		t.Fatalf("EmptyConnection: %v", err)
	}
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "x", Size: 5})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	if session.ConnectionID != b {
		t.Errorf("upload went to the account being emptied")
	}
	_, err = f.service.BeginUpload(f.ctx, user, BeginUploadInput{ConnectionID: &a, Name: "x", Size: 5})
	if !errors.Is(err, storage.ErrAccountDraining) {
		t.Errorf("error = %v, want ErrAccountDraining", err)
	}
	pool, err := f.storage.Pool(f.ctx, user)
	if err != nil {
		t.Fatalf("Pool: %v", err)
	}
	if pool.Free > 100 {
		t.Errorf("free = %d, counting room nothing can use", pool.Free)
	}
	connections, err := f.storage.List(f.ctx, user)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(connections) != 2 || !connections[0].Draining || connections[1].Draining {
		t.Errorf("connections = %+v", connections)
	}
}

func testStopEmptying(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 100)
	file := f.putIn(t, user, a, nil, "a.txt", "x")
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); err != nil {
		t.Fatalf("EmptyConnection: %v", err)
	}
	if err := f.service.StopEmptying(f.ctx, user, a); err != nil {
		t.Fatalf("StopEmptying: %v", err)
	}
	if run := f.runMoves(t); run.Moved != 0 {
		t.Errorf("run = %+v", run)
	}
	if got := f.connectionOf(t, user, file.ID); got != a {
		t.Errorf("connection = %s", got)
	}
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "x", Size: 5})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	if session.ConnectionID != a {
		t.Errorf("the account didn't come back into use")
	}
}

func testEmptyAgain(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	f.putIn(t, user, a, nil, "a.txt", "x")
	if _, err := f.service.EmptyConnection(f.ctx, user, a, ""); err != nil {
		t.Fatalf("EmptyConnection: %v", err)
	}
	f.account("b@gmail.com").ChunkErr = errors.New("backend error")
	f.runMoves(t)
	f.account("b@gmail.com").ChunkErr = nil
	result, err := f.service.EmptyConnection(f.ctx, user, a, "")
	if err != nil {
		t.Fatalf("EmptyConnection again: %v", err)
	}
	if result.Queued != 1 {
		t.Errorf("result = %+v", result)
	}
	if run := f.runMoves(t); run.Moved != 1 {
		t.Errorf("run = %+v", run)
	}
}

// bottomless is an account pair for streaming a huge file through: reading gives
// zeros, and writing keeps nothing.
type bottomless struct {
	provider.Provider
	size int64
}

func (b bottomless) Describe(ctx context.Context, id string) (provider.Object, error) {
	object, err := b.Provider.Describe(ctx, id)
	object.Size, object.Checksum = b.size, ""
	return object, err
}

func (b bottomless) Get(context.Context, string, *provider.ByteRange) (io.ReadCloser, error) {
	return io.NopCloser(io.LimitReader(zeros{}, b.size)), nil
}

func (b bottomless) PutChunk(ctx context.Context, session string, chunk provider.Chunk) (provider.ResumeState, error) {
	if _, err := io.Copy(io.Discard, chunk.Body); err != nil {
		return provider.ResumeState{}, err
	}
	received := chunk.Offset + chunk.Length
	if received < chunk.Total {
		return provider.ResumeState{Received: received}, nil
	}
	object, err := b.Put(ctx, provider.Upload{Name: "big", Body: bytes.NewReader(nil)})
	return provider.ResumeState{Received: received, Object: &object}, err
}

func testTransferMemoryStaysFlat(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	a := f.link(t, user, "a@gmail.com", 1<<40)
	b := f.link(t, user, "b@gmail.com", 1<<40)
	file := f.putIn(t, user, a, nil, "big.bin", "x")
	f.queue(t, user, b, file.ID)
	const size = 512 << 20
	const limit = 32 << 20
	f.wrap = func(p provider.Provider) provider.Provider { return bottomless{p, size} }

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	var peak atomic.Uint64
	done, sampled := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
				var now runtime.MemStats
				runtime.ReadMemStats(&now)
				peak.Store(max(peak.Load(), now.HeapAlloc))
			}
		}
	}()
	run, err := f.service.RunTransfers(f.ctx)
	close(done)
	<-sampled
	if err != nil || run.Moved != 1 {
		t.Fatalf("run = %+v, error %v", run, err)
	}
	if grew := peak.Load() - min(peak.Load(), before.HeapAlloc); grew > limit {
		t.Errorf("heap grew by %d MiB for a 512 MiB move", grew>>20)
	}
}

func testTransferIsolation(t *testing.T, f *uploadFixture) {
	owner := f.user(t)
	a := f.link(t, owner, "a@gmail.com", 1<<30)
	b := f.link(t, owner, "b@gmail.com", 1<<30)
	file := f.putIn(t, owner, a, nil, "a.txt", "x")
	f.queue(t, owner, b, file.ID)
	queue, err := f.service.ListTransfers(f.ctx, owner)
	if err != nil {
		t.Fatalf("ListTransfers: %v", err)
	}

	stranger := f.user(t)
	own := f.link(t, stranger, "s@gmail.com", 1<<30)
	mine := f.putIn(t, stranger, own, nil, "mine.txt", "y")
	if _, err := f.service.TransferFiles(f.ctx, stranger, []uuid.UUID{file.ID}, own, ""); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("their file: error = %v", err)
	}
	if _, err := f.service.TransferFiles(f.ctx, stranger, []uuid.UUID{mine.ID}, b, ""); !errors.Is(err, storage.ErrConnectionNotFound) {
		t.Errorf("their account: error = %v", err)
	}
	if _, err := f.service.EmptyConnection(f.ctx, stranger, a, ""); !errors.Is(err, storage.ErrConnectionNotFound) {
		t.Errorf("emptying theirs: error = %v", err)
	}
	if err := f.service.StopEmptying(f.ctx, stranger, a); !errors.Is(err, storage.ErrConnectionNotFound) {
		t.Errorf("stopping theirs: error = %v", err)
	}
	if err := f.service.CancelTransfer(f.ctx, stranger, queue.Transfers[0].ID); !errors.Is(err, ErrTransferNotFound) {
		t.Errorf("cancelling theirs: error = %v", err)
	}
	seen, err := f.service.ListTransfers(f.ctx, stranger)
	if err != nil {
		t.Fatalf("ListTransfers: %v", err)
	}
	if len(seen.Transfers) != 0 || seen.Waiting != 0 {
		t.Errorf("a stranger sees %+v", seen)
	}
	for _, account := range seen.Accounts {
		if account.ConnectionID != own {
			t.Errorf("a stranger sees account %s", account.ConnectionID)
		}
	}
}

func TestTransferHandlers(t *testing.T) {
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
	b := f.link(t, owner, "b@gmail.com", 1<<30)
	file := f.putIn(t, owner, a, nil, "a.txt", "hello")
	call := func(user uuid.UUID, method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Origin", testOrigin)
		req.Header.Set("Content-Type", "application/json")
		req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: user.String()})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	move := `{"file_ids":["` + file.ID.String() + `"],"target_connection_id":"` + b.String() + `"}`

	if rec := call(f.user(t), http.MethodPost, "/api/v1/transfers", move); rec.Code != http.StatusNotFound {
		t.Errorf("a stranger: status = %d", rec.Code)
	}
	if rec := call(owner, http.MethodPost, "/api/v1/transfers", `{"file_ids":[],"target_connection_id":"`+b.String()+`"}`); rec.Code != http.StatusBadRequest {
		t.Errorf("nothing: status = %d", rec.Code)
	}
	rec := call(owner, http.MethodPost, "/api/v1/transfers", move)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"queued":1`) {
		t.Fatalf("move: status = %d, body %s", rec.Code, rec.Body)
	}
	f.account("b@gmail.com").ChunkErr = errors.New("secret detail from google")
	f.runMoves(t)
	f.account("b@gmail.com").ChunkErr = nil
	rec = call(owner, http.MethodGet, "/api/v1/transfers", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "stopped partway") {
		t.Errorf("list: status = %d, body %s", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "secret detail") {
		t.Errorf("list leaks the cause: %s", rec.Body)
	}
	if rec := call(owner, http.MethodPost, "/api/v1/transfers/retry", ""); rec.Code != http.StatusNoContent {
		t.Errorf("retry: status = %d", rec.Code)
	}
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("run = %+v", run)
	}
	if rec := call(owner, http.MethodDelete, "/api/v1/transfers/"+uuid.NewString(), ""); rec.Code != http.StatusNotFound {
		t.Errorf("cancel nothing: status = %d", rec.Code)
	}

	if rec := call(f.user(t), http.MethodPost, "/api/v1/storage/connections/"+b.String()+"/empty", ""); rec.Code != http.StatusNotFound {
		t.Errorf("empty as a stranger: status = %d", rec.Code)
	}
	if rec := call(owner, http.MethodPost, "/api/v1/storage/connections/"+b.String()+"/empty", ""); rec.Code != http.StatusOK {
		t.Errorf("empty: status = %d, body %s", rec.Code, rec.Body)
	}
	if rec := call(owner, http.MethodDelete, "/api/v1/storage/connections/"+b.String()+"/empty", ""); rec.Code != http.StatusNoContent {
		t.Errorf("stop: status = %d", rec.Code)
	}
}
