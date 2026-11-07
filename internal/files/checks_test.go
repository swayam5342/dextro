package files

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
	"github.com/swayam5342/tributary/internal/provider"
)

func (f *uploadFixture) problems(t *testing.T, userID uuid.UUID) []FileProblem {
	t.Helper()
	checks, err := f.service.FileChecks(f.ctx, userID)
	if err != nil {
		t.Fatalf("FileChecks: %v", err)
	}
	return checks.Problems
}

func wantProblem(t *testing.T, got []FileProblem, kind ProblemKind, name string) FileProblem {
	t.Helper()
	if len(got) != 1 || got[0].Kind != kind || got[0].Name != name {
		t.Fatalf("problems = %+v, want one %s for %s", got, kind, name)
	}
	return got[0]
}

func TestFileChecks(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"an upload remembers its checksum", testUploadKeepsChecksum},
		{"a chunked upload remembers its checksum", testChunkedUploadKeepsChecksum},
		{"a sync fills in checksums for older files", testSyncFillsChecksums},
		{"a file gone from its account is reported", testSyncReportsMissing},
		{"a trashed file gone from its account isn't reported", testSyncIgnoresTrashedMissing},
		{"a file changed in its account is reported once", testSyncReportsChanged},
		{"dismissing a change accepts it", testDismissAcceptsChange},
		{"added files can change without a report", testExternalChangeIsQuiet},
		{"a check reads every file and passes good ones", testCheckPasses},
		{"a check reports bytes that don't match", testCheckReportsDamage},
		{"a passing check clears an old report", testCheckClearsDamage},
		{"an unreachable account isn't damage", testCheckSkipsUnreachable},
		{"a check takes the longest unchecked file first", testCheckRotates},
		{"turning checks off stops them reading anything", testChecksCanBeOff},
		{"checks wait for their schedule", testChecksFollowSchedule},
		{"a restart doesn't start a check", testChecksSurviveRestart},
		{"a run stops at its limit", testCheckStopsAtLimit},
		{"a file over the limit is left out", testCheckSkipsOversized},
		{"a sync still reports problems with checks off", testSyncReportsWithChecksOff},
		{"problems are private to their owner", testProblemsAreScoped},
		{"deleting a file takes its problems with it", testDeleteClearsProblems},
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

func (f *uploadFixture) record(t *testing.T, userID, fileID uuid.UUID) fileRecord {
	t.Helper()
	record, err := f.service.store.FileForOwner(f.ctx, userID, fileID)
	if err != nil {
		t.Fatalf("FileForOwner: %v", err)
	}
	return record
}

func testUploadKeepsChecksum(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "a.txt")
	record := f.record(t, user, file.ID)
	if record.checksum == "" || record.contentHash != record.checksum {
		t.Errorf("checksum %q, content hash %q", record.checksum, record.contentHash)
	}
}

func testChunkedUploadKeepsChecksum(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a.txt", Size: 3})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	done, err := f.service.PutUploadChunk(f.ctx, user, session.ID, 0, 3, strings.NewReader("abc"))
	if err != nil {
		t.Fatalf("PutUploadChunk: %v", err)
	}
	if record := f.record(t, user, done.File.ID); record.checksum == "" {
		t.Error("no checksum")
	}
}

func testSyncFillsChecksums(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "a.txt")
	want := f.record(t, user, file.ID).checksum
	if _, err := f.pool.Exec(f.ctx, `UPDATE files SET checksum = NULL, content_hash = NULL`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	f.sync(t, user, connection)
	if record := f.record(t, user, file.ID); record.checksum != want || record.contentHash != want {
		t.Errorf("checksum %q, content hash %q, want %q", record.checksum, record.contentHash, want)
	}
	if got := f.problems(t, user); len(got) != 0 {
		t.Errorf("problems = %+v", got)
	}
}

