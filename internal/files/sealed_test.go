package files

import (
	"bytes"
	"crypto/rand"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/swayam5342/tributary/internal/provider"
	"github.com/swayam5342/tributary/internal/secrets"
)

func testCipher(t *testing.T) *fileCipher {
	t.Helper()
	c, err := newFileCipher(bytes.Repeat([]byte{9}, fileKeyBytes))
	if err != nil {
		t.Fatalf("newFileCipher: %v", err)
	}
	return c
}

func testHeader(t *testing.T) []byte {
	t.Helper()
	header, err := encodeSealedHeader(secrets.Sealed{KeyID: "k1", Ciphertext: bytes.Repeat([]byte{7}, 60)}, "text/plain")
	if err != nil {
		t.Fatalf("encodeSealedHeader: %v", err)
	}
	return header
}

func randomBytes(t *testing.T, n int64) []byte {
	t.Helper()
	body := make([]byte, n)
	if _, err := rand.Read(body); err != nil {
		t.Fatalf("rand: %v", err)
	}
	return body
}

// sealWhole encrypts a body the way one streamed upload does.
func sealWhole(t *testing.T, c *fileCipher, header, plain []byte) []byte {
	t.Helper()
	sealed, err := io.ReadAll(c.seal(bytes.NewReader(plain), header, 0, -1))
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	return sealed
}

// sealInPieces encrypts a body the way a chunked upload does, piece by piece.
func sealInPieces(t *testing.T, c *fileCipher, header, plain []byte) []byte {
	t.Helper()
	total := int64(len(plain))
	var sealed bytes.Buffer
	for offset := int64(0); offset < total; {
		length := nextSealedPiece(offset, total)
		part, err := io.ReadAll(c.seal(bytes.NewReader(plain[offset:offset+length]), header, offset, total))
		if err != nil {
			t.Fatalf("seal piece at %d: %v", offset, err)
		}
		if offset+length < total && len(part)%provider.ChunkAlignment != 0 {
			t.Fatalf("piece at %d is %d bytes, not a multiple of the alignment", offset, len(part))
		}
		if int64(sealed.Len()) != storedOffset(offset) {
			t.Fatalf("piece at %d starts at %d, want %d", offset, sealed.Len(), storedOffset(offset))
		}
		sealed.Write(part)
		offset += length
	}
	return sealed.Bytes()
}

// open decrypts a range of a sealed object.
func openRange(t *testing.T, c *fileCipher, sealed []byte, total int64, rng *provider.ByteRange) ([]byte, error) {
	t.Helper()
	span := sealedSpan(total, rng)
	source := io.NopCloser(bytes.NewReader(sealed[span.stored.Start : span.stored.End+1]))
	return io.ReadAll(c.open(source, span))
}

// sizes sit on either side of every boundary the format has.
func sizes() []int64 {
	first := int64(sealedPayload - sealedHeaderBytes)
	return []int64{
		1, 2, first - 1, first, first + 1,
		first + sealedPayload - 1, first + sealedPayload, first + sealedPayload + 1,
		first + 5*sealedPayload + 7,
		nextSealedPiece(0, 1<<40) - 1, nextSealedPiece(0, 1<<40), nextSealedPiece(0, 1<<40) + 1,
	}
}

func TestSealedRoundTrips(t *testing.T) {
	c, header := testCipher(t), testHeader(t)
	for _, size := range sizes() {
		plain := randomBytes(t, size)
		whole := sealWhole(t, c, header, plain)
		if int64(len(whole)) != storedSize(size) {
			t.Errorf("size %d: stored %d, want %d", size, len(whole), storedSize(size))
		}
		if plainSize(int64(len(whole))) != size {
			t.Errorf("size %d: plainSize gave %d", size, plainSize(int64(len(whole))))
		}
		if pieces := sealInPieces(t, c, header, plain); !bytes.Equal(pieces, whole) {
			t.Errorf("size %d: pieces and a stream made different objects", size)
		}
		got, err := openRange(t, c, whole, size, nil)
		if err != nil || !bytes.Equal(got, plain) {
			t.Errorf("size %d: err %v, %d bytes back", size, err, len(got))
		}
	}
}

func TestSealedEmptyFile(t *testing.T) {
	c, header := testCipher(t), testHeader(t)
	whole := sealWhole(t, c, header, nil)
	if int64(len(whole)) != storedSize(0) || plainSize(int64(len(whole))) != 0 {
		t.Fatalf("stored %d bytes", len(whole))
	}
	got, err := openRange(t, c, whole, 0, nil)
	if err != nil || len(got) != 0 {
		t.Errorf("err %v, %d bytes", err, len(got))
	}
}

