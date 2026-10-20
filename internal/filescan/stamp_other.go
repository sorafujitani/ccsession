//go:build !darwin && !linux

package filescan

import "os"

func identity(os.FileInfo) (dev, ino uint64, mtime, ctime int64, ok bool) {
	return 0, 0, 0, 0, false
}