func testSyncReportsMissing(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "gone.txt")
	if err := f.account("a@gmail.com").Delete(f.ctx, f.objectID(t, user, file.ID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	f.sync(t, user, connection)
	problem := wantProblem(t, f.problems(t, user), ProblemMissing, "gone.txt")
	if problem.FileID != nil || problem.Size != 5 || problem.ConnectionID != connection {
		t.Errorf("problem = %+v", problem)
	}
	if f.files(t, user) != 0 {
		t.Error("the record stayed")
	}
}

func testSyncIgnoresTrashedMissing(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "trashed.txt")
	object := f.objectID(t, user, file.ID)
	if err := f.service.TrashFile(f.ctx, user, file.ID, "203.0.113.7"); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.account("a@gmail.com").Delete(f.ctx, object); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	f.sync(t, user, connection)
	if got := f.problems(t, user); len(got) != 0 {
		t.Errorf("problems = %+v", got)
	}
}

func testSyncReportsChanged(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "edited.txt")
	f.account("a@gmail.com").Replace(f.objectID(t, user, file.ID), []byte("edited"))
	f.sync(t, user, connection)
	f.sync(t, user, connection)
	problem := wantProblem(t, f.problems(t, user), ProblemChanged, "edited.txt")
	if problem.FileID == nil || *problem.FileID != file.ID {
		t.Errorf("problem = %+v", problem)
	}
}

func testDismissAcceptsChange(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "edited.txt")
	f.account("a@gmail.com").Replace(f.objectID(t, user, file.ID), []byte("edited"))
	f.sync(t, user, connection)
	problem := wantProblem(t, f.problems(t, user), ProblemChanged, "edited.txt")
	if err := f.service.DismissProblem(f.ctx, user, problem.ID); err != nil {
		t.Fatalf("DismissProblem: %v", err)
	}
	f.sync(t, user, connection)
	if got := f.problems(t, user); len(got) != 0 {
		t.Errorf("it came back: %+v", got)
	}
}

func testExternalChangeIsQuiet(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "mine.txt")
	if _, err := f.pool.Exec(f.ctx, `UPDATE files SET external = true`); err != nil {
		t.Fatalf("mark external: %v", err)
	}
	before := f.record(t, user, file.ID).checksum
	f.account("a@gmail.com").Replace(f.objectID(t, user, file.ID), []byte("edited"))
	f.sync(t, user, connection)
	if got := f.problems(t, user); len(got) != 0 {
		t.Errorf("problems = %+v", got)
	}
	if f.record(t, user, file.ID).checksum == before {
		t.Error("the checksum didn't follow the file")
	}
}

func (f *uploadFixture) check(t *testing.T) CheckRun {
	t.Helper()
	run, err := f.service.CheckFiles(f.ctx, true)
	if err != nil {
		t.Fatalf("CheckFiles: %v", err)
	}
	return run
}

// tick is the job's hourly look at whether a check is due.
func (f *uploadFixture) tick(t *testing.T) CheckRun {
	t.Helper()
	run, err := f.service.CheckFiles(f.ctx, false)
	if err != nil {
		t.Fatalf("CheckFiles: %v", err)
	}
	return run
}

func (f *uploadFixture) setting(t *testing.T, key, value string) {
	t.Helper()
	if err := f.service.store.Settings().SetSetting(f.ctx, key, []byte(value)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
}

func (f *uploadFixture) lastCheckWas(t *testing.T, ago time.Duration) {
	t.Helper()
	stamp, err := json.Marshal(time.Now().Add(-ago))
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	f.setting(t, "file_check_last_run", string(stamp))
}

func (f *uploadFixture) sized(t *testing.T, userID uuid.UUID, name string, size int) File {
	t.Helper()
	file, err := f.service.Upload(f.ctx, userID, UploadInput{Name: name, Size: int64(size), Body: bytes.NewReader(make([]byte, size))})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	return file
}

func testCheckPasses(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.recorded(t, user, "a@gmail.com", "a.txt")
	f.recorded(t, user, "a@gmail.com", "b.txt")
	if run := f.check(t); run.Checked != 2 || run.Damaged != 0 {
		t.Errorf("run = %+v", run)
	}
	checks, err := f.service.FileChecks(f.ctx, user)
	if err != nil {
		t.Fatalf("FileChecks: %v", err)
	}
	if checks.Checkable != 2 || checks.Checked != 2 || len(checks.Problems) != 0 {
		t.Errorf("checks = %+v", checks)
	}
}

func testCheckReportsDamage(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "bad.txt")
	f.account("a@gmail.com").Damage(f.objectID(t, user, file.ID))
	if run := f.check(t); run.Damaged != 1 {
		t.Errorf("run = %+v", run)
	}
	wantProblem(t, f.problems(t, user), ProblemDamaged, "bad.txt")
}

