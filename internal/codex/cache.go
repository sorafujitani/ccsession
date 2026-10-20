package codex

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"time"

	"github.com/sorafujitani/ccsession/internal/filescan"
	"github.com/sorafujitani/ccsession/internal/session"
)

const codexCacheVersion = 1

// A record is reused only through two checks. An identical file stamp
// (device, inode, size, mtime, ctime), recorded only once those timestamps
// had settled, skips reading. Otherwise the digest of the previously scanned
// prefix must match before the scan resumes at its offset; any other change,
// including a same-size edit or a replacement, rescans the whole file.
// Without stamps, as on Windows, every scan re-hashes the prefix, which still
// skips JSON work for unchanged bytes.
type codexCacheRecord struct {
	Stamp  string     `json:"stamp,omitempty"`
	Offset int64      `json:"offset"`
	Prefix string     `json:"prefix"`         // prefixDigest of bytes [0, Offset).
	State  listState  `json:"state"`          // After the lines in [0, Offset).
	Tail   *listState `json:"tail,omitempty"` // Also after an unterminated last line.
}

// final is the state the record lists the file with.
func (r *codexCacheRecord) final() listState {
	if r.Tail != nil {
		return *r.Tail
	}
	return r.State
}

func (r *codexCacheRecord) equal(o codexCacheRecord) bool {
	return r.Stamp == o.Stamp && r.Offset == o.Offset && r.Prefix == o.Prefix &&
		r.State.equal(o.State) && (r.Tail == nil) == (o.Tail == nil) &&
		(r.Tail == nil || r.Tail.equal(*o.Tail))
}

type codexCache struct {
	Version int                         `json:"version"`
	Home    string                      `json:"home"`
	Files   map[string]codexCacheRecord `json:"files"`
}

// Tests replace these to observe per-file scans and to control which file
// timestamps count as settled.
var (
	scanSessionFile = scanCachedFile
	now             = time.Now
)

// scanCachedFile returns the list Session for path and the record that
// describes it. prev may be nil.
func scanCachedFile(path string, prev *codexCacheRecord) (*session.Session, codexCacheRecord, error) {
	// An unchanged file costs one stat; opening it costs several syscalls.
	if prev != nil && prev.Stamp != "" {
		if info, err := os.Stat(path); err == nil && filescan.Stamp(info) == prev.Stamp {
			sess, err := prev.final().finish(path, info)
			return sess, *prev, err
		}
	}
	f, info, err := openTranscript(path)
	if err != nil {
		return nil, codexCacheRecord{}, err
	}
	defer f.Close()
	stamp := ""
	if filescan.Settled(info, now()) {
		stamp = filescan.Stamp(info)
	}
	h, st, offset := newPrefixHasher(), listState{}, int64(0)
	if prev != nil && prev.Offset > 0 && prev.Offset <= info.Size() {
		// A read error, including a file that shrank since the stat, fails
		// the check and rescans.
		if verified, err := prefixDigest(f, prev.Offset); err == nil && verified.Digest() == prev.Prefix {
			h, st, offset = verified, prev.State, prev.Offset
		}
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, codexCacheRecord{}, err
	}
	complete, completeOffset, final, err := scanList(f, h, st, offset)
	if err != nil {
		return nil, codexCacheRecord{}, err
	}
	rec := codexCacheRecord{Stamp: stamp, Offset: completeOffset, Prefix: h.Digest(), State: complete}
	if !final.equal(complete) {
		rec.Tail = &final
	}
	sess, err := final.finish(path, info)
	return sess, rec, err
}

// listFile is the uncached list scan, for single-file lookups.
func listFile(path string) (*session.Session, error) {
	f, info, err := openTranscript(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	_, _, final, err := scanList(f, nil, listState{}, 0)
	if err != nil {
		return nil, err
	}
	return final.finish(path, info)
}

func openTranscript(path string) (*os.File, os.FileInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, session.ErrSessionFileMissing
		}
		return nil, nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, err
	}
	return f, info, nil
}

func codexCachePath(home string) (string, string) {
	return session.StoreCachePath("codex", home)
}

func readCodexCache(path, home string) codexCache {
	var cache codexCache
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &cache) == nil && cache.Version == codexCacheVersion && cache.Home == home && cache.Files != nil {
			return cache
		}
	}
	return codexCache{Version: codexCacheVersion, Home: home, Files: map[string]codexCacheRecord{}}
}

func writeCodexCache(path string, cache codexCache) error {
	return session.WriteJSONAtomic(path, cache)
}
