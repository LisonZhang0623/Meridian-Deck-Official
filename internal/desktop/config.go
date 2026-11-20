package desktop

import (
	"encoding/json"
	"errors"

	"sshtool/internal/core"
)

func (s *Service) callConfig(op string, args []json.RawMessage) (any, error) {
	if err := decode(args); err != nil {
		return nil, err
	}
	if op == "config:export" {
		if s.ui.SaveConfig == nil {
			return nil, errors.New("配置保存对话框不可用")
		}
		file, err := s.ui.SaveConfig("Meridian-Deck-config.json")
		if err != nil || file == "" {
			return false, err
		}
		err = s.store.ExportConfig(file)
		return err == nil, err
	}
	if s.ui.SelectConfig == nil {
		return nil, errors.New("配置打开对话框不可用")
	}
	file, err := s.ui.SelectConfig()
	if err != nil || file == "" {
		return nil, err
	}
	imported, err := s.store.ImportConfig(file)
	if err == nil {
		s.emit(core.Event{"type": "profiles", "profiles": imported.Profiles})
	}
	return imported, err
}
