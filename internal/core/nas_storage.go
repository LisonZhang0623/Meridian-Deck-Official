package core

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

const nasConfigFile = "nas.json"

type nasConfig struct {
	Root string `json:"root"`
}

// NAS selection is local authorization and is never part of portable settings.
func (s *Store) LoadNASRoot() (string, error) {
	file := filepath.Join(s.directory, nasConfigFile)
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return "", errors.New("无法读取 NAS 文件夹配置，请重新选择")
	}
	raw, err := readConfig(file)
	var config nasConfig
	if err != nil || json.Unmarshal(raw, &config) != nil || !filepath.IsAbs(config.Root) {
		return "", errors.New("NAS 文件夹配置无效，请重新选择")
	}
	return config.Root, nil
}

func (s *Store) SaveNASRoot(root string) error {
	if !filepath.IsAbs(root) {
		return errors.New("NAS 文件夹必须是绝对路径")
	}
	raw, err := json.Marshal(nasConfig{Root: root})
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(s.directory, nasConfigFile), append(raw, '\n'))
}
