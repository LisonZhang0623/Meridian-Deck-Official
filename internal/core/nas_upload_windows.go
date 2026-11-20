package core

import "os"

func openNASFile(root *os.Root, name string) (*os.File, error) {
	return root.Open(name)
}
