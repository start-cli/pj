//go:build darwin

package index

import (
	"fmt"
	"strings"
	"syscall"
)

// nonLocalFSType lists the macOS f_fstypename values where WAL is unsafe. macOS
// reports the filesystem by name rather than a magic number, so the check is a
// name match against the network/remote families.
var nonLocalFSType = map[string]string{
	"nfs":     "NFS",
	"smbfs":   "SMB",
	"webdav":  "WebDAV",
	"afpfs":   "AFP",
	"osxfuse": "FUSE",
	"macfuse": "FUSE",
}

// localDiskWarning returns a non-empty warning when dir is on a filesystem where
// WAL is unsafe. Best-effort: an unstatfs-able path or an unrecognised type yields
// no warning.
func localDiskWarning(dir string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return ""
	}
	name := int8SliceToString(st.Fstypename[:])
	if label, ok := nonLocalFSType[name]; ok {
		return diskWarnMsg(dir, label)
	}
	return ""
}

func int8SliceToString(b []int8) string {
	buf := make([]byte, 0, len(b))
	for _, c := range b {
		if c == 0 {
			break
		}
		buf = append(buf, byte(c))
	}
	return strings.ToLower(string(buf))
}

func diskWarnMsg(dir, label string) string {
	return fmt.Sprintf("index directory %s looks like a %s (non-local) filesystem; WAL is unsafe there — set XDG_STATE_HOME to a local disk", dir, label)
}