func testCheckClearsDamage(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "bad.txt")
	object := f.objectID(t, user, file.ID)
	before := f.record(t, user, file.ID).checksum
	f.account("a@gmail.com").Damage(object)
	f.check(t)
	wantProblem(t, f.problems(t, user), ProblemDamaged, "bad.txt")
	f.account("a@gmail.com").Replace(object, []byte("hello"))
	if got := f.record(t, user, file.ID).checksum; got != before {
		t.Fatalf("checksum changed to %q", got)
	}
	f.check(t)
	if got := f.problems(t, user); len(got) != 0 {
		t.Errorf("problems = %+v", got)
	}
}

func testCheckSkipsUnreachable(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.recorded(t, user, "a@gmail.com", "a.txt")
	f.wrap = failingGets
	run := f.check(t)
	if run.Checked != 0 || run.Damaged != 0 || len(run.Failures) != 1 {
		t.Errorf("run = %+v", run)
	}
	if got := f.problems(t, user); len(got) != 0 {
		t.Errorf("problems = %+v", got)
	}
}

func testCheckRotates(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	old := f.recorded(t, user, "a@gmail.com", "old.txt")
	recent := f.recorded(t, user, "a@gmail.com", "recent.txt")
	if _, err := f.pool.Exec(f.ctx, `UPDATE files SET checked_at = now() WHERE id = $1`, recent.ID); err != nil {
		t.Fatalf("stamp: %v", err)
	}
	records, err := f.service.store.FilesToCheck(f.ctx, 1<<30, 1)
	if err != nil {
		t.Fatalf("FilesToCheck: %v", err)
	}
	if len(records) != 1 || records[0].ID != old.ID {
		t.Errorf("records = %+v", records)
	}
}

func (f *uploadFixture) setChecks(t *testing.T, on string) {
	t.Helper()
	if err := f.service.store.Settings().SetSetting(f.ctx, "file_checks_enabled", []byte(on)); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
}

func testChecksCanBeOff(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "bad.txt")
	f.account("a@gmail.com").Damage(f.objectID(t, user, file.ID))

	checks, err := f.service.FileChecks(f.ctx, user)
	if err != nil {
		t.Fatalf("FileChecks: %v", err)
	}
	if !checks.Enabled {
		t.Error("checks should start on")
	}

	f.setChecks(t, "false")
	f.wrap = countingGets(&f.reads)
	if run := f.check(t); run.Checked != 0 || run.Damaged != 0 {
		t.Errorf("run = %+v", run)
	}
	if got := f.reads.Load(); got != 0 {
		t.Errorf("read %d files while off", got)
	}
	if got := f.count(t, `SELECT count(*) FROM files WHERE checked_at IS NOT NULL`); got != 0 {
		t.Errorf("%d files were marked as checked", got)
	}
	if checks, err = f.service.FileChecks(f.ctx, user); err != nil {
		t.Fatalf("FileChecks: %v", err)
	}
	if checks.Enabled {
		t.Error("checks say they're on")
	}

	f.setChecks(t, "true")
	if run := f.check(t); run.Damaged != 1 {
		t.Errorf("turned back on: run = %+v", run)
	}
}

