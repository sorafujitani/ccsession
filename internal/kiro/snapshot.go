package kiro

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"runtime"
)

// A snapshot is a fast-path hint, not a per-row revision. Changed or uncertain
// snapshots fall back to content hashes, including same-timestamp row updates.
func classicDatabaseSnapshot(path string) string {
	// These platforms expose inode/device and nanosecond ctime in FileInfo.Sys.
	// Other platforms retain the content-hash fallback rather than trusting mtime.
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return ""
	}
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
		hash.Write(stamp)
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

func classicFileSnapshot(file *os.File, wal bool) ([]byte, []byte, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	raw, err := json.Marshal(info.Sys())
	if err != nil {
		return nil, nil, err
	}
	var stat map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stat); err != nil {
		return nil, nil, err
	}
	// Reading the DB may change atime, which must not invalidate the cache.
	delete(stat, "Atim")
	delete(stat, "Atimespec")
	stamp, err := json.Marshal(stat)
	if err != nil {
		return nil, nil, err
	}
	size := 100 // SQLite header, including the change counter.
	if wal {
		size = 32 // WAL generation salts and checkpoint sequence.
	}
	header := make([]byte, size)
	n, err := file.ReadAt(header, 0)
	if err != nil && err != io.EOF {
		return nil, nil, err
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
				return nil, nil, err
			}
			header = append(header, frame...)
		}
	}
	return stamp, header, nil
}
