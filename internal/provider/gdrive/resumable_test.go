package gdrive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/swayam5342/tributary/internal/provider"
)

func openProvider(t *testing.T, f *fakeDrive) *Provider {
	t.Helper()
	c := newTestClient(f)
	settings, credentials := connect(t, c)
	p, err := c.Open(context.Background(), settings, credentials)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	return p
}

func chunkOf(body []byte, offset, total int64) provider.Chunk {
	return provider.Chunk{Offset: offset, Length: int64(len(body)), Total: total, Body: bytes.NewReader(body)}
}

func TestResumableSendsWhatGoogleExpects(t *testing.T) {
	f := newFakeDrive(t)
	p := openProvider(t, f)
	ctx := context.Background()
	session, err := p.BeginResumable(ctx, provider.Resumable{Name: "a.bin", ContentType: "text/plain", Size: 10})
	if err != nil {
		t.Fatalf("BeginResumable: %v", err)
	}
	f.mu.Lock()
	got := f.lastBegin
	f.mu.Unlock()
	if got.length != "10" || got.contentType != "text/plain" || got.parent != p.folderID {
		t.Errorf("begin = %+v", got)
	}
	if !strings.Contains(got.fields, "id") {
		t.Errorf("fields = %q, the finished file can't be read without them", got.fields)
	}
	if _, err := p.PutChunk(ctx, session, chunkOf([]byte("0123456789"), 0, 10)); err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
}

// Google usually answers 308, but sends a 200 saying so when asked. Both must work.
func TestResumableUnderstandsBothAnswers(t *testing.T) {
	for _, real308 := range []bool{false, true} {
		f := newFakeDrive(t)
		f.real308 = real308
		p := openProvider(t, f)
		ctx := context.Background()
		const align = provider.ChunkAlignment
		session, err := p.BeginResumable(ctx, provider.Resumable{Name: "a.bin", Size: align + 1})
		if err != nil {
			t.Fatalf("BeginResumable: %v", err)
		}
		state, err := p.PutChunk(ctx, session, chunkOf(make([]byte, align), 0, align+1))
		if err != nil {
			t.Fatalf("real308 %v: PutChunk: %v", real308, err)
		}
		if state.Received != align || state.Object != nil {
			t.Errorf("real308 %v: state = %+v", real308, state)
		}
	}
}

func TestResumableReportsNothingKept(t *testing.T) {
	f := newFakeDrive(t)
	p := openProvider(t, f)
	ctx := context.Background()
	session, err := p.BeginResumable(ctx, provider.Resumable{Name: "a.bin", Size: 10})
	if err != nil {
		t.Fatalf("BeginResumable: %v", err)
	}
	state, err := p.ResumableStatus(ctx, session, 10)
	if err != nil {
		t.Fatalf("ResumableStatus: %v", err)
	}
	if state.Received != 0 || state.Object != nil {
		t.Errorf("state = %+v", state)
	}
}

// Google can keep less than it was sent, and the caller has to be told.
func TestResumableTellsTheTruthAboutWhatWasKept(t *testing.T) {
	f := newFakeDrive(t)
	f.dropBytes = provider.ChunkAlignment
	p := openProvider(t, f)
	ctx := context.Background()
	const align = provider.ChunkAlignment
	session, err := p.BeginResumable(ctx, provider.Resumable{Name: "a.bin", Size: 3 * align})
	if err != nil {
		t.Fatalf("BeginResumable: %v", err)
	}
	state, err := p.PutChunk(ctx, session, chunkOf(make([]byte, 2*align), 0, 3*align))
	if err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
	if state.Received != align {
		t.Errorf("Received = %d, want %d", state.Received, align)
	}
}

func TestResumableExpiredSession(t *testing.T) {
	f := newFakeDrive(t)
	p := openProvider(t, f)
	ctx := context.Background()
	session, err := p.BeginResumable(ctx, provider.Resumable{Name: "a.bin", Size: 10})
	if err != nil {
		t.Fatalf("BeginResumable: %v", err)
	}
	f.expireSessions()
	if _, err := p.ResumableStatus(ctx, session, 10); !errors.Is(err, provider.ErrSessionExpired) {
		t.Errorf("error = %v, want ErrSessionExpired", err)
	}
	if err := p.CancelResumable(ctx, session); err != nil {
		t.Errorf("cancelling an expired session: %v", err)
	}
}

// A session address grants access to the upload, so it must never reach a log.
func TestErrorsNeverCarryTheSessionAddress(t *testing.T) {
	f := newFakeDrive(t)
	p := openProvider(t, f)
	ctx := context.Background()

	dead := httptest.NewServer(http.NotFoundHandler())
	session := dead.URL + "/upload/session/secret-upload-id"
	dead.Close()

	_, chunkErr := p.PutChunk(ctx, session, chunkOf([]byte("x"), 0, 1))
	_, statusErr := p.ResumableStatus(ctx, session, 1)
	cancelErr := p.CancelResumable(ctx, session)
	for name, err := range map[string]error{"chunk": chunkErr, "status": statusErr, "cancel": cancelErr} {
		if err == nil {
			t.Fatalf("%s: error = nil", name)
		}
		if strings.Contains(err.Error(), "secret-upload-id") {
			t.Errorf("%s error leaks the address: %v", name, err)
		}
	}
}

func TestAServerErrorIsNotAnExpiredSession(t *testing.T) {
	f := newFakeDrive(t)
	p := openProvider(t, f)
	ctx := context.Background()
	session, err := p.BeginResumable(ctx, provider.Resumable{Name: "a.bin", Size: 10})
	if err != nil {
		t.Fatalf("BeginResumable: %v", err)
	}
	f.mu.Lock()
	f.failChunks = true
	f.mu.Unlock()
	_, err = p.PutChunk(ctx, session, chunkOf([]byte("0123456789"), 0, 10))
	if err == nil || errors.Is(err, provider.ErrSessionExpired) {
		t.Errorf("error = %v, want a retryable failure", err)
	}
}

// Rule 7: one chunk streams through, however large it is.
func TestChunkMemoryStaysFlat(t *testing.T) {
	f := newFakeDrive(t)
	f.discardBodies = true
	p := openProvider(t, f)
	ctx := context.Background()

	const size = 256 << 20
	const limit = 32 << 20
	session, err := p.BeginResumable(ctx, provider.Resumable{Name: "big.bin", Size: size})
	if err != nil {
		t.Fatalf("BeginResumable: %v", err)
	}
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
	state, err := p.PutChunk(ctx, session, provider.Chunk{Length: size, Total: size, Body: io.LimitReader(zeros{}, size)})
	close(done)
	<-sampled
	if err != nil {
		t.Fatalf("PutChunk: %v", err)
	}
	if state.Object == nil || state.Object.Size != size {
		t.Errorf("state = %+v", state)
	}
	if grew := peak.Load() - min(peak.Load(), before.HeapAlloc); grew > limit {
		t.Errorf("heap grew by %d MiB for a %d MiB chunk", grew>>20, size>>20)
	}
}
