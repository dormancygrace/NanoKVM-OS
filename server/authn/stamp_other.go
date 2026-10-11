//go:build !linux

package authn

import "os"

// sysStamp is empty off Linux, where the size and modification time alone
// identify the file. The server only runs on Linux; this keeps the package
// building elsewhere.
type sysStamp struct{}

func sysStampOf(os.FileInfo) sysStamp { return sysStamp{} }
