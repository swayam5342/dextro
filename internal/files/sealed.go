package files

import (
	"bufio"
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/swayam5342/tributary/internal/provider"
	"github.com/swayam5342/tributary/internal/secrets"
)

// An encrypted file is stored as a run of frames, each sealed on its own with the file's
// key, so any part can be read without the rest. A frame is exactly what Drive insists
// every chunk is a multiple of, which keeps three things simple: a piece of a file is a
// whole number of frames, whatever Drive keeps of a piece ends on a frame, and a range
// only has to fetch the frames it touches.
//
// The first frame begins with a header, in the clear, holding the file's key sealed by the
// server's keyring. Tributary reads the key from its own records, and the header is there
// so that a sync can bring encrypted files back after losing the database.
//
//	frame 0: header | sealed(first bytes)    frame n: sealed(next bytes)
const (
	sealedFrame = provider.ChunkAlignment
	sealedTag   = 16
	// sealedPayload is what a frame holds.
	sealedPayload     = sealedFrame - sealedTag
	sealedHeaderBytes = 512
	// sealedPieceFrames is how many frames a piece carries: 8 MiB, like a plain upload.
	sealedPieceFrames = 32
	fileKeyBytes      = 32
	// sealedContentType is what the account is told, which is also how a sync recognises one.
	sealedContentType = "application/vnd.tributary.encrypted"
	// fileKeyContext binds a sealed key to being a file key. It can't name the file, because
	// a sync has to open it before any record exists.
	fileKeyContext = "file-key"
)

var sealedMagic = []byte("TRIBENC1")

// ErrFileUnreadable means an encrypted file couldn't be decrypted: it's been changed or
// damaged, or the key that sealed it is gone.
var ErrFileUnreadable = errors.New("encrypted file can't be read")

// storedSize is how many bytes a file of this size takes up in its account. The stream is
// the header and then the file, cut into frames, and every frame adds a tag.
func storedSize(plain int64) int64 {
	stream := plain + sealedHeaderBytes
	frames := (stream + sealedPayload - 1) / sealedPayload
	return stream + frames*sealedTag
}

// plainSize is the opposite of storedSize.
func plainSize(stored int64) int64 {
	frames := (stored + sealedFrame - 1) / sealedFrame
	return stored - frames*sealedTag - sealedHeaderBytes
}

// plainOffset says how much of a file an account has, given how much of the object it
// kept. Part of a frame is no use to anyone.
func plainOffset(stored int64) int64 {
	frames := stored / sealedFrame
	if frames == 0 {
		return 0
	}
	return frames*sealedPayload - sealedHeaderBytes
}

// storedOffset is where a piece starts in the object. A piece always starts on a frame.
func storedOffset(plain int64) int64 {
	if plain == 0 {
		return 0
	}
	return (plain + sealedHeaderBytes) / sealedPayload * sealedFrame
}

// nextSealedPiece says how long the piece starting here has to be. It's a set length,
// because anything else wouldn't be a whole number of frames.
func nextSealedPiece(offset, total int64) int64 {
	frame := (offset + sealedHeaderBytes) / sealedPayload
	end := (frame+sealedPieceFrames)*sealedPayload - sealedHeaderBytes
	return min(end, total) - offset
}

// encodeSealedHeader writes the first bytes of an object, in a fixed amount of space.
func encodeSealedHeader(key secrets.Sealed, contentType string) ([]byte, error) {
	if len(key.KeyID) > 255 || len(contentType) > 255 {
		return nil, errors.New("sealed header: value too long")
	}
	header := make([]byte, 0, sealedHeaderBytes)
	header = append(header, sealedMagic...)
	header = append(header, byte(len(key.KeyID))) //nolint:gosec // Checked above.
	header = append(header, key.KeyID...)
	header = binary.BigEndian.AppendUint16(header, uint16(len(key.Ciphertext))) //nolint:gosec // Checked just below.
	header = append(header, key.Ciphertext...)
	header = append(header, byte(len(contentType))) //nolint:gosec // Checked above.
	header = append(header, contentType...)
	if len(header) > sealedHeaderBytes {
		return nil, errors.New("sealed header: too long")
	}
	return header[:sealedHeaderBytes], nil
}

