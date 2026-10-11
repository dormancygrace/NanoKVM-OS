package authn

import (
	"io"
	"os"
	"time"
)

// settleTime is how old an account file's modification time must be before
// the parsed file is cached. Filesystem timestamps are coarse (a scheduler
// tick on ext4, two seconds on FAT), so a file written within one tick of
// being read could be rewritten at the same size and time without the change
// showing in its identity.
const settleTime = 5 * time.Second

type cachedDatabase struct {
	stamp fileStamp
	db    *database
}

// fileStamp identifies one version of the account file.
type fileStamp struct {
	size    int64
	modTime time.Time
	sys     sysStamp
}

func stampOf(info os.FileInfo) fileStamp {
	return fileStamp{size: info.Size(), modTime: info.ModTime(), sys: sysStampOf(info)}
}

func (a fileStamp) equal(b fileStamp) bool {
	return a.size == b.size && a.modTime.Equal(b.modTime) && a.sys == b.sys
}

// settled reports whether the file was last written long enough ago that a
// later write is bound to change its stamp. A modification time ahead of the
// clock counts as recent too, so a clock that stepped backwards cannot pin a
// stale file.
func (a fileStamp) settled(now time.Time) bool {
	age := now.Sub(a.modTime)
	return age >= settleTime
}

// readAccountFile reads the file and stats the same open file, so the stamp
// describes the content that was read even if the file is replaced between
// the two. It is a variable so a test can count the reads.
var readAccountFile = func(path string) ([]byte, os.FileInfo, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, nil, err
	}
	return data, info, nil
}

func (db *database) clone() *database {
	copy := *db
	copy.Users = make([]User, len(db.Users))
	for index := range db.Users {
		copy.Users[index] = *cloneUser(&db.Users[index])
	}
	return &copy
}
