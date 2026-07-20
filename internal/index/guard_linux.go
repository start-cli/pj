//go:build linux

package index

import (
	"fmt"
	"syscall"
)

// nonLocalMagic maps the statfs f_type magic of filesystems where WAL is unsafe —
// network or FUSE-backed, where the DB and its -wal/-shm can separate or lack a
// real fsync — to a human label. A hit means "hard-warn, point at XDG_STATE_HOME".
var nonLocalMagic = map[int64]string{
	0x6969:     "NFS",
	0xFF534D42: "CIFS/SMB",
	0x517B:     "SMB",
	0xFE534D42: "SMB2",
	0x65735546: "FUSE",
}

// localDiskWarning returns a non-empty warning when dir is on a filesystem where
// WAL is unsafe. It is best-effort: an unstatfs-able path or an unrecognised type
// yields no warning (we do not block a store on an unknown-but-likely-local FS).
func localDiskWarning(dir string) string {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return ""
	}
	if label, ok := nonLocalMagic[st.Type]; ok {
		return diskWarnMsg(dir, label)
	}
	return ""
}

func diskWarnMsg(dir, label string) string {
	return fmt.Sprintf("index directory %s looks like a %s (non-local) filesystem; WAL is unsafe there — set XDG_STATE_HOME to a local disk", dir, label)
}
