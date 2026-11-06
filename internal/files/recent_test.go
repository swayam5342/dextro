package files

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/tributary/internal/db"
	"github.com/swayam5342/tributary/internal/db/dbtest"
	"github.com/swayam5342/tributary/internal/httpx"
	"github.com/swayam5342/tributary/internal/httpx/apigen"
)

func wantNames(t *testing.T, got []File, want ...string) {
	t.Helper()
	have := make([]string, 0, len(got))
	for _, file := range got {
		have = append(have, file.Name)
	}
	if len(have) != len(want) {
		t.Fatalf("files = %v, want %v", have, want)
	}
	for i := range want {
		if have[i] != want[i] {
			t.Fatalf("files = %v, want %v", have, want)
		}
	}
}

func TestRecentAndStarred(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	f := newUploadFixture(t, pool)
	user, stranger := f.user(t), f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.link(t, stranger, "b@gmail.com", 1<<30)

	put := func(owner uuid.UUID, name string, folder *uuid.UUID) File {
		t.Helper()
		file, err := f.service.Upload(ctx, owner, UploadInput{Name: name, ContentType: "text/plain", Size: 1, Body: strings.NewReader("x"), FolderID: folder})
		if err != nil {
			t.Fatalf("Upload: %v", err)
		}
		return file
	}
	folder, err := f.service.CreateFolder(ctx, user, nil, "Old")
	if err != nil {
		t.Fatalf("CreateFolder: %v", err)
	}
	first := put(user, "first", nil)
	inFolder := put(user, "in folder", &folder.ID)
	second := put(user, "second", nil)
	third := put(user, "third", nil)
	put(stranger, "theirs", nil)

	recent := func(owner uuid.UUID) []File {
		t.Helper()
		got, err := f.service.Recent(ctx, owner)
		if err != nil {
			t.Fatalf("Recent: %v", err)
		}
		return got
	}
	starred := func(owner uuid.UUID) []File {
		t.Helper()
		got, err := f.service.Starred(ctx, owner)
		if err != nil {
			t.Fatalf("Starred: %v", err)
		}
		return got
	}

	wantNames(t, recent(user), "third", "second", "in folder", "first")
	wantNames(t, recent(stranger), "theirs")

	star := func(id uuid.UUID, on bool) {
		t.Helper()
		if err := f.service.SetStarred(ctx, user, id, on); err != nil {
			t.Fatalf("SetStarred: %v", err)
		}
	}
	wantNames(t, starred(user))
	star(first.ID, true)
	star(second.ID, true)
	star(inFolder.ID, true)
	wantNames(t, starred(user), "in folder", "second", "first")

	got, err := f.service.Get(ctx, user, first.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.Starred || !got.UpdatedAt.Equal(first.UpdatedAt) {
		t.Errorf("starred = %v, updated %v, was %v", got.Starred, got.UpdatedAt, first.UpdatedAt)
	}

	star(second.ID, false)
	wantNames(t, starred(user), "in folder", "first")

	// Someone else's file can't be starred, and doesn't say it exists.
	if err := f.service.SetStarred(ctx, stranger, first.ID, true); !errors.Is(err, ErrFileNotFound) {
		t.Errorf("stranger: err = %v, want ErrFileNotFound", err)
	}
	wantNames(t, starred(stranger))

	// Trash hides a file from both, and restoring brings its star back.
	if err := f.service.TrashFile(ctx, user, first.ID, ""); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}
	if err := f.service.TrashFolder(ctx, user, folder.ID, ""); err != nil {
		t.Fatalf("TrashFolder: %v", err)
	}
	wantNames(t, starred(user))
	wantNames(t, recent(user), "third", "second")
	if err := f.service.RestoreFile(ctx, user, first.ID); err != nil {
		t.Fatalf("RestoreFile: %v", err)
	}
	wantNames(t, starred(user), "first")

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	router := httpx.NewRouter(httpx.Deps{
		Logger: logger, DB: pool, PublicOrigin: testOrigin, Sessions: sessions{}, APIKeys: keys{},
		Auth: apigen.Unimplemented{}, Admin: apigen.Unimplemented{}, Storage: apigen.Unimplemented{},
		Files: NewHandler(f.service, logger),
	})
	call := func(method, target string, owner uuid.UUID) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, target, nil)
		req.Header.Set("Origin", testOrigin)
		req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: owner.String()})
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	if rec := call(http.MethodPut, "/api/v1/files/"+third.ID.String()+"/star", user); rec.Code != http.StatusNoContent {
		t.Fatalf("star: %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodPut, "/api/v1/files/"+third.ID.String()+"/star", stranger); rec.Code != http.StatusNotFound {
		t.Errorf("a stranger's star: %d %s", rec.Code, rec.Body)
	}
	rec := call(http.MethodGet, "/api/v1/starred", user)
	var body apigen.FileList
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || len(body.Files) != 2 || body.Files[0].Name != "third" || !body.Files[0].Starred {
		t.Errorf("starred: %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodDelete, "/api/v1/files/"+third.ID.String()+"/star", user); rec.Code != http.StatusNoContent {
		t.Fatalf("unstar: %d %s", rec.Code, rec.Body)
	}
	if rec := call(http.MethodGet, "/api/v1/recent", user); rec.Code != http.StatusOK {
		t.Errorf("recent: %d %s", rec.Code, rec.Body)
	}
}
