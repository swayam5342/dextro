package gdrive

import (
	"context"
	"errors"
	"io"
	"net/url"
	"runtime"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swayam5342/tributary/internal/provider"
	"github.com/swayam5342/tributary/internal/provider/providertest"
)

const testVerifier = "the-verifier-the-verifier-the-verifier-the-verifier"

func newTestClient(f *fakeDrive) *Client {
	return newClient("client-id", "client-secret", "https://files.example.org/callback",
		f.server.URL+"/auth", f.server.URL+"/token", f.server.URL+"/drive/v3/")
}

func connect(t *testing.T, c *Client) (Settings, Credentials) {
	t.Helper()
	settings, credentials, err := c.Connect(context.Background(), "the-code", testVerifier)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	return settings, credentials
}

func TestConformance(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	p.pageSize = providertest.ListPageSize
	providertest.Run(t, p)
	// Rule 6: making something public is one explicit act, never a side effect.
	if len(f.permissionFiles) == 0 {
		t.Error("the suite never reached the permissions, so this proves nothing")
	}
	for _, name := range f.permissionFiles {
		if !strings.HasPrefix(name, providertest.SharedPrefix) {
			t.Errorf("the adapter touched the permissions of %q", name)
		}
	}
}

func TestShare(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	object, err := p.Put(ctx, provider.Upload{Name: "a.txt", Body: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	link, err := p.Share(ctx, object.ID, provider.ShareCommenter)
	if err != nil {
		t.Fatalf("Share: %v", err)
	}
	if !strings.Contains(link, object.ID) {
		t.Errorf("link = %q", link)
	}
	created := f.anyone[object.ID]
	if created.Type != "anyone" || created.Role != "commenter" {
		t.Errorf("permission = %+v", created)
	}
	if created.AllowFileDiscovery == nil || *created.AllowFileDiscovery {
		t.Errorf("allowFileDiscovery = %v, want an explicit false", created.AllowFileDiscovery)
	}
	if _, err := p.Share(ctx, object.ID, provider.ShareEditor); err != nil {
		t.Fatalf("Share again: %v", err)
	}
	if got := f.anyone[object.ID].Role; got != "writer" {
		t.Errorf("role = %q", got)
	}
	if err := p.Unshare(ctx, object.ID); err != nil {
		t.Fatalf("Unshare: %v", err)
	}
	if _, ok := f.anyone[object.ID]; ok {
		t.Error("the permission is still there")
	}

	f.shareForbidden = true
	if _, err := p.Share(ctx, object.ID, provider.ShareViewer); !errors.Is(err, provider.ErrShareRefused) {
		t.Errorf("error = %v, want ErrShareRefused", err)
	}
}

func TestAuthCodeURL(t *testing.T) {
	f := newFakeDrive(t)
	raw := newTestClient(f).AuthCodeURL("the-state", testVerifier)
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	q := parsed.Query()
	want := map[string]string{
		"state":                 "the-state",
		"client_id":             "client-id",
		"redirect_uri":          "https://files.example.org/callback",
		"access_type":           "offline",
		"prompt":                "select_account consent",
		"code_challenge_method": "S256",
		// Exactly this: Tributary must not be able to read the rest of someone's Drive.
		"scope": "https://www.googleapis.com/auth/drive.file",
	}
	for key, value := range want {
		if got := q.Get(key); got != value {
			t.Errorf("%s = %q, want %q", key, got, value)
		}
	}
	if q.Get("code_challenge") == "" || strings.Contains(raw, testVerifier) {
		t.Errorf("url must carry the PKCE challenge, not the verifier: %s", raw)
	}
}

func TestConnect(t *testing.T) {
	f := newFakeDrive(t)
	settings, credentials := connect(t, newTestClient(f))
	if settings.Email != "person@example.org" || settings.FolderID == "" {
		t.Errorf("settings = %+v", settings)
	}
	if credentials.RefreshToken != "the-refresh-token" {
		t.Errorf("credentials = %+v", credentials)
	}
	if f.gotVerifier != testVerifier {
		t.Errorf("code_verifier = %q", f.gotVerifier)
	}
}

func TestReconnectingReusesTheFolder(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	first, _ := connect(t, c)
	second, _ := connect(t, c)
	if first.FolderID != second.FolderID {
		t.Errorf("folder changed: %s then %s", first.FolderID, second.FolderID)
	}
	if got := f.countFolders(); got != 1 {
		t.Errorf("folders = %d, want 1", got)
	}
}

func TestConnectFailures(t *testing.T) {
	t.Run("a bad code", func(t *testing.T) {
		f := newFakeDrive(t)
		if _, _, err := newTestClient(f).Connect(context.Background(), "wrong", testVerifier); err == nil {
			t.Fatal("Connect succeeded, want error")
		}
	})
	t.Run("no refresh token", func(t *testing.T) {
		f := newFakeDrive(t)
		f.omitRefresh = true
		if _, _, err := newTestClient(f).Connect(context.Background(), "the-code", testVerifier); err == nil {
			t.Fatal("Connect succeeded, want error: the connection would stop working within the hour")
		}
	})
}

func TestCheck(t *testing.T) {
	setup := func(t *testing.T) (*fakeDrive, *Provider, Settings) {
		t.Helper()
		f := newFakeDrive(t)
		c := newTestClient(f)
		settings, credentials := connect(t, c)
		p, err := c.Open(context.Background(), settings, credentials)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return f, p, settings
	}

	t.Run("a revoked token fails", func(t *testing.T) {
		f, p, _ := setup(t)
		f.refreshRevoked = true
		if err := p.Check(context.Background()); err == nil {
			t.Fatal("Check succeeded, want error")
		}
	})

	t.Run("a trashed folder fails", func(t *testing.T) {
		f, p, settings := setup(t)
		f.files[settings.FolderID].Trashed = true
		if err := p.Check(context.Background()); !errors.Is(err, ErrFolderMissing) {
			t.Fatalf("error = %v, want ErrFolderMissing", err)
		}
	})

	t.Run("a deleted folder fails", func(t *testing.T) {
		f, p, settings := setup(t)
		delete(f.files, settings.FolderID)
		if err := p.Check(context.Background()); !errors.Is(err, ErrFolderMissing) {
			t.Fatalf("error = %v, want ErrFolderMissing", err)
		}
	})
}

func TestQuota(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	f.limit, f.used = 15<<30, 4<<30
	quota, err := p.Quota(context.Background())
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if quota.Limit != 15<<30 || quota.Used != 4<<30 {
		t.Errorf("quota = %+v", quota)
	}
	free, limited := quota.Free()
	if !limited || free != 11<<30 {
		t.Errorf("Free = %d, %v", free, limited)
	}

	// Some accounts report no limit at all, and an over-full one has no room.
	f.limit, f.used = 0, 4<<30
	quota, err = p.Quota(context.Background())
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if _, limited := quota.Free(); limited {
		t.Errorf("an account with no limit reports one: %+v", quota)
	}
	f.limit, f.used = 1<<30, 2<<30
	quota, err = p.Quota(context.Background())
	if err != nil {
		t.Fatalf("Quota: %v", err)
	}
	if free, _ := quota.Free(); free != 0 {
		t.Errorf("Free = %d, want 0 for an over-full account", free)
	}
}

func TestQuotaFailsWithARevokedToken(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.refreshRevoked = true
	if _, err := p.Quota(context.Background()); err == nil {
		t.Fatal("Quota succeeded, want error")
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

// Rule 7: an upload's memory use must not grow with the file.
func TestUploadMemoryStaysFlat(t *testing.T) {
	f := newFakeDrive(t)
	f.discardBodies = true
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	const size = 256 << 20
	// Measured against the chunk buffer, with room for the client and the runtime.
	const limit = 8 * chunkSize
	// HeapAlloc counts garbage as well as what's in use, so how high it climbs depends on
	// when the collector happens to run. Collecting often keeps it close to what the upload
	// really holds, or a busy machine fails this with no leak at all.
	defer debug.SetGCPercent(debug.SetGCPercent(10))
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	var peak atomic.Uint64
	done := make(chan struct{})
	sampled := make(chan struct{})
	go func() {
		defer close(sampled)
		for {
			select {
			case <-done:
				return
			case <-time.After(10 * time.Millisecond):
				var now runtime.MemStats
				runtime.ReadMemStats(&now)
				if now.HeapAlloc > peak.Load() {
					peak.Store(now.HeapAlloc)
				}
			}
		}
	}()
	object, err := p.Put(context.Background(), provider.Upload{Name: "big.bin", Body: io.LimitReader(zeros{}, size)})
	close(done)
	<-sampled
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if object.Size != size {
		t.Errorf("Size = %d, want %d", object.Size, size)
	}
	grew := peak.Load() - min(peak.Load(), before.HeapAlloc)
	t.Logf("heap grew by %d MiB for a %d MiB file", grew>>20, size>>20)
	if grew > limit {
		t.Errorf("heap grew by %d MiB for a %d MiB file", grew>>20, size>>20)
	}
}

func TestThumbnail(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	object, err := p.Put(context.Background(), provider.Upload{Name: "photo.jpg", Body: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}

	if _, err := p.Thumbnail(context.Background(), object.ID); !errors.Is(err, provider.ErrNoThumbnail) {
		t.Fatalf("before Google makes one: error = %v, want ErrNoThumbnail", err)
	}

	f.thumbnails[object.ID] = []byte("small image")
	thumbnail, err := p.Thumbnail(context.Background(), object.ID)
	if err != nil {
		t.Fatalf("Thumbnail: %v", err)
	}
	defer func() { _ = thumbnail.Body.Close() }()
	body, err := io.ReadAll(thumbnail.Body)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "small image" || thumbnail.ContentType != "image/jpeg" {
		t.Errorf("thumbnail = %q, %q", body, thumbnail.ContentType)
	}
	if !strings.HasSuffix(f.gotThumbPath, "=s480") {
		t.Errorf("asked for %q, want a 480px image", f.gotThumbPath)
	}
}

// Moving a file to Drive's trash counts as gone, as it does in a listing.
func TestExistsTreatsTrashAsGone(t *testing.T) {
	f := newFakeDrive(t)
	p := openProvider(t, f)
	ctx := context.Background()
	object, err := p.Put(ctx, provider.Upload{Name: "a.txt", Body: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	f.mu.Lock()
	f.files[object.ID].Trashed = true
	f.mu.Unlock()
	if exists, err := p.Exists(ctx, object.ID); err != nil || exists {
		t.Errorf("exists = %v, error = %v", exists, err)
	}
}

func TestExistsReportsFailures(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	f.mu.Lock()
	f.refreshRevoked = true
	f.mu.Unlock()
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if exists, err := p.Exists(context.Background(), "file-1"); err == nil || exists {
		t.Errorf("exists = %v, error = %v, a failure must not read as gone", exists, err)
	}
}

// Each request opens the account afresh, and fetching a new access token every time
// would add a trip to Google to every file and thumbnail.
func TestOpeningAnAccountReusesItsToken(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	ctx := context.Background()
	for range 5 {
		p, err := c.Open(ctx, settings, credentials)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := p.Check(ctx); err != nil {
			t.Fatalf("Check: %v", err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refreshes != 1 {
		t.Errorf("token requests = %d, want 1", f.refreshes)
	}
}

func TestEachAccountHasItsOwnToken(t *testing.T) {
	f := newFakeDrive(t)
	c := newTestClient(f)
	first := c.tokenSource("first-refresh-token")
	if second := c.tokenSource("second-refresh-token"); first == second {
		t.Error("two accounts share a token source")
	}
	if again := c.tokenSource("first-refresh-token"); first != again {
		t.Error("the same account got a second token source")
	}
}

func TestDescribeTreatsTrashAsGone(t *testing.T) {
	f := newFakeDrive(t)
	p := openProvider(t, f)
	ctx := context.Background()
	object, err := p.Put(ctx, provider.Upload{Name: "a.txt", Body: strings.NewReader("x")})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	f.mu.Lock()
	f.files[object.ID].Trashed = true
	f.mu.Unlock()
	if _, err := p.Describe(ctx, object.ID); !errors.Is(err, provider.ErrObjectNotFound) {
		t.Errorf("error = %v, want ErrObjectNotFound", err)
	}
}
