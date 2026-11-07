package files

import (
	"bytes"
	"context"
	"crypto/md5" //nolint:gosec // What Drive reports, so a copy matches a plain file.
	"encoding/hex"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
	"github.com/swayam5342/tributary/internal/provider"
)

const secretText = "the quick brown fox keeps a secret"

func md5Hex(body []byte) string {
	sum := md5.Sum(body) //nolint:gosec // What Drive reports, so a copy matches a plain file.
	return hex.EncodeToString(sum[:])
}

// vault makes a folder whose new files are encrypted.
func (f *uploadFixture) vault(t *testing.T, userID uuid.UUID, name string) Folder {
	t.Helper()
	folder, err := f.service.CreateFolder(f.ctx, userID, nil, name)
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	folder, err = f.service.SetFolderEncryption(f.ctx, userID, folder.ID, true, "203.0.113.7")
	if err != nil {
		t.Fatalf("SetFolderEncryption: %v", err)
	}
	return folder
}

// sendAll uploads a body in the pieces the session asks for.
func (f *uploadFixture) sendAll(t *testing.T, userID uuid.UUID, folder *uuid.UUID, name, contentType string, body []byte) File {
	t.Helper()
	session, err := f.service.BeginUpload(f.ctx, userID, BeginUploadInput{Name: name, ContentType: contentType, FolderID: folder, Size: int64(len(body)), LastModified: 1})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	if !session.Encrypted {
		t.Fatal("the session isn't encrypted")
	}
	total := int64(len(body))
	for session.File == nil {
		start := session.Received
		end := start + nextSealedPiece(start, total)
		session, err = f.service.PutUploadChunk(f.ctx, userID, session.ID, start, end-start, bytes.NewReader(body[start:end]))
		if err != nil {
			t.Fatalf("PutUploadChunk at %d: %v", start, err)
		}
	}
	return *session.File
}

func (f *uploadFixture) read(t *testing.T, userID, id uuid.UUID, rng *provider.ByteRange) []byte {
	t.Helper()
	reader, err := f.service.Open(f.ctx, userID, id, rng)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return body
}

