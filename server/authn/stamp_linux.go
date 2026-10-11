//go:build linux

package authn

import (
	"os"
	"syscall"
)

// sysStamp is the part of a file's identity the portable os.FileInfo leaves
// out. The inode and device change when the file is replaced by a rename, as
// the store's own writes and most editors do. The change time cannot be set
// from user space, unlike the modification time.
type sysStamp struct {
	dev, ino            uint64
	ctimeSec, ctimeNsec int64
}

func sysStampOf(info os.FileInfo) sysStamp {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return sysStamp{}
	}
	return sysStamp{
		dev:       uint64(stat.Dev),
		ino:       uint64(stat.Ino),
		ctimeSec:  int64(stat.Ctim.Sec),
		ctimeNsec: int64(stat.Ctim.Nsec),
	}
}
