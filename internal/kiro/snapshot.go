package kiro

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"io"
	"os"

	"github.com/sorafujitani/ccsession/internal/filescan"
)

// A snapshot is a fast-path hint, not a per-row revision. Changed or uncertain
// snapshots fall back to content hashes, including same-timestamp row updates.
// Platforms without file stamps return "" and keep the content-hash fallback.
// Every commit a reader can observe changes the SQLite or WAL headers hashed
// here, so recent writes need no timestamp margin. (An EXCLUSIVE locking-mode
// writer may skip header updates, but holds its lock across commits.)
func classicDatabaseSnapshot(path string) string {
	hash := sha256.New()
	for _, wal := range []bool{false, true} {
		name := path
		if wal {
			name += "-wal"
		}
		file, err := os.Open(name)
		if err != nil {
			if wal && os.IsNotExist(err) {
				hash.Write([]byte("no-wal"))
				continue
			}
			return ""
		}
		stamp, header, err := classicFileSnapshot(file, wal)
		file.Close()
		if err != nil {
			return ""
		}
		hash.Write([]byte(stamp))
		hash.Write(header)
		if wal {
			// A commit frame can reach the WAL before readers see its publication.
			// Include both WAL-index header copies, but not mutable reader locks.
			shared, err := os.Open(path + "-shm")
			if err != nil {
				return ""
			}
			index := make([]byte, 96)
			_, err = io.ReadFull(shared, index)
			shared.Close()
			if err != nil {
				return ""
			}
			hash.Write(index)
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func classicFileSnapshot(file *os.File, wal bool) (string, []byte, error) {
	info, err := file.Stat()
	if err != nil {
		return "", nil, err
	}
	stamp := filescan.Stamp(info)
	if stamp == "" {
		return "", nil, errors.New("kiro: file stamps unsupported")
	}
	size := 100 // SQLite header, including the change counter.
	if wal {
		size = 32 // WAL generation salts and checkpoint sequence.
	}
	header := make([]byte, size)
	n, err := file.ReadAt(header, 0)
	if err != nil && err != io.EOF {
		return "", nil, err
	}
	header = header[:n]
	if wal && n == 32 {
		pageSize := int64(binary.BigEndian.Uint32(header[8:12]))
		frameSize := pageSize + 24
		if pageSize >= 512 && pageSize <= 65536 && info.Size() >= 32+frameSize {
			// Include the last complete frame's commit marker and rolling checksum,
			// without reading any page payload.
			offset := 32 + ((info.Size()-32)/frameSize-1)*frameSize
			frame := make([]byte, 24)
			if _, err := file.ReadAt(frame, offset); err != nil {
				return "", nil, err
			}
			header = append(header, frame...)
		}
	}
	return stamp, header, nil
}
