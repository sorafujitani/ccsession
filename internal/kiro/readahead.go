package kiro

import (
	"io"
	"os"

	"github.com/ncruces/go-sqlite3/util/vfsutil"
	"github.com/ncruces/go-sqlite3/vfs"
)

// classicVFS reads ahead on sequential page reads of a read-only main
// database. SQLite reads overflow pages one at a time, and this wasm build
// has no memory-mapped I/O for the main database, so a scan of large
// conversation bodies otherwise costs one pread per 4 KiB page. On a 2.4 GB
// store that was 600k preads; parallel scans spent most of their time
// contending for them in the kernel.
//
// Only connections that ask for this VFS by name use it; registering it at
// init does not change other databases.
const classicVFS = "ccsession-kiro-readahead"

const (
	readAheadMin = 64 << 10
	readAheadMax = 1 << 20
)

// walFormatVersion in header bytes 18 (write) or 19 (read) marks a WAL
// database; see https://sqlite.org/fileformat2.html#file_format_version_numbers.
const walFormatVersion = 2

func init() {
	vfs.Register(classicVFS, readAheadVFS{vfs.Find("")})
}

type readAheadVFS struct{ vfs.VFS }

func (v readAheadVFS) Open(name string, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	file, flags, err := vfsutil.WrapOpen(v.VFS, name, flags)
	return wrapReadAhead(file, name, flags, err)
}

func (v readAheadVFS) OpenFilename(name *vfs.Filename, flags vfs.OpenFlag) (vfs.File, vfs.OpenFlag, error) {
	file, flags, err := vfsutil.WrapOpenFilename(v.VFS, name, flags)
	path := ""
	if name != nil {
		path = name.String()
	}
	return wrapReadAhead(file, path, flags, err)
}

func wrapReadAhead(file vfs.File, path string, flags vfs.OpenFlag, err error) (vfs.File, vfs.OpenFlag, error) {
	if err != nil || flags&vfs.OPEN_MAIN_DB == 0 || flags&vfs.OPEN_READONLY == 0 || path == "" {
		return file, flags, err
	}
	// A WAL database can change under a long-lived lock through checkpoints,
	// so only rollback-journal databases are buffered. A -wal file skips the
	// wrapper; ReadAt also checks the header, for WAL databases whose -wal
	// file is absent and for a later switch to WAL.
	if _, statErr := os.Lstat(path + "-wal"); statErr == nil {
		return file, flags, nil
	}
	return &readAheadFile{File: file}, flags, nil
}

// readAheadFile buffers only while this connection holds a lock, which keeps
// rollback-journal writers from changing the file. Every lock transition
// discards the buffer, and a WAL header disables buffering for good.
type readAheadFile struct {
	vfs.File
	lock   vfs.LockLevel
	wal    bool
	buf    []byte
	bufOff int64
	next   int64
	window int
}

func (f *readAheadFile) ReadAt(p []byte, off int64) (int, error) {
	if off == 0 {
		n, err := f.File.ReadAt(p, off)
		if n > 19 && (p[18] == walFormatVersion || p[19] == walFormatVersion) {
			f.wal = true
			f.reset()
		}
		return n, err
	}
	if f.wal || f.lock < vfs.LOCK_SHARED {
		return f.File.ReadAt(p, off)
	}
	end := off + int64(len(p))
	if off >= f.bufOff && end <= f.bufOff+int64(len(f.buf)) {
		f.next = end
		return copy(p, f.buf[off-f.bufOff:]), nil
	}
	sequential := off == f.next
	f.next = end
	if !sequential {
		// Random access: read exactly what SQLite asked for.
		f.window = 0
		return f.File.ReadAt(p, off)
	}
	f.window = min(max(f.window*2, readAheadMin), readAheadMax)
	if f.window <= len(p) {
		return f.File.ReadAt(p, off)
	}
	if cap(f.buf) < f.window {
		f.buf = make([]byte, f.window)
	}
	n, err := f.File.ReadAt(f.buf[:f.window], off)
	if n < len(p) || (err != nil && err != io.EOF) {
		// Let the file report short reads and errors for the bytes requested.
		f.buf = f.buf[:0]
		return f.File.ReadAt(p, off)
	}
	f.buf, f.bufOff = f.buf[:n], off
	return copy(p, f.buf), nil
}

