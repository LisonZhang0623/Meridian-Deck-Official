//go:build !windows

package core

import (
	"os"

	"golang.org/x/sys/unix"
)

func openNASFile(root *os.Root, name string) (*os.File, error) {
	// A FIFO substituted after Lstat must never block this open.
	return root.OpenFile(name, os.O_RDONLY|unix.O_NONBLOCK, 0)
}
