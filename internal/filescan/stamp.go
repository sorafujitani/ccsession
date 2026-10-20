package filescan

import (
	"fmt"
	"os"
	"time"
)

// racyWindow covers coarse file timestamps: a file changed this recently can
// change again without a new mtime or ctime.
const racyWindow = 2 * time.Second

// Stamp identifies a file version from metadata alone: device, inode, size,
// and nanosecond mtime and ctime. Reads, which only touch atime, keep it, and
// a same-size rewrite with a restored mtime still changes ctime. It returns
// "" on platforms without inode and ctime, so callers fall back to content
// checks instead of trusting mtime.
func Stamp(info os.FileInfo) string {
	dev, ino, mtime, ctime, ok := identity(info)
	if !ok {
		return ""
	}
	return fmt.Sprintf("%d:%d:%d:%d:%d", dev, ino, info.Size(), mtime, ctime)
}

// Settled reports whether info's timestamps are old enough at now for its
// Stamp to identify the content, like Git's racily clean entries: a file
// changed within the timestamp granularity may change again unnoticed.
func Settled(info os.FileInfo, now time.Time) bool {
	_, _, mtime, ctime, ok := identity(info)
	settled := now.Add(-racyWindow).UnixNano()
	return ok && mtime <= settled && ctime <= settled
}