func testChecksFollowSchedule(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.recorded(t, user, "a@gmail.com", "a.txt")

	if run := f.tick(t); run.Checked != 1 {
		t.Fatalf("the first run should go ahead: %+v", run)
	}
	if run := f.tick(t); run.Checked != 0 {
		t.Errorf("ran again straight away: %+v", run)
	}
	f.lastCheckWas(t, 5*time.Hour)
	if run := f.tick(t); run.Checked != 0 {
		t.Errorf("ran after 5 hours of 6: %+v", run)
	}
	f.lastCheckWas(t, 6*time.Hour)
	if run := f.tick(t); run.Checked != 1 {
		t.Errorf("didn't run after 6 hours: %+v", run)
	}
	// Someone can choose their own schedule.
	f.setting(t, "file_check_every_hours", "1")
	f.lastCheckWas(t, time.Hour)
	if run := f.tick(t); run.Checked != 1 {
		t.Errorf("didn't follow a shorter schedule: %+v", run)
	}
	// Asking for one doesn't wait.
	if run := f.check(t); run.Checked != 1 {
		t.Errorf("a run on demand waited: %+v", run)
	}
}

// The job looks in at startup too, so the schedule has to outlive the server.
func testChecksSurviveRestart(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.recorded(t, user, "a@gmail.com", "a.txt")
	f.tick(t)
	restarted := newUploadFixture(t, f.pool)
	restarted.accounts = f.accounts
	if run := restarted.tick(t); run.Checked != 0 {
		t.Errorf("a new server ran it again: %+v", run)
	}
}

func testCheckStopsAtLimit(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	for _, name := range []string{"a", "b", "c"} {
		f.sized(t, user, name, 400<<10)
	}
	f.setting(t, "file_check_limit_mb", "1")
	if run := f.check(t); run.Checked != 2 {
		t.Errorf("run = %+v, want 2 of 3 inside 1 MB", run)
	}
	if run := f.check(t); run.Checked != 2 {
		t.Errorf("the next run should take the one left, and start again: %+v", run)
	}
	if got := f.count(t, `SELECT count(*) FROM files WHERE checked_at IS NULL`); got != 0 {
		t.Errorf("%d files never reached", got)
	}
}

func testCheckSkipsOversized(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.sized(t, user, "huge", 2<<20)
	f.sized(t, user, "small", 100)
	f.setting(t, "file_check_limit_mb", "1")
	f.reads.Store(0)
	f.wrap = countingGets(&f.reads)
	if run := f.check(t); run.Checked != 1 {
		t.Errorf("run = %+v", run)
	}
	if got := f.reads.Load(); got != 1 {
		t.Errorf("read %d files, want only the small one", got)
	}
	checks, err := f.service.FileChecks(f.ctx, user)
	if err != nil {
		t.Fatalf("FileChecks: %v", err)
	}
	if checks.Checkable != 1 || checks.Checked != 1 || checks.LimitBytes != 1<<20 || checks.EveryHours != 6 {
		t.Errorf("checks = %+v", checks)
	}
}

func testSyncReportsWithChecksOff(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "gone.txt")
	f.setChecks(t, "false")
	if err := f.account("a@gmail.com").Delete(f.ctx, f.objectID(t, user, file.ID)); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	f.sync(t, user, connection)
	wantProblem(t, f.problems(t, user), ProblemMissing, "gone.txt")
}

func testProblemsAreScoped(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	stranger := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "edited.txt")
	f.account("a@gmail.com").Replace(f.objectID(t, user, file.ID), []byte("edited"))
	f.sync(t, user, connection)
	problem := wantProblem(t, f.problems(t, user), ProblemChanged, "edited.txt")
	if got := f.problems(t, stranger); len(got) != 0 {
		t.Errorf("a stranger sees %+v", got)
	}
	if err := f.service.DismissProblem(f.ctx, stranger, problem.ID); !errors.Is(err, ErrProblemNotFound) {
		t.Errorf("err = %v", err)
	}
	wantProblem(t, f.problems(t, user), ProblemChanged, "edited.txt")
}