// SharedMemory is also requested when the file opens, before any lock;
// under a lock it means SQLite is opening the WAL, possibly after another
// connection switched the database to WAL. A WAL pager keeps its lock across
// transactions, so buffering stops for good then.
func (f *readAheadFile) SharedMemory() vfs.SharedMemory {
	if f.lock >= vfs.LOCK_SHARED {
		f.wal = true
		f.reset()
	}
	return vfsutil.WrapSharedMemory(f.File)
}

func (f *readAheadFile) reset() {
	f.buf = f.buf[:0]
	f.next = -1
	f.window = 0
}

func (f *readAheadFile) Lock(lock vfs.LockLevel) error {
	f.reset()
	err := f.File.Lock(lock)
	if err == nil {
		f.lock = lock
	}
	return err
}

func (f *readAheadFile) Unlock(lock vfs.LockLevel) error {
	f.reset()
	err := f.File.Unlock(lock)
	if err != nil {
		// The lock state is unknown; stop buffering until the next Lock.
		lock = vfs.LOCK_NONE
	}
	f.lock = lock
	return err
}

func (f *readAheadFile) WriteAt(p []byte, off int64) (int, error) {
	f.reset()
	return f.File.WriteAt(p, off)
}

func (f *readAheadFile) Truncate(size int64) error {
	f.reset()
	return f.File.Truncate(size)
}

// Wrap optional methods.

func (f *readAheadFile) Unwrap() vfs.File         { return f.File }
func (f *readAheadFile) LockState() vfs.LockLevel { return vfsutil.WrapLockState(f.File) }
func (f *readAheadFile) PersistWAL() bool         { return vfsutil.WrapPersistWAL(f.File) }
func (f *readAheadFile) SetPersistWAL(keep bool)  { vfsutil.WrapSetPersistWAL(f.File, keep) }
func (f *readAheadFile) PowersafeOverwrite() bool { return vfsutil.WrapPowersafeOverwrite(f.File) }
func (f *readAheadFile) SetPowersafeOverwrite(psow bool) {
	vfsutil.WrapSetPowersafeOverwrite(f.File, psow)
}
func (f *readAheadFile) ChunkSize(size int)              { vfsutil.WrapChunkSize(f.File, size) }
func (f *readAheadFile) SizeHint(size int64) error       { return vfsutil.WrapSizeHint(f.File, size) }
func (f *readAheadFile) HasMoved() (bool, error)         { return vfsutil.WrapHasMoved(f.File) }
func (f *readAheadFile) Overwrite() error                { return vfsutil.WrapOverwrite(f.File) }
func (f *readAheadFile) SyncSuper(super string) error    { return vfsutil.WrapSyncSuper(f.File, super) }
func (f *readAheadFile) CommitPhaseTwo() error           { return vfsutil.WrapCommitPhaseTwo(f.File) }
func (f *readAheadFile) BeginAtomicWrite() error         { return vfsutil.WrapBeginAtomicWrite(f.File) }
func (f *readAheadFile) CommitAtomicWrite() error        { return vfsutil.WrapCommitAtomicWrite(f.File) }
func (f *readAheadFile) RollbackAtomicWrite() error      { return vfsutil.WrapRollbackAtomicWrite(f.File) }
func (f *readAheadFile) CheckpointStart()                { vfsutil.WrapCheckpointStart(f.File) }
func (f *readAheadFile) CheckpointDone()                 { vfsutil.WrapCheckpointDone(f.File) }
func (f *readAheadFile) BusyHandler(handler func() bool) { vfsutil.WrapBusyHandler(f.File, handler) }
func (f *readAheadFile) Pragma(name, value string) (string, error) {
	return vfsutil.WrapPragma(f.File, name, value)
}
