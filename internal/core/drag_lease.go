package core

import (
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	dragRunLease = ".sshtool-drag-run"
	dragRunMagic = "sshtool-drag-cache-v1\n"
)

func createDragRun(root string) (string, *os.File, error) {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", nil, err
	}
	run, err := os.MkdirTemp(root, "drag-")
	if err != nil {
		return "", nil, err
	}
	marker := filepath.Join(run, dragRunLease)
	lease, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err == nil {
		err = lockDragRun(lease)
		if err == nil {
			// Only advertise ownership once the lease is held. A concurrent
			// startup must skip a run that is still being created.
			_, err = lease.WriteString(dragRunMagic)
		}
		if err == nil {
			return run, lease, nil
		}
		_ = lease.Close()
		_ = os.Remove(marker)
	}
	_ = os.Remove(run)
	return "", nil, err
}

// Reclaim only marked snapshots whose previous process no longer holds its lease.
// Cleanup is best effort so a locked or unreadable cache cannot prevent startup.
func reclaimDragRuns(directory string) {
	if !filepath.IsAbs(directory) {
		return
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() {
		return
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "drag-") {
			continue
		}
		marker := filepath.Join(entry.Name(), dragRunLease)
		info, err := root.Lstat(marker)
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(dragRunMagic)) {
			continue
		}
		lease, err := root.OpenFile(marker, os.O_RDWR, 0)
		if err != nil {
			continue
		}
		var magic [len(dragRunMagic)]byte
		if err = lockDragRun(lease); err == nil {
			_, err = io.ReadFull(lease, magic[:])
		}
		_ = lease.Close()
		if err == nil && string(magic[:]) == dragRunMagic {
			_ = root.RemoveAll(entry.Name())
		}
	}
}