// decodeSealedHeader reads what encodeSealedHeader wrote.
func decodeSealedHeader(header []byte) (secrets.Sealed, string, error) {
	invalid := errors.New("not a sealed header")
	if len(header) < sealedHeaderBytes || !bytes.HasPrefix(header, sealedMagic) {
		return secrets.Sealed{}, "", invalid
	}
	rest := header[len(sealedMagic):]
	take := func(n int) ([]byte, bool) {
		if n > len(rest) {
			return nil, false
		}
		part := rest[:n]
		rest = rest[n:]
		return part, true
	}
	size, ok := take(1)
	if !ok {
		return secrets.Sealed{}, "", invalid
	}
	keyID, ok := take(int(size[0]))
	if !ok {
		return secrets.Sealed{}, "", invalid
	}
	length, ok := take(2)
	if !ok {
		return secrets.Sealed{}, "", invalid
	}
	sealedKey, ok := take(int(binary.BigEndian.Uint16(length)))
	if !ok {
		return secrets.Sealed{}, "", invalid
	}
	size, ok = take(1)
	if !ok {
		return secrets.Sealed{}, "", invalid
	}
	contentType, ok := take(int(size[0]))
	if !ok {
		return secrets.Sealed{}, "", invalid
	}
	return secrets.Sealed{KeyID: string(keyID), Ciphertext: bytes.Clone(sealedKey)}, string(contentType), nil
}

// newFileKey makes the key for one file.
func newFileKey() []byte {
	key := make([]byte, fileKeyBytes)
	_, _ = rand.Read(key) // crypto/rand never returns an error.
	return key
}

// fileCipher seals and opens the frames of one file.
type fileCipher struct {
	aead cipher.AEAD
}

func newFileCipher(key []byte) (*fileCipher, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("file cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("file cipher: %w", err)
	}
	return &fileCipher{aead: aead}, nil
}

// nonce comes from the frame's place in the file and whether it's the last. Every file has
// its own key, so a place is only ever used for the same bytes. It's also what makes a
// frame moved to another place, or an end cut off, fail to open.
func (c *fileCipher) nonce(frame int64, last bool) []byte {
	nonce := make([]byte, c.aead.NonceSize())
	if last {
		nonce[0] = 1
	}
	binary.BigEndian.PutUint64(nonce[4:], uint64(frame)) //nolint:gosec // A frame number is never negative.
	return nonce
}

// sealReader turns a file's bytes into frames as they're read, one frame at a time.
type sealReader struct {
	cipher *fileCipher
	source *bufio.Reader
	header []byte
	frame  int64
	// read is how far into the file this has got. total is the file's size, or -1 if it's
	// only known when the source ends.
	read       int64
	total      int64
	plain      []byte
	out        []byte
	frameBytes []byte
	done       bool
}

// seal encrypts a file, or the piece of it starting at offset, whose source ends where the
// piece does.
func (c *fileCipher) seal(source io.Reader, header []byte, offset, total int64) io.Reader {
	return &sealReader{
		cipher:     c,
		source:     bufio.NewReader(source),
		header:     header,
		frame:      storedOffset(offset) / sealedFrame,
		read:       offset,
		total:      total,
		plain:      make([]byte, sealedPayload),
		frameBytes: make([]byte, 0, sealedFrame),
	}
}

func (r *sealReader) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.done {
			return 0, io.EOF
		}
		if err := r.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	return n, nil
}

func (r *sealReader) next() error {
	room := int64(sealedPayload)
	if r.frame == 0 {
		room -= sealedHeaderBytes
	}
	if r.total >= 0 {
		room = min(room, r.total-r.read)
	}
	n, err := io.ReadFull(r.source, r.plain[:room])
	ended := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
	if err != nil && !ended {
		return err
	}
	if r.total >= 0 {
		if n == 0 && ended {
			// The end of a piece, which falls between frames.
			r.done = true
			return nil
		}
		if int64(n) < room {
			return io.ErrUnexpectedEOF
		}
	}
	last, err := r.isLast(n, ended)
	if err != nil {
		return err
	}
	var extra []byte
	out := r.frameBytes[:0]
	if r.frame == 0 {
		out = append(out, r.header...)
		extra = r.header
	}
	// See nonce: it comes from the frame's place, under a key no other file has.
	r.out = r.cipher.aead.Seal(out, r.cipher.nonce(r.frame, last), r.plain[:n], extra) //nolint:gosec // Not a fixed nonce.
	r.frame++
	r.read += int64(n)
	r.done = last
	return nil
}