// raw is what the account really holds for a file.
func (f *uploadFixture) raw(t *testing.T, email string, objectID string) []byte {
	t.Helper()
	reader, err := f.account(email).Get(f.ctx, objectID, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer func() { _ = reader.Close() }()
	body, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return body
}

func TestEncryptedFiles(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	tests := []struct {
		name string
		run  func(*testing.T, *uploadFixture)
	}{
		{"the account never sees the file", testAccountSeesNoPlaintext},
		{"a file bigger than a piece comes back whole", testManyPieces},
		{"folders below an encrypted folder count", testEncryptionInherited},
		{"other folders aren't encrypted", testPlainFoldersStayPlain},
		{"turning it on leaves stored files alone", testTurningOnKeepsFiles},
		{"turning it off stops new files being encrypted", testTurningOff},
		{"a one-request upload is encrypted too", testOneRequestEncrypted},
		{"an empty file works", testEmptyEncrypted},
		{"a range only returns that range", testEncryptedRange},
		{"a piece of the wrong length is refused", testWrongPieceRefused},
		{"a failed piece can be sent again", testRetryAfterFailure},
		{"an account keeping less stays on a frame", testKeptLessEncrypted},
		{"asking how far it got works in file bytes", testProgressEncrypted},
		{"an encrypted file moves between accounts", testEncryptedMoves},
		{"an encrypted file goes in a zip", testEncryptedZip},
		{"an encrypted copy matches a plain one", testEncryptedDuplicates},
		{"encrypted files have no thumbnail", testEncryptedThumbnail},
		{"encrypted files can't have a link", testEncryptedShare},
		{"a sync brings encrypted files back", testEncryptedRecovery},
		{"a sync leaves an encrypted file alone", testEncryptedSyncQuiet},
		{"a tampered file refuses to open", testEncryptedTampering},
		{"a damaged file is reported", testEncryptedCheck},
		{"folders are private to their owner", testEncryptionIsScoped},
		{"turning it on is audited", testEncryptionAudited},
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

func testAccountSeesNoPlaintext(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	file := f.sendAll(t, user, &vault.ID, "secret.txt", "text/plain", []byte(secretText))

	if !file.Encrypted || file.Size != int64(len(secretText)) || file.ContentType != "text/plain" {
		t.Errorf("file = %+v", file)
	}
	object := f.objectID(t, user, file.ID)
	stored := f.raw(t, "a@gmail.com", object)
	if bytes.Contains(stored, []byte("secret")) && bytes.Contains(stored, []byte("quick brown")) {
		t.Error("the account can read the file")
	}
	described, err := f.account("a@gmail.com").Describe(f.ctx, object)
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if described.ContentType != sealedContentType {
		t.Errorf("the account was told %q", described.ContentType)
	}
	if got := f.read(t, user, file.ID, nil); string(got) != secretText {
		t.Errorf("read back %q", got)
	}
}

func testManyPieces(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	body := randomBytes(t, 3*sealedPieceFrames*sealedPayload+12345)
	file := f.sendAll(t, user, &vault.ID, "big.bin", "application/octet-stream", body)
	if file.Size != int64(len(body)) {
		t.Errorf("size = %d", file.Size)
	}
	if !bytes.Equal(f.read(t, user, file.ID, nil), body) {
		t.Error("the file came back different")
	}
	if len(f.raw(t, "a@gmail.com", f.objectID(t, user, file.ID))) != int(storedSize(int64(len(body)))) {
		t.Error("the stored size doesn't match")
	}
}

func testEncryptionInherited(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	inner, err := f.service.CreateFolder(f.ctx, user, &vault.ID, "Inner")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	contents, err := f.service.List(f.ctx, user, &inner.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if !contents.Encrypted {
		t.Error("the listing doesn't say so")
	}
	if file := f.sendAll(t, user, &inner.ID, "a.txt", "text/plain", []byte("abc")); !file.Encrypted {
		t.Error("the file isn't encrypted")
	}
}

func testPlainFoldersStayPlain(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.vault(t, user, "Vault")
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a.txt", Size: 3})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	if session.Encrypted {
		t.Error("a plain folder encrypted a file")
	}
	done, err := f.service.PutUploadChunk(f.ctx, user, session.ID, 0, 3, strings.NewReader("abc"))
	if err != nil {
		t.Fatalf("PutUploadChunk: %v", err)
	}
	if done.File.Encrypted || string(f.raw(t, "a@gmail.com", f.objectID(t, user, done.File.ID))) != "abc" {
		t.Errorf("file = %+v", done.File)
	}
}

func testTurningOnKeepsFiles(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	folder, err := f.service.CreateFolder(f.ctx, user, nil, "Later")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	file, err := f.service.Upload(f.ctx, user, UploadInput{Name: "old.txt", FolderID: &folder.ID, Size: 5, Body: strings.NewReader("hello")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if _, err := f.service.SetFolderEncryption(f.ctx, user, folder.ID, true, ""); err != nil {
		t.Fatalf("SetFolderEncryption: %v", err)
	}
	if got := f.read(t, user, file.ID, nil); string(got) != "hello" {
		t.Errorf("read back %q", got)
	}
}

func testTurningOff(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	before := f.sendAll(t, user, &vault.ID, "before.txt", "text/plain", []byte("kept safe"))
	if _, err := f.service.SetFolderEncryption(f.ctx, user, vault.ID, false, ""); err != nil {
		t.Fatalf("SetFolderEncryption: %v", err)
	}
	after, err := f.service.Upload(f.ctx, user, UploadInput{Name: "after.txt", FolderID: &vault.ID, Size: 5, Body: strings.NewReader("hello")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if after.Encrypted {
		t.Error("a new file was encrypted")
	}
	if got := f.read(t, user, before.ID, nil); string(got) != "kept safe" {
		t.Errorf("an encrypted file stopped working: %q", got)
	}
}

func testOneRequestEncrypted(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	file, err := f.service.Upload(f.ctx, user, UploadInput{Name: "a.txt", ContentType: "text/plain", FolderID: &vault.ID, Size: -1, Body: strings.NewReader(secretText)})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if !file.Encrypted || file.Size != int64(len(secretText)) {
		t.Errorf("file = %+v", file)
	}
	if bytes.Contains(f.raw(t, "a@gmail.com", f.objectID(t, user, file.ID)), []byte("quick brown")) {
		t.Error("the account can read the file")
	}
	if got := f.read(t, user, file.ID, nil); string(got) != secretText {
		t.Errorf("read back %q", got)
	}
}

func testEmptyEncrypted(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	file, err := f.service.Upload(f.ctx, user, UploadInput{Name: "empty.txt", FolderID: &vault.ID, Size: 0, Body: strings.NewReader("")})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if file.Size != 0 || !file.Encrypted {
		t.Errorf("file = %+v", file)
	}
	if got := f.read(t, user, file.ID, nil); len(got) != 0 {
		t.Errorf("read back %q", got)
	}
}

func testEncryptedRange(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	body := randomBytes(t, 3*sealedPayload)
	file := f.sendAll(t, user, &vault.ID, "a.bin", "application/octet-stream", body)
	start, end := int64(sealedPayload+5), int64(2*sealedPayload+5)
	if got := f.read(t, user, file.ID, &provider.ByteRange{Start: start, End: end}); !bytes.Equal(got, body[start:end+1]) {
		t.Error("wrong bytes back")
	}
}

func testWrongPieceRefused(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a.bin", FolderID: &vault.ID, Size: 1 << 24})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	// A plain upload's piece, which isn't a whole number of frames here.
	if _, err := f.service.PutUploadChunk(f.ctx, user, session.ID, 0, 8<<20, bytes.NewReader(make([]byte, 8<<20))); !errors.Is(err, ErrInvalidChunk) {
		t.Errorf("err = %v", err)
	}
}

func testRetryAfterFailure(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	body := randomBytes(t, 2*sealedPieceFrames*sealedPayload)
	total := int64(len(body))
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a.bin", FolderID: &vault.ID, Size: total})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	first := nextSealedPiece(0, total)
	session, err = f.service.PutUploadChunk(f.ctx, user, session.ID, 0, first, bytes.NewReader(body[:first]))
	if err != nil {
		t.Fatalf("PutUploadChunk: %v", err)
	}
	second := nextSealedPiece(first, total)
	f.account("a@gmail.com").ChunkErr = errors.New("connection lost")
	if _, err := f.service.PutUploadChunk(f.ctx, user, session.ID, first, second, bytes.NewReader(body[first:first+second])); err == nil {
		t.Fatal("the failure wasn't reported")
	}
	f.account("a@gmail.com").ChunkErr = nil
	for session.File == nil {
		start := session.Received
		length := nextSealedPiece(start, total)
		session, err = f.service.PutUploadChunk(f.ctx, user, session.ID, start, length, bytes.NewReader(body[start:start+length]))
		if err != nil {
			t.Fatalf("PutUploadChunk: %v", err)
		}
	}
	if !bytes.Equal(f.read(t, user, session.File.ID, nil), body) {
		t.Error("the file came back different")
	}
	record := f.record(t, user, session.File.ID)
	if record.contentHash != md5Hex(body) {
		t.Errorf("hash = %q, want %q", record.contentHash, md5Hex(body))
	}
}

func testKeptLessEncrypted(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	body := randomBytes(t, 2*sealedPieceFrames*sealedPayload)
	total := int64(len(body))
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a.bin", FolderID: &vault.ID, Size: total})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	f.account("a@gmail.com").DropBytes = 2 * sealedFrame
	first := nextSealedPiece(0, total)
	session, err = f.service.PutUploadChunk(f.ctx, user, session.ID, 0, first, bytes.NewReader(body[:first]))
	if err != nil {
		t.Fatalf("PutUploadChunk: %v", err)
	}
	f.account("a@gmail.com").DropBytes = 0
	if want := plainOffset((sealedPieceFrames - 2) * sealedFrame); session.Received != want {
		t.Fatalf("received = %d, want %d", session.Received, want)
	}
	for session.File == nil {
		start := session.Received
		length := nextSealedPiece(start, total)
		session, err = f.service.PutUploadChunk(f.ctx, user, session.ID, start, length, bytes.NewReader(body[start:start+length]))
		if err != nil {
			t.Fatalf("PutUploadChunk at %d: %v", start, err)
		}
	}
	if !bytes.Equal(f.read(t, user, session.File.ID, nil), body) {
		t.Error("the file came back different")
	}
	// The hash couldn't go back, so this file just can't match duplicates.
	if got := f.record(t, user, session.File.ID).contentHash; got != "" {
		t.Errorf("hash = %q", got)
	}
}

func testProgressEncrypted(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	body := randomBytes(t, 2*sealedPieceFrames*sealedPayload)
	total := int64(len(body))
	session, err := f.service.BeginUpload(f.ctx, user, BeginUploadInput{Name: "a.bin", FolderID: &vault.ID, Size: total})
	if err != nil {
		t.Fatalf("BeginUpload: %v", err)
	}
	first := nextSealedPiece(0, total)
	if _, err := f.service.PutUploadChunk(f.ctx, user, session.ID, 0, first, bytes.NewReader(body[:first])); err != nil {
		t.Fatalf("PutUploadChunk: %v", err)
	}
	got, err := f.service.UploadProgress(f.ctx, user, session.ID)
	if err != nil {
		t.Fatalf("UploadProgress: %v", err)
	}
	if got.Received != first {
		t.Errorf("received = %d, want %d", got.Received, first)
	}
	// Only asking mustn't lose the hash.
	for session = got; session.File == nil; {
		start := session.Received
		length := nextSealedPiece(start, total)
		session, err = f.service.PutUploadChunk(f.ctx, user, session.ID, start, length, bytes.NewReader(body[start:start+length]))
		if err != nil {
			t.Fatalf("PutUploadChunk: %v", err)
		}
	}
	if record := f.record(t, user, session.File.ID); record.contentHash != md5Hex(body) {
		t.Errorf("hash = %q", record.contentHash)
	}
}

func testEncryptedMoves(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	target := f.link(t, user, "b@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	file := f.sendAll(t, user, &vault.ID, "secret.txt", "text/plain", []byte(secretText))
	f.queue(t, user, target, file.ID)
	if run := f.runMoves(t); run.Moved != 1 {
		t.Fatalf("run = %+v", run)
	}
	if f.connectionOf(t, user, file.ID) != target {
		t.Fatal("it didn't move")
	}
	if got := f.read(t, user, file.ID, nil); string(got) != secretText {
		t.Errorf("read back %q", got)
	}
	described, err := f.account("b@gmail.com").Describe(f.ctx, f.objectID(t, user, file.ID))
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	if described.ContentType != sealedContentType {
		t.Errorf("the new account was told %q", described.ContentType)
	}
}

func testEncryptedZip(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	file := f.sendAll(t, user, &vault.ID, "secret.txt", "text/plain", []byte(secretText))
	zipped := f.zipped(t, user, []uuid.UUID{file.ID}, nil)
	if got := zipped["secret.txt"]; got != secretText {
		t.Errorf("zip = %v", zipped)
	}
}

func testEncryptedDuplicates(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	f.sendAll(t, user, &vault.ID, "encrypted.txt", "text/plain", []byte(secretText))
	if _, err := f.service.Upload(f.ctx, user, UploadInput{Name: "plain.txt", Size: int64(len(secretText)), Body: strings.NewReader(secretText)}); err != nil {
		t.Fatalf("Upload: %v", err)
	}
	found, err := f.service.Duplicates(f.ctx, user)
	if err != nil {
		t.Fatalf("Duplicates: %v", err)
	}
	if len(found.Groups) != 1 || len(found.Groups[0].Files) != 2 {
		t.Errorf("groups = %+v", found.Groups)
	}
}

func testEncryptedThumbnail(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.account("a@gmail.com").SetThumbnail("image/png", []byte("picture"))
	vault := f.vault(t, user, "Vault")
	file := f.sendAll(t, user, &vault.ID, "a.png", "image/png", []byte("abc"))
	if _, err := f.service.Thumbnail(f.ctx, user, file.ID); !errors.Is(err, provider.ErrNoThumbnail) {
		t.Errorf("err = %v", err)
	}
}

func testEncryptedShare(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.allowLinks(t)
	vault := f.vault(t, user, "Vault")
	file := f.sendAll(t, user, &vault.ID, "a.txt", "text/plain", []byte("abc"))
	if _, err := f.service.ShareFile(f.ctx, user, file.ID, ShareInput{Role: "viewer"}, ""); !errors.Is(err, ErrFileEncrypted) {
		t.Errorf("err = %v", err)
	}
}

func testEncryptedRecovery(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	f.sendAll(t, user, &vault.ID, "secret.txt", "text/plain", []byte(secretText))
	f.account("a@gmail.com").Age(longAgo)
	if _, err := f.pool.Exec(f.ctx, `DELETE FROM files`); err != nil {
		t.Fatalf("lose files: %v", err)
	}
	f.sync(t, user, connection)
	contents, err := f.service.List(f.ctx, user, &vault.ID)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(contents.Files) != 1 {
		t.Fatalf("files = %+v", contents.Files)
	}
	file := contents.Files[0]
	if !file.Encrypted || file.ContentType != "text/plain" || file.Size != int64(len(secretText)) {
		t.Errorf("file = %+v", file)
	}
	if got := f.read(t, user, file.ID, nil); string(got) != secretText {
		t.Errorf("read back %q", got)
	}
}

func testEncryptedSyncQuiet(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	connection := f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	file := f.sendAll(t, user, &vault.ID, "secret.txt", "text/plain", []byte(secretText))
	before := f.record(t, user, file.ID)
	f.account("a@gmail.com").Age(longAgo)
	f.sync(t, user, connection)
	after := f.record(t, user, file.ID)
	if got := f.problems(t, user); len(got) != 0 {
		t.Errorf("problems = %+v", got)
	}
	if after.contentHash != before.contentHash || after.contentHash != md5Hex([]byte(secretText)) {
		t.Errorf("hash went from %q to %q", before.contentHash, after.contentHash)
	}
}

func testEncryptedTampering(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	file := f.sendAll(t, user, &vault.ID, "secret.txt", "text/plain", []byte(secretText))
	object := f.objectID(t, user, file.ID)
	body := f.raw(t, "a@gmail.com", object)
	body[len(body)-1] ^= 1
	f.account("a@gmail.com").Replace(object, body)
	reader, err := f.service.Open(f.ctx, user, file.ID, nil)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = reader.Close() }()
	if _, err := io.ReadAll(reader); !errors.Is(err, ErrFileUnreadable) {
		t.Errorf("err = %v", err)
	}
}

func testEncryptedCheck(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	vault := f.vault(t, user, "Vault")
	file := f.sendAll(t, user, &vault.ID, "secret.txt", "text/plain", []byte(secretText))
	if run := f.check(t); run.Checked != 1 {
		t.Errorf("a good file failed: %+v", run)
	}
	f.account("a@gmail.com").Damage(f.objectID(t, user, file.ID))
	if run := f.check(t); run.Damaged != 1 {
		t.Errorf("run = %+v", run)
	}
}

func testEncryptionIsScoped(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	stranger := f.user(t)
	vault := f.vault(t, user, "Vault")
	if _, err := f.service.SetFolderEncryption(f.ctx, stranger, vault.ID, false, ""); !errors.Is(err, ErrFolderNotFound) {
		t.Errorf("err = %v", err)
	}
}

func testEncryptionAudited(t *testing.T, f *uploadFixture) {
	user := f.user(t)
	f.vault(t, user, "Vault")
	if got := f.count(t, `SELECT count(*) FROM audit_events WHERE action = 'folder.encryption_changed'`); got != 1 {
		t.Errorf("events = %d", got)
	}
}
