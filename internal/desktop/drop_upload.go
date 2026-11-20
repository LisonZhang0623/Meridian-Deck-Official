package desktop

import (
	"errors"
	"path/filepath"
	"time"

	"sshtool/internal/core"
)

func (s *Service) takeDroppedPaths(tokens []string) ([]core.LocalDropEntry, error) {
	if len(tokens) == 0 || len(tokens) > 100 {
		return nil, errors.New("每次最多拖入 100 个文件或文件夹")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	seen := make(map[string]bool, len(tokens))
	entries := make([]core.LocalDropEntry, 0, len(tokens))
	for _, token := range tokens {
		grant, ok := s.grants[token]
		if !ok || grant.drop == nil || !grant.expires.After(now) || seen[token] {
			return nil, errors.New("拖入选择已失效，请重新拖入")
		}
		seen[token] = true
		entries = append(entries, core.LocalDropEntry{Path: grant.path, Info: grant.drop})
	}
	for _, token := range tokens {
		delete(s.grants, token)
	}
	return entries, nil
}

func (s *Service) uploadDrop(session, directory string, tokens []string) (bool, error) {
	if err := core.ValidateRemotePath(directory); err != nil {
		return false, err
	}
	if _, err := s.ssh.SFTP(session); err != nil {
		return false, err
	}
	identity, err := s.ssh.Identity(session)
	if err != nil {
		return false, err
	}
	entries, err := s.takeDroppedPaths(tokens)
	if err != nil {
		return false, err
	}
	if s.ui.ConfirmDragTransfer == nil {
		return false, errors.New("拖拽上传需要确认")
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, filepath.Base(entry.Path))
	}
	if !s.ui.ConfirmDragTransfer("upload", names, directory) {
		return false, nil
	}
	if current, err := s.ssh.Identity(session); err != nil || current != identity {
		return false, errors.New("确认期间 SSH 连接已变化，请重新拖入")
	}
	if err = s.transfers.UploadDropped(session, directory, entries); err != nil {
		return false, err
	}
	return true, nil
}
