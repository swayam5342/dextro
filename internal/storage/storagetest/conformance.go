// Package storagetest holds the tests every provider must pass, and a fake
// provider for testing the code above that interface.
package storagetest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/swayam5342/dextro/internal/storage"
)

// ListPageSize is the page size providers must be configured with for Run, so
// that a handful of objects spans several pages.
const ListPageSize = 2

// largeBody is bigger than any adapter's upload chunk, so it takes several.
const largeBody = 40 << 20

// Run checks that a provider behaves as the storage.Provider contract says. The
// provider must start empty and page listings at ListPageSize.
func Run(t *testing.T, provider storage.Provider) {
	t.Helper()
	ctx := context.Background()

	put := func(t *testing.T, name, body string) storage.Object {
		t.Helper()
		object, err := provider.Put(ctx, storage.Upload{Name: name, ContentType: "text/plain", Body: strings.NewReader(body)})
		if err != nil {
			t.Fatalf("Put(%q): %v", name, err)
		}
		t.Cleanup(func() { _ = provider.Delete(ctx, object.ID) })
		return object
	}
	read := func(t *testing.T, id string) string {
		t.Helper()
		reader, err := provider.Get(ctx, id)
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
	listAll := func(t *testing.T) []storage.Object {
		t.Helper()
		var all []storage.Object
		token := ""
		for range 100 {
			page, err := provider.List(ctx, token)
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

	t.Run("check succeeds", func(t *testing.T) {
		if err := provider.Check(ctx); err != nil {
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
		if err := provider.Delete(ctx, object.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if _, err := provider.Get(ctx, object.ID); !errors.Is(err, storage.ErrObjectNotFound) {
			t.Errorf("Get after delete: error = %v, want ErrObjectNotFound", err)
		}
		for _, listed := range listAll(t) {
			if listed.ID == object.ID {
				t.Error("deleted object is still listed")
			}
		}
	})

	t.Run("missing objects are reported as such", func(t *testing.T) {
		object := put(t, "once.txt", "x")
		if err := provider.Delete(ctx, object.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		if err := provider.Delete(ctx, object.ID); !errors.Is(err, storage.ErrObjectNotFound) {
			t.Errorf("Delete twice: error = %v, want ErrObjectNotFound", err)
		}
	})

	t.Run("a large stream of unknown length arrives intact", func(t *testing.T) {
		// Not a Seeker or a Len-er, so the provider can't learn the size up front.
		body := io.LimitReader(pattern{}, largeBody)
		want := sha256.New()
		object, err := provider.Put(ctx, storage.Upload{
			Name: "large.bin", ContentType: "application/octet-stream", Body: io.TeeReader(body, want),
		})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		t.Cleanup(func() { _ = provider.Delete(ctx, object.ID) })
		if object.Size != largeBody {
			t.Errorf("Size = %d, want %d", object.Size, largeBody)
		}
		reader, err := provider.Get(ctx, object.ID)
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
		_, err := provider.Put(ctx, storage.Upload{Name: "broken.txt", ContentType: "text/plain", Body: body})
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