// isLast decides whether this frame ends the file. Without a size, it has to look ahead.
func (r *sealReader) isLast(n int, ended bool) (bool, error) {
	if r.total >= 0 {
		return r.read+int64(n) == r.total, nil
	}
	if ended {
		return true, nil
	}
	_, err := r.source.Peek(1)
	if errors.Is(err, io.EOF) {
		return true, nil
	}
	return false, err
}

// sealedRange says which part of an object holds a range of a file, and how to read it.
type sealedRange struct {
	stored provider.ByteRange
	// frame is the first frame fetched and lastFrame the file's last, whichever is fetched.
	frame     int64
	lastFrame int64
	storedLen int64
	// skip is how much of the first frame comes before the range, and length how much to return.
	skip   int64
	length int64
}

// sealedSpan works out what has to be fetched. A nil range means the whole file.
func sealedSpan(total int64, rng *provider.ByteRange) sealedRange {
	stored := storedSize(total)
	lastFrame := (stored - 1) / sealedFrame
	if rng == nil {
		return sealedRange{
			stored:    provider.ByteRange{Start: 0, End: stored - 1},
			lastFrame: lastFrame,
			storedLen: stored,
			length:    total,
		}
	}
	first := (rng.Start + sealedHeaderBytes) / sealedPayload
	last := (rng.End + sealedHeaderBytes) / sealedPayload
	skip := rng.Start + sealedHeaderBytes - first*sealedPayload
	if first == 0 {
		skip = rng.Start
	}
	return sealedRange{
		stored:    provider.ByteRange{Start: first * sealedFrame, End: min((last+1)*sealedFrame, stored) - 1},
		frame:     first,
		lastFrame: lastFrame,
		storedLen: stored,
		skip:      skip,
		length:    rng.End - rng.Start + 1,
	}
}

// openReader decrypts frames as they're read, and fails rather than return anything that
// doesn't prove itself.
type openReader struct {
	cipher *fileCipher
	source io.ReadCloser
	span   sealedRange
	frame  int64
	left   int64
	skip   int64
	sealed []byte
	plain  []byte
	out    []byte
	// started is set once a frame has been opened, because an empty file has one to check.
	started bool
}

// open decrypts a span of an object. source starts where the span says to.
func (c *fileCipher) open(source io.ReadCloser, span sealedRange) io.ReadCloser {
	return &openReader{
		cipher: c,
		source: source,
		span:   span,
		frame:  span.frame,
		left:   span.length,
		skip:   span.skip,
		sealed: make([]byte, sealedFrame),
		plain:  make([]byte, 0, sealedPayload),
	}
}

func (r *openReader) Close() error {
	return r.source.Close()
}

func (r *openReader) Read(p []byte) (int, error) {
	for len(r.out) == 0 {
		if r.left == 0 && r.started {
			return 0, io.EOF
		}
		if err := r.next(); err != nil {
			return 0, err
		}
	}
	n := copy(p, r.out)
	r.out = r.out[n:]
	r.left -= int64(n)
	return n, nil
}

func (r *openReader) next() error {
	size := int64(sealedFrame)
	last := r.frame == r.span.lastFrame
	if last {
		size = r.span.storedLen - r.frame*sealedFrame
	}
	frame := r.sealed[:size]
	if _, err := io.ReadFull(r.source, frame); err != nil {
		return err
	}
	var extra []byte
	if r.frame == 0 {
		extra = frame[:sealedHeaderBytes]
		frame = frame[sealedHeaderBytes:]
	}
	plain, err := r.cipher.aead.Open(r.plain[:0], r.cipher.nonce(r.frame, last), frame, extra)
	if err != nil {
		return ErrFileUnreadable
	}
	r.started = true
	r.frame++
	plain = plain[min(r.skip, int64(len(plain))):]
	r.skip = 0
	r.out = plain[:min(r.left, int64(len(plain)))]
	return nil
}
