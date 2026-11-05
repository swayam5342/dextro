// Package providertest holds the tests every provider must pass, and a fake
// provider for testing the code above that interface.
package providertest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/swayam5342/dextro/internal/provider"
)

// ListPageSize is the page size providers must be configured with for Run, so
// that a handful of objects spans several pages.
const ListPageSize = 2

// largeBody is bigger than any adapter's upload chunk, so it takes several.
const largeBody = 40 << 20

// Run checks that a provider behaves as the provider.Provider contract says. The
// provider must start empty and page listings at ListPageSize.
func Run(t *testing.T, p provider.Provider) {
	t.Helper()
	ctx := context.Background()

	put := func(t *testing.T, name, body string) provider.Object {
		t.Helper()
		object, err := p.Put(ctx, provider.Upload{Name: name, ContentType: "text/plain", Body: strings.NewReader(body)})
		if err != nil {
			t.Fatalf("Put(%q): %v", name, err)
		}
		t.Cleanup(func() { _ = p.Delete(ctx, object.ID) })
		return object
	}
	read := func(t *testing.T, id string) string {
		t.Helper()
		reader, err := p.Get(ctx, id, nil)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer func() { _ = reader.Close() }()
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		return string(body)
	}
	listAll := func(t *testing.T) []provider.Object {
		t.Helper()
		var all []provider.Object
		token := ""
		for range 100 {
			page, err := p.List(ctx, token)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			all = append(all, page.Objects...)
			if token = page.NextPageToken; token == "" {
				return all
			}
		}
		t.Fatal("List never reached the last page")
		return nil
	}

	runResumable(t, p, read)

	t.Run("quota reports usage", func(t *testing.T) {
		quota, err := p.Quota(ctx)
		if err != nil {
			t.Fatalf("Quota: %v", err)
		}
		if quota.Used < 0 || quota.Limit < 0 {
			t.Errorf("quota = %+v", quota)
		}
	})

	t.Run("check succeeds", func(t *testing.T) {
		if err := p.Check(ctx); err != nil {
			t.Fatalf("Check: %v", err)
		}
	})

	t.Run("a stored object reads back", func(t *testing.T) {
		object := put(t, "notes.txt", "hello")
		if object.ID == "" || object.Name != "notes.txt" || object.Size != 5 {
			t.Errorf("object = %+v", object)
		}
		if got := read(t, object.ID); got != "hello" {
			t.Errorf("body = %q", got)
		}
	})

	t.Run("a byte range reads just those bytes", func(t *testing.T) {
		object := put(t, "range.txt", "0123456789")
		tests := []struct {
			name string
			rng  provider.ByteRange
			want string
		}{
			{"the start", provider.ByteRange{Start: 0, End: 3}, "0123"},
			{"the middle", provider.ByteRange{Start: 3, End: 5}, "345"},
			{"the end", provider.ByteRange{Start: 7, End: 9}, "789"},
			{"one byte", provider.ByteRange{Start: 4, End: 4}, "4"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				reader, err := p.Get(ctx, object.ID, &tt.rng)
				if err != nil {
					t.Fatalf("Get: %v", err)
				}
				defer func() { _ = reader.Close() }()
				body, err := io.ReadAll(reader)
				if err != nil {
					t.Fatalf("read body: %v", err)
				}
				if string(body) != tt.want {
					t.Errorf("body = %q, want %q", body, tt.want)
				}
			})
		}
	})

	t.Run("an empty object is allowed", func(t *testing.T) {
		object := put(t, "empty.txt", "")
		if object.Size != 0 {
			t.Errorf("Size = %d", object.Size)
		}
		if got := read(t, object.ID); got != "" {
			t.Errorf("body = %q", got)
		}
	})

	t.Run("the same name never overwrites", func(t *testing.T) {
		first := put(t, "same.txt", "one")
		second := put(t, "same.txt", "two")
		if first.ID == second.ID {
			t.Fatal("both uploads got one ID")
		}
		if got := read(t, first.ID); got != "one" {
			t.Errorf("first body = %q", got)
		}
	})

	t.Run("awkward names survive", func(t *testing.T) {
		for _, name := range []string{"with space.txt", "a/b/../c.txt", "ünï-cødé ✓.txt", "100%.txt", "quote'\".txt"} {
			object := put(t, name, "x")
			found := false
			for _, listed := range listAll(t) {
				found = found || (listed.ID == object.ID && listed.Name == name)
			}
			if !found {
				t.Errorf("name %q did not survive a listing", name)
			}
		}
	})

	t.Run("listing pages through everything", func(t *testing.T) {
		var want []string
		for range ListPageSize*2 + 1 {
			want = append(want, put(t, "page.txt", "x").ID)
		}
		var got []string
		for _, object := range listAll(t) {
			got = append(got, object.ID)
			if slices.Contains(want, object.ID) && object.ModifiedAt.IsZero() {
				t.Errorf("object has no modified time: %+v", object)
			}
		}
		for _, id := range want {
			if !slices.Contains(got, id) {
				t.Errorf("listing is missing %s", id)
			}
		}
	})

	t.Run("deleting removes the object", func(t *testing.T) {
		object := put(t, "gone.txt", "x")
		if err := p.Delete(ctx, object.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := p.Get(ctx, object.ID, nil); !errors.Is(err, provider.ErrObjectNotFound) {
			t.Errorf("Get after delete: error = %v, want ErrObjectNotFound", err)
		}
		for _, listed := range listAll(t) {
			if listed.ID == object.ID {
				t.Error("deleted object is still listed")
			}
		}
	})

	t.Run("a missing object has no thumbnail", func(t *testing.T) {
		object := put(t, "gone.txt", "x")
		if err := p.Delete(ctx, object.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := p.Thumbnail(ctx, object.ID); !errors.Is(err, provider.ErrObjectNotFound) {
			t.Errorf("error = %v, want ErrObjectNotFound", err)
		}
	})

	t.Run("missing objects are reported as such", func(t *testing.T) {
		object := put(t, "once.txt", "x")
		if err := p.Delete(ctx, object.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := p.Delete(ctx, object.ID); !errors.Is(err, provider.ErrObjectNotFound) {
			t.Errorf("Delete twice: error = %v, want ErrObjectNotFound", err)
		}
	})

	t.Run("a large stream of unknown length arrives intact", func(t *testing.T) {
		// Not a Seeker or a Len-er, so the provider can't learn the size up front.
		body := io.LimitReader(pattern{}, largeBody)
		want := sha256.New()
		object, err := p.Put(ctx, provider.Upload{
			Name: "large.bin", ContentType: "application/octet-stream", Body: io.TeeReader(body, want),
		})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		t.Cleanup(func() { _ = p.Delete(ctx, object.ID) })
		if object.Size != largeBody {
			t.Errorf("Size = %d, want %d", object.Size, largeBody)
		}
		reader, err := p.Get(ctx, object.ID, nil)
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		defer func() { _ = reader.Close() }()
		got := sha256.New()
		if _, err := io.Copy(got, reader); err != nil {
			t.Fatalf("read body: %v", err)
		}
		if !bytes.Equal(got.Sum(nil), want.Sum(nil)) {
			t.Error("the body changed in transit")
		}
	})

	t.Run("a failing upload stores nothing", func(t *testing.T) {
		before := len(listAll(t))
		boom := errors.New("client went away")
		body := io.MultiReader(strings.NewReader("partial"), failing{boom})
		_, err := p.Put(ctx, provider.Upload{Name: "broken.txt", ContentType: "text/plain", Body: body})
		if err == nil {
			t.Fatal("Put succeeded, want error")
		}
		if after := len(listAll(t)); after != before {
			t.Errorf("objects = %d after a failed upload, want %d", after, before)
		}
	})
}

// pattern is an endless, non-repeating-looking stream.
type pattern struct{}

func (pattern) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(i*31 + i>>8)
	}
	return len(p), nil
}

