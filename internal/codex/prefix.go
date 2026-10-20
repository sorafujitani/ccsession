package codex

import (
	"crypto/sha256"
	"encoding"
	"encoding/hex"
	"errors"
	"hash"
	"io"
	"runtime"
	"slices"
	"sync"
)

// prefixBlock is the unit prefixDigest hashes in parallel.
const prefixBlock = 4 << 20

// prefixHasher computes a prefix digest, the SHA-256 of the SHA-256s of each
// prefixBlock of a file's first bytes, as those bytes are written to it.
type prefixHasher struct {
	sums  []byte    // SHA-256s of complete blocks.
	block hash.Hash // The block being written.
	n     int64     // Bytes written.
	saved struct {
		sums  int
		block []byte
		n     int64
	}
}

func newPrefixHasher() *prefixHasher {
	return &prefixHasher{block: sha256.New()}
}

func (h *prefixHasher) Write(p []byte) (int, error) {
	written := len(p)
	for len(p) > 0 {
		k := min(int64(len(p)), prefixBlock-h.n%prefixBlock)
		h.block.Write(p[:k])
		h.n += k
		p = p[k:]
		if h.n%prefixBlock == 0 {
			h.sums = h.block.Sum(h.sums)
			h.block.Reset()
		}
	}
	return written, nil
}

// Digest returns the digest of everything written so far.
func (h *prefixHasher) Digest() string {
	sums := h.sums
	if h.n%prefixBlock != 0 {
		sums = h.block.Sum(slices.Clip(sums))
	}
	sum := sha256.Sum256(sums)
	return hex.EncodeToString(sum[:])
}

// save records the current position, for restore to drop later writes.
func (h *prefixHasher) save() {
	block, _ := h.block.(encoding.BinaryMarshaler).MarshalBinary() // sha256 always can.
	h.saved.sums, h.saved.block, h.saved.n = len(h.sums), block, h.n
}

// restore returns to the position recorded by the last save, which must
// precede it.
func (h *prefixHasher) restore() {
	h.sums, h.n = h.sums[:h.saved.sums], h.saved.n
	_ = h.block.(encoding.BinaryUnmarshaler).UnmarshalBinary(h.saved.block)
}

// prefixDigest hashes the first n bytes of f, complete blocks in parallel, and
// returns a hasher positioned at n, so verifying a large transcript before
// resuming costs little more than reading it once.
func prefixDigest(f io.ReaderAt, n int64) (*prefixHasher, error) {
	full := int(n / prefixBlock)
	h := newPrefixHasher()
	h.sums = make([]byte, full*sha256.Size)
	errs := make([]error, full+1)
	sem := make(chan struct{}, runtime.GOMAXPROCS(0))
	var wg sync.WaitGroup
	for i := range full {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			block := sha256.New()
			errs[i] = copyExactly(block, f, int64(i)*prefixBlock, prefixBlock)
			block.Sum(h.sums[i*sha256.Size : i*sha256.Size])
		}()
	}
	h.n = int64(full) * prefixBlock
	errs[full] = copyExactly(h.block, f, h.n, n-h.n)
	h.n = n
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return h, nil
}

// copyExactly writes f's size bytes at off to w; fewer means the file shrank.
func copyExactly(w io.Writer, f io.ReaderAt, off, size int64) error {
	copied, err := io.Copy(w, io.NewSectionReader(f, off, size))
	if err == nil && copied != size {
		err = io.ErrUnexpectedEOF
	}
	return err
}
