package desktop

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"sshtool/internal/core"
)

type directoryPlan struct {
	session string
	plan    core.DirectoryPlan
	expires time.Time
}

func (s *Service) selectTransferDirectory(id, title string) (string, error) {
	if s.ui.SelectDirectory == nil {
		return "", errors.New("当前平台未提供目录选择")
	}
	identity, err := s.ssh.Identity(id)
	if err != nil {
		return "", err
	}
	local, err := s.ui.SelectDirectory(title)
	if err != nil || local == "" {
		return "", err
	}
	if !filepath.IsAbs(local) {
		return "", errors.New("本地目录必须是绝对路径")
	}
	local, err = filepath.EvalSymlinks(local)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(local); err != nil || !info.IsDir() {
		return "", errors.New("请选择可访问的本地目录")
	}
	cache := s.cacheRoot
	if resolved, err := filepath.EvalSymlinks(cache); err == nil {
		cache = resolved
	}
	// A parent of the cache would also include internal drag-out snapshots.
	for _, paths := range [][2]string{{cache, local}, {local, cache}} {
		if rel, err := filepath.Rel(paths[0], paths[1]); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", errors.New("请选择拖出缓存以外的目录")
		}
	}
	if current, err := s.ssh.Identity(id); err != nil || current != identity {
		return "", errors.New("选择目录期间连接已变化，请重试")
	}
	return local, nil
}

func (s *Service) rememberDirectoryPlan(id string, plan core.DirectoryPlan) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.filePlans == nil {
		s.filePlans = make(map[string]directoryPlan)
	}
	now := time.Now()
	var oldest string
	for key, existing := range s.filePlans {
		if !existing.expires.After(now) || existing.session == id {
			delete(s.filePlans, key)
			continue
		}
		if oldest == "" || existing.expires.Before(s.filePlans[oldest].expires) {
			oldest = key
		}
	}
	if len(s.filePlans) >= 8 {
		delete(s.filePlans, oldest)
	}
	key := core.NewID()
	s.filePlans[key] = directoryPlan{session: id, plan: plan, expires: now.Add(10 * time.Minute)}
	return key
}

func (s *Service) takeDirectoryPlan(id, key string) (core.DirectoryPlan, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.filePlans[key]
	if !ok || entry.session != id || !entry.expires.After(time.Now()) {
		return core.DirectoryPlan{}, errors.New("目录比较结果已过期，请重新比较")
	}
	delete(s.filePlans, key)
	return entry.plan, nil
}

func (s *Service) callFileFeature(op string, args []json.RawMessage) (any, error) {
	var id, remote, direction string
	switch op {
	case "transfer:pause":
		if err := decode(args, &id); err != nil {
			return nil, err
		}
		return nil, s.transfers.Pause(id)
	case "transfer:resume", "transfer:discardPartial":
		var session string
		if err := decode(args, &id, &session); err != nil {
			return nil, err
		}
		if session != "" {
			if err := core.ValidateID(session); err != nil {
				return nil, err
			}
		}
		if op == "transfer:resume" {
			return nil, s.transfers.Resume(id, session)
		}
		return nil, s.transfers.DiscardPartial(id, session)
	case "files:deleteTree":
		if err := decode(args, &id, &remote); err != nil {
			return nil, err
		}
		if s.ui.ConfirmDeleteTree == nil {
			return nil, errors.New("递归删除需要系统确认")
		}
		plan, err := s.transfers.PrepareDirectoryDelete(id, remote)
		if err != nil {
			return nil, err
		}
		if !s.ui.ConfirmDeleteTree(plan.RemotePath, plan.Count) {
			return false, nil
		}
		if err = s.transfers.DeleteDirectoryTree(plan); err != nil {
			return nil, err
		}
		return true, nil
	case "files:syncDirectory":
		if err := decode(args, &id, &remote, &direction); err != nil {
			return nil, err
		}
		if direction != "upload" && direction != "download" {
			return nil, errors.New("请选择有效的同步方向")
		}
		plan, err := s.takeDirectoryPlan(id, remote)
		if err != nil {
			return nil, err
		}
		return s.transfers.SyncDirectory(plan, direction)
	default:
		if err := decode(args, &id, &remote); err != nil {
			return nil, err
		}
		if err := core.ValidateRemotePath(remote); err != nil {
			return nil, err
		}
		title := "选择要比较的本地目录"
		if op == "transfer:uploadDirectory" {
			title = "选择要上传的文件夹"
		} else if op == "transfer:downloadDirectory" {
			title = "选择下载到的本地目录"
		}
		local, err := s.selectTransferDirectory(id, title)
		if err != nil || local == "" {
			return nil, err
		}
		switch op {
		case "transfer:uploadDirectory":
			return s.transfers.UploadDirectory(id, local, remote)
		case "transfer:downloadDirectory":
			return s.transfers.DownloadDirectory(id, remote, local)
		case "files:compareDirectory":
			plan, err := s.transfers.CompareDirectory(id, local, remote)
			if err != nil {
				return nil, err
			}
			key := s.rememberDirectoryPlan(id, plan)
			return struct {
				Token string `json:"token"`
				core.DirectoryPlan
			}{Token: key, DirectoryPlan: plan}, nil
		}
	}
	return nil, errors.New("未知的目录操作")
}