func TestSealedRanges(t *testing.T) {
	c, header := testCipher(t), testHeader(t)
	size := int64(4*sealedPayload + 1000)
	plain := randomBytes(t, size)
	whole := sealWhole(t, c, header, plain)
	first := int64(sealedPayload - sealedHeaderBytes)
	for _, rng := range []provider.ByteRange{
		{Start: 0, End: 0},
		{Start: 0, End: 99},
		{Start: 5, End: first - 1},
		{Start: first - 1, End: first},
		{Start: first, End: first},
		{Start: first + 10, End: first + sealedPayload + 10},
		{Start: size - 1, End: size - 1},
		{Start: 100, End: size - 1},
	} {
		got, err := openRange(t, c, whole, size, &rng)
		if err != nil {
			t.Errorf("%+v: %v", rng, err)
			continue
		}
		if !bytes.Equal(got, plain[rng.Start:rng.End+1]) {
			t.Errorf("%+v: wrong bytes back", rng)
		}
	}
}

func TestSealedPlainOffsetsMatchFrames(t *testing.T) {
	if plainOffset(0) != 0 || storedOffset(0) != 0 {
		t.Fatal("the start isn't the start")
	}
	for frames := int64(1); frames < 70; frames++ {
		plain := plainOffset(frames * sealedFrame)
		if storedOffset(plain) != frames*sealedFrame {
			t.Errorf("%d frames: %d back to %d", frames, plain, storedOffset(plain))
		}
	}
	// Less than a frame hasn't kept anything usable.
	if got := plainOffset(sealedFrame - 1); got != 0 {
		t.Errorf("part of a frame gave %d", got)
	}
}

func TestSealedRefusesTampering(t *testing.T) {
	c, header := testCipher(t), testHeader(t)
	size := int64(3 * sealedPayload)
	plain := randomBytes(t, size)
	whole := sealWhole(t, c, header, plain)

	flip := func(at int) []byte {
		changed := bytes.Clone(whole)
		changed[at] ^= 1
		return changed
	}
	swapped := bytes.Clone(whole)
	copy(swapped[sealedFrame:2*sealedFrame], whole[2*sealedFrame:3*sealedFrame])
	copy(swapped[2*sealedFrame:3*sealedFrame], whole[sealedFrame:2*sealedFrame])
	other, err := newFileCipher(bytes.Repeat([]byte{8}, fileKeyBytes))
	if err != nil {
		t.Fatalf("newFileCipher: %v", err)
	}

	tests := []struct {
		name   string
		cipher *fileCipher
		sealed []byte
	}{
		{"a changed byte", c, flip(sealedFrame + 100)},
		{"a changed header", c, flip(20)},
		{"frames swapped", c, swapped},
		{"the wrong key", other, whole},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := openRange(t, tt.cipher, tt.sealed, size, nil); !errors.Is(err, ErrFileUnreadable) {
				t.Errorf("err = %v", err)
			}
		})
	}
}

// Cutting frames off the end has to fail, or a shortened file would look whole.
func TestSealedRefusesTruncation(t *testing.T) {
	c, header := testCipher(t), testHeader(t)
	plain := randomBytes(t, 3*sealedPayload)
	whole := sealWhole(t, c, header, plain)
	cut := whole[:2*sealedFrame]
	if _, err := openRange(t, c, cut, plainSize(int64(len(cut))), nil); !errors.Is(err, ErrFileUnreadable) {
		t.Errorf("err = %v", err)
	}
}

func TestSealedShortPieceFails(t *testing.T) {
	c, header := testCipher(t), testHeader(t)
	total := int64(1 << 20)
	if _, err := io.ReadAll(c.seal(strings.NewReader("too short"), header, 0, total)); err == nil {
		t.Error("a short piece was accepted")
	}
}

func TestSealedHeaderRoundTrips(t *testing.T) {
	key := secrets.Sealed{KeyID: "k2", Ciphertext: []byte("sealed key")}
	header, err := encodeSealedHeader(key, "image/png")
	if err != nil {
		t.Fatalf("encodeSealedHeader: %v", err)
	}
	if len(header) != sealedHeaderBytes {
		t.Fatalf("header is %d bytes", len(header))
	}
	got, contentType, err := decodeSealedHeader(header)
	if err != nil {
		t.Fatalf("decodeSealedHeader: %v", err)
	}
	if got.KeyID != key.KeyID || !bytes.Equal(got.Ciphertext, key.Ciphertext) || contentType != "image/png" {
		t.Errorf("got %+v, %q", got, contentType)
	}
	if _, _, err := decodeSealedHeader(make([]byte, sealedHeaderBytes)); err == nil {
		t.Error("a header with no magic was accepted")
	}
	if _, err := encodeSealedHeader(secrets.Sealed{KeyID: strings.Repeat("k", 300)}, "a/b"); err == nil {
		t.Error("a key ID too long to fit was accepted")
	}
}
