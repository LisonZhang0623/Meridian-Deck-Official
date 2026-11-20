package core

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
)

type configBundle struct {
	Kind     string    `json:"kind"`
	Version  int       `json:"version"`
	Profiles []Profile `json:"profiles"`
	Settings Settings  `json:"settings"`
}

type ConfigImport struct {
	Added    int       `json:"added"`
	Profiles []Profile `json:"profiles"`
	Settings Settings  `json:"settings"`
}

func portableProfile(p Profile) Profile {
	p = cloneProfile(p)
	p.RememberCredentials, p.HasCredentials = false, false
	p.LastConnectedAt, p.JumpIdentity = 0, ""
	return p
}

func (s *Store) ExportConfig(file string) error {
	if !filepath.IsAbs(file) {
		return errors.New("配置导出路径无效")
	}
	if target, err := os.Stat(file); err == nil {
		for _, name := range []string{"settings.json", "commands.json", nasConfigFile} {
			if live, err := os.Stat(filepath.Join(s.directory, name)); err == nil && os.SameFile(live, target) {
				return errors.New("请使用其他文件名，避免覆盖当前工作台数据")
			}
		}
	}
	s.mu.RLock()
	bundle := configBundle{Kind: "sshtool-config", Version: 1, Profiles: make([]Profile, 0, len(s.data.Profiles)), Settings: s.data.Settings}
	for _, profile := range s.data.Profiles {
		bundle.Profiles = append(bundle.Profiles, portableProfile(profile))
	}
	s.mu.RUnlock()
	raw, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) >= 8*1024*1024 {
		return errors.New("配置文件超过大小限制")
	}
	return atomicWrite(file, append(raw, '\n'))
}

func (s *Store) ImportConfig(file string) (ConfigImport, error) {
	if !filepath.IsAbs(file) {
		return ConfigImport{}, errors.New("配置导入路径无效")
	}
	if info, err := os.Stat(file); err != nil || !info.Mode().IsRegular() {
		return ConfigImport{}, errors.New("请选择普通 JSON 文件")
	}
	raw, err := readConfig(file)
	if err != nil {
		return ConfigImport{}, err
	}
	var bundle configBundle
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&bundle) != nil || decoder.Decode(new(any)) != io.EOF || bundle.Kind != "sshtool-config" || bundle.Version != 1 || bundle.Profiles == nil || len(bundle.Profiles) > 2000 {
		return ConfigImport{}, errors.New("不支持此配置文件，请使用 Meridian Deck 导出的 JSON")
	}
	bundle.Settings, err = ValidateSettings(bundle.Settings)
	if err != nil {
		return ConfigImport{}, err
	}
	incoming := make(map[string]Profile, len(bundle.Profiles))
	for i, p := range bundle.Profiles {
		if err = ValidateID(p.ID); err != nil {
			return ConfigImport{}, err
		}
		p, err = ValidateProfile(portableProfile(p))
		if err != nil {
			return ConfigImport{}, err
		}
		if _, exists := incoming[p.ID]; exists {
			return ConfigImport{}, errors.New("导入文件有重复连接标识")
		}
		incoming[p.ID], bundle.Profiles[i] = p, p
	}
	if err = validateProfileLinks(bundle.Profiles); err != nil {
		return ConfigImport{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	next := cloneData(s.data)
	current := make(map[string]Profile, len(next.Profiles))
	for _, profile := range next.Profiles {
		current[profile.ID] = portableProfile(profile)
	}
	remapped := make(map[string]string, len(incoming))
	var merge func(string) (string, error)
	merge = func(id string) (string, error) {
		if mapped := remapped[id]; mapped != "" {
			return mapped, nil
		}
		profile := incoming[id]
		if profile.JumpProfileID != "" {
			var err error
			profile.JumpProfileID, err = merge(profile.JumpProfileID)
			if err != nil {
				return "", err
			}
		}
		if existing, taken := current[id]; taken && !reflect.DeepEqual(existing, profile) {
			raw, _ := json.Marshal(profile)
			hash := sha256.Sum256(raw)
			profile.ID = "import-" + hex.EncodeToString(hash[:16])
		}
		if existing, taken := current[profile.ID]; taken {
			if !reflect.DeepEqual(existing, profile) {
				return "", errors.New("导入连接标识冲突")
			}
		} else {
			if len(next.Profiles) >= 2000 {
				return "", errors.New("导入后连接数将超过 2000")
			}
			next.Profiles = append(next.Profiles, profile)
			current[profile.ID] = profile
		}
		remapped[id] = profile.ID
		return profile.ID, nil
	}
	for _, profile := range bundle.Profiles {
		if _, err := merge(profile.ID); err != nil {
			return ConfigImport{}, err
		}
	}
	if err = validateProfileLinks(next.Profiles); err != nil {
		return ConfigImport{}, err
	}
	next.Settings = bundle.Settings
	if err = s.persist(next); err != nil {
		return ConfigImport{}, err
	}
	result := ConfigImport{Added: len(next.Profiles) - len(s.data.Profiles), Profiles: make([]Profile, 0, len(next.Profiles)), Settings: next.Settings}
	s.data = next
	for _, profile := range next.Profiles {
		result.Profiles = append(result.Profiles, publicProfile(next, profile))
	}
	return result, nil
}