func testDeleteClearsProblems(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	file := f.recorded(t, user, "a@gmail.com", "edited.txt")
	f.account("a@gmail.com").Replace(f.objectID(t, user, file.ID), []byte("edited"))
	f.sync(t, user, connection)
	if err := f.service.TrashFile(f.ctx, user, file.ID, ""); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.DeleteFile(f.ctx, user, file.ID, ""); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	if got := f.problems(t, user); len(got) != 0 {
		t.Errorf("problems = %+v", got)
	}
}

func TestDuplicates(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE users, settings, audit_events CASCADE`); err != nil {
		t.Fatalf("reset database: %v", err)
	}
	f := newUploadFixture(t, pool)
	user := f.user(t)
	stranger := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, user, "b@gmail.com", 1<<30)
	f.link(t, stranger, "c@gmail.com", 1<<30)

	put := func(owner uuid.UUID, folder *uuid.UUID, name, body string) File {
		t.Helper()
		file, err := f.service.Upload(ctx, owner, UploadInput{Name: name, FolderID: folder, Size: int64(len(body)), Body: strings.NewReader(body)})
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		return file
	}
	photos, err := f.service.CreateFolder(ctx, user, nil, "Photos")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	trip, err := f.service.CreateFolder(ctx, user, &photos.ID, "Trip")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	put(user, nil, "one.txt", "same content")
	put(user, &trip.ID, "two.txt", "same content")
	trashed := put(user, nil, "three.txt", "same content")
	if err := f.service.TrashFile(ctx, user, trashed.ID, ""); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	put(user, nil, "alone.txt", "different")
	put(stranger, nil, "theirs.txt", "same content")
	put(user, nil, "empty-a.txt", "")
	put(user, nil, "empty-b.txt", "")

	bin, err := f.service.CreateFolder(ctx, user, nil, "Bin")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	put(user, nil, "lonely.txt", "only live copy")
	put(user, &bin.ID, "hidden.txt", "only live copy")
	if err := f.service.TrashFolder(ctx, user, bin.ID, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}

	found, err := f.service.Duplicates(ctx, user)
	if err != nil {
		t.Fatalf("Duplicates: %v", err)
	}
	if len(found.Groups) != 1 {
		t.Fatalf("groups = %+v", found.Groups)
	}
	group := found.Groups[0]
	if len(group.Files) != 2 || group.Files[0].File.Name != "one.txt" || group.Files[1].File.Name != "two.txt" {
		t.Fatalf("group = %+v", group)
	}
	if group.Files[0].Location != "" || group.Files[1].Location != "Photos/Trip" {
		t.Errorf("locations %q, %q", group.Files[0].Location, group.Files[1].Location)
	}
	if want := int64(len("same content")); group.Size != want || found.ReclaimableBytes != want {
		t.Errorf("size %d, reclaimable %d, want %d", group.Size, found.ReclaimableBytes, want)
	}

	theirs, err := f.service.Duplicates(ctx, stranger)
	if err != nil {
		t.Fatalf("Duplicates: %v", err)
	}
	if len(theirs.Groups) != 0 {
		t.Errorf("a stranger's groups = %+v", theirs.Groups)
	}
}

// getFailure is an account whose downloads fail, as a dropped connection would.
type getFailure struct{ provider.Provider }

func (getFailure) Get(context.Context, string, *provider.ByteRange) (io.ReadCloser, error) {
	return nil, errors.New("connection lost")
}

func failingGets(p provider.Provider) provider.Provider { return getFailure{p} }

// getCounter is an account that counts its downloads.
type getCounter struct {
	provider.Provider
	reads *atomic.Int32
}

func (c getCounter) Get(ctx context.Context, id string, rng *provider.ByteRange) (io.ReadCloser, error) {
	c.reads.Add(1)
	return c.Provider.Get(ctx, id, rng)
}

func countingGets(reads *atomic.Int32) func(provider.Provider) provider.Provider {
	return func(p provider.Provider) provider.Provider { return getCounter{p, reads} }
}
