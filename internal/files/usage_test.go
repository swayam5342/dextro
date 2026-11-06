package files

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/swayam5342/dextro/internal/db"
	"github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/httpx"
	"github.com/swayam5342/dextro/internal/httpx/apigen"
)

func (f *uploadFixture) putTyped(t *testing.T, userID uuid.UUID, contentType, body string) File {
	t.Helper()
	file, err := f.service.Upload(f.ctx, userID, UploadInput{Name: uuid.NewString(), ContentType: contentType, Size: int64(len(body)), Body: strings.NewReader(body)})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	return file
}

func TestUsage(t *testing.T) {
	pool := dbtest.Start(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	f := newUploadFixture(t, pool)
	user := f.user(t)
	f.link(t, user, "a@gmail.com", 1<<30)
	f.putTyped(t, user, "image/jpeg", "123")
	f.putTyped(t, user, "image/png", "45")
	f.putTyped(t, user, "video/mp4", "1234567")
	f.putTyped(t, user, "application/pdf", "1234")
	f.putTyped(t, user, "text/plain", "1")
	f.putTyped(t, user, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", "12")
	f.putTyped(t, user, "application/vnd.ms-excel", "123")
	f.putTyped(t, user, "application/zip", "this is neither")
	trashed := f.putTyped(t, user, "image/gif", "12345678")
	if err := f.service.TrashFile(f.ctx, user, trashed.ID, ""); err != nil {
		t.Fatalf("TrashFile: %v", err)
	}

	got, err := f.service.Usage(f.ctx, user)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	// The trash still takes up room in the account, so it counts.
	if want := (Usage{Photo: 13, Video: 7, Document: 10}); got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}

	stranger := f.user(t)
	theirs, err := f.service.Usage(f.ctx, stranger)
	if err != nil {
		t.Fatalf("Usage: %v", err)
	}
	if theirs != (Usage{}) {
		t.Errorf("a stranger sees %+v", theirs)
	}

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
	req := httptest.NewRequest(http.MethodGet, "/api/v1/usage", nil)
	req.AddCookie(&http.Cookie{Name: httpx.SessionCookieName, Value: user.String()})
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"photo_bytes":13`) {
		t.Errorf("status = %d, body %s", rec.Code, rec.Body)
	}
}
