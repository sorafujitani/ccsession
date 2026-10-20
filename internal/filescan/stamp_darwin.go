package filescan

import (
	"os"
	"syscall"
)

func identity(info os.FileInfo) (dev, ino uint64, mtime, ctime int64, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, 0, 0, false
	}
	return uint64(st.Dev), st.Ino, st.Mtimespec.Nano(), st.Ctimespec.Nano(), true
}