type failing struct{ err error }

func (f failing) Read([]byte) (int, error) { return 0, f.err }

func runResumable(t *testing.T, p provider.Provider, read func(*testing.T, string) string) {
	t.Helper()
	ctx := context.Background()
	const align = provider.ChunkAlignment
	body := strings.Repeat("a", align) + strings.Repeat("b", align) + "tail"
	total := int64(len(body))

	begin := func(t *testing.T) string {
		t.Helper()
		session, err := p.BeginResumable(ctx, provider.Resumable{Name: "chunks.bin", ContentType: "text/plain", Size: total})
		if err != nil {
			t.Fatalf("BeginResumable: %v", err)
		}
		return session
	}
	send := func(t *testing.T, session string, from, to int64) provider.ResumeState {
		t.Helper()
		state, err := p.PutChunk(ctx, session, provider.Chunk{
			Offset: from,
			Length: to - from,
			Total:  total,
			Body:   strings.NewReader(body[from:to]),
		})
		if err != nil {
			t.Fatalf("PutChunk(%d-%d): %v", from, to, err)
		}
		return state
	}

	t.Run("chunks join up into one object", func(t *testing.T) {
		session := begin(t)
		if state := send(t, session, 0, align); state.Received != align || state.Object != nil {
			t.Fatalf("after one chunk: %+v", state)
		}
		send(t, session, align, 2*align)
		state := send(t, session, 2*align, total)
		if state.Object == nil {
			t.Fatalf("the last chunk finished nothing: %+v", state)
		}
		t.Cleanup(func() { _ = p.Delete(ctx, state.Object.ID) })
		if state.Object.Size != total || state.Object.Name != "chunks.bin" {
			t.Errorf("object = %+v", state.Object)
		}
		if got := read(t, state.Object.ID); got != body {
			t.Errorf("body differs: %d bytes, want %d", len(got), len(body))
		}
	})

	t.Run("status says how far an upload has got", func(t *testing.T) {
		session := begin(t)
		t.Cleanup(func() { _ = p.CancelResumable(ctx, session) })
		state, err := p.ResumableStatus(ctx, session, total)
		if err != nil {
			t.Fatalf("ResumableStatus: %v", err)
		}
		if state.Received != 0 {
			t.Errorf("before any chunk: %+v", state)
		}
		send(t, session, 0, align)
		if state, err = p.ResumableStatus(ctx, session, total); err != nil {
			t.Fatalf("ResumableStatus: %v", err)
		}
		if state.Received != align || state.Object != nil {
			t.Errorf("after one chunk: %+v", state)
		}
	})

	t.Run("status finds a finished upload", func(t *testing.T) {
		session := begin(t)
		send(t, session, 0, align)
		send(t, session, align, 2*align)
		finished := send(t, session, 2*align, total)
		t.Cleanup(func() { _ = p.Delete(ctx, finished.Object.ID) })
		state, err := p.ResumableStatus(ctx, session, total)
		if err != nil {
			t.Fatalf("ResumableStatus: %v", err)
		}
		if state.Object == nil || state.Object.ID != finished.Object.ID {
			t.Errorf("state = %+v, want the finished object", state)
		}
	})

	t.Run("a cancelled upload is gone, and cancelling again is fine", func(t *testing.T) {
		session := begin(t)
		send(t, session, 0, align)
		if err := p.CancelResumable(ctx, session); err != nil {
			t.Fatalf("CancelResumable: %v", err)
		}
		if _, err := p.ResumableStatus(ctx, session, total); !errors.Is(err, provider.ErrSessionExpired) {
			t.Errorf("error = %v, want ErrSessionExpired", err)
		}
		if err := p.CancelResumable(ctx, session); err != nil {
			t.Errorf("cancelling twice: %v", err)
		}
	})
}
