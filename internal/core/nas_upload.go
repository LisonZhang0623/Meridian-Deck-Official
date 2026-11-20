package core

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type nasUploadSource struct {
	root, relative   string
	rootInfo, source os.FileInfo
}

// LocalizeNASPath validates a nonempty slash-separated path inside the NAS root.
func LocalizeNASPath(relative string) (string, error) {
	if len(relative) > 32768 {
		return "", errors.New("NAS 文件路径过长")
	}
	for _, name := range strings.Split(relative, "/") {
		if err := ValidateEntryName(name); err != nil {
			return "", fmt.Errorf("无效的 NAS 相对路径: %w", err)
		}
	}
	local, err := filepath.Localize(relative)
	if err != nil {
		return "", errors.New("无效的 NAS 相对路径")
	}
	return local, nil
}

// CheckNASDirectory verifies each component before visiting a directory.
// An empty path or dot represents the already-open root.
func CheckNASDirectory(root *os.Root, relative string) (os.FileInfo, error) {
	if relative == "" || relative == "." {
		return root.Stat(".")
	}
	if _, err := LocalizeNASPath(relative); err != nil {
		return nil, err
	}
	var info os.FileInfo
	var name string
	for _, component := range strings.Split(relative, "/") {
		name = filepath.Join(name, component)
		current, err := root.Lstat(name)
		if err != nil {
			return nil, err
		}
		if current.Mode().Type() != os.ModeDir {
			return nil, errors.New("NAS 子目录必须是普通目录，不能经过链接")
		}
		info = current
	}
	return info, nil
}
