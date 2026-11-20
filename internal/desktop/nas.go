package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"sshtool/internal/core"
)

const maxNASDirectoryEntries = 2000

type nasSelection struct {
	id, path string
	loadErr  error
}

type nasEntry struct {
	Path     string `json:"path"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Size     int64  `json:"size"`
	Modified int64  `json:"modified"`
}

type nasDirectory struct {
	RootID    string     `json:"rootId"`
	Root      string     `json:"root"`
	Path      string     `json:"path"`
	Parent    *string    `json:"parent"`
	Entries   []nasEntry `json:"entries"`
	Truncated bool       `json:"truncated"`
}

func nasPathContains(base, target string) bool {
	relative, err := filepath.Rel(base, target)
	return err == nil && filepath.IsLocal(relative)
}

// The drag cache is created lazily. Resolve aliases in its existing ancestors
// even when the last components do not exist yet.
func resolveNASCachePath(cache string) (string, error) {
	resolved, err := filepath.EvalSymlinks(cache)
	if !errors.Is(err, os.ErrNotExist) {
		return resolved, err
	}
	parent := filepath.Dir(cache)
	if _, statErr := os.Lstat(cache); parent == cache || !errors.Is(statErr, os.ErrNotExist) {
		return "", err
	}
	resolvedParent, err := resolveNASCachePath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolvedParent, filepath.Base(cache)), nil
}

func (s *Service) openNASRoot(directory string) (*os.Root, error) {
	resolved, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, fmt.Errorf("无法访问 NAS 文件夹，请检查网络共享或挂载状态：%w", err)
	}
	if relative, err := filepath.Rel(directory, resolved); err != nil || relative != "." {
		return nil, errors.New("NAS 文件夹路径已变化，请重新选择")
	}
	cache, err := resolveNASCachePath(s.cacheRoot)
	if err != nil {
		return nil, fmt.Errorf("无法核对拖出缓存路径：%w", err)
	}
	if nasPathContains(cache, directory) || nasPathContains(directory, cache) {
		return nil, errors.New("请选择拖出缓存以外的 NAS 文件夹")
	}
	before, err := os.Lstat(directory)
	if err != nil || before.Mode().Type() != os.ModeDir {
		return nil, errors.New("请选择可访问的 NAS 文件夹")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, fmt.Errorf("无法打开 NAS 文件夹：%w", err)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		root.Close()
		return nil, errors.New("NAS 文件夹已变化，请重新选择")
	}
	return root, nil
}

func listNASDirectory(selection nasSelection, root *os.Root, relative string) (nasDirectory, error) {
	listing := nasDirectory{RootID: selection.id, Root: selection.path, Path: relative, Entries: []nasEntry{}}
	local := "."
	if relative != "" {
		var err error
		if local, err = core.LocalizeNASPath(relative); err != nil {
			return listing, err
		}
		parent := path.Dir(relative)
		if parent == "." {
			parent = ""
		}
		listing.Parent = &parent
	}
	before, err := core.CheckNASDirectory(root, relative)
	if err != nil {
		return listing, err
	}
	directory, err := root.OpenRoot(local)
	if err != nil {
		return listing, err
	}
	defer directory.Close()
	opened, err := directory.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		return listing, errors.New("NAS 子目录已变化，请刷新列表")
	}
	file, err := directory.Open(".")
	if err != nil {
		return listing, err
	}
	defer file.Close()
	entries, err := file.Readdir(maxNASDirectoryEntries + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return listing, err
	}
	listing.Truncated = len(entries) > maxNASDirectoryEntries
	if listing.Truncated {
		entries = entries[:maxNASDirectoryEntries]
	}
	for _, entry := range entries {
		kind := "file"
		if entry.Mode().Type() == os.ModeDir {
			kind = "directory"
		} else if !entry.Mode().IsRegular() {
			continue
		}
		slashPath := path.Join(relative, entry.Name())
		if _, err := core.LocalizeNASPath(slashPath); err != nil {
			continue
		}
		listing.Entries = append(listing.Entries, nasEntry{
			Path: slashPath, Name: entry.Name(), Type: kind, Size: entry.Size(), Modified: entry.ModTime().UnixMilli(),
		})
	}
	sort.Slice(listing.Entries, func(i, j int) bool {
		a, b := listing.Entries[i], listing.Entries[j]
		if a.Type != b.Type {
			return a.Type == "directory"
		}
		if compare := strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)); compare != 0 {
			return compare < 0
		}
		return a.Name < b.Name
	})
	return listing, nil
}

func (s *Service) selectNASRoot() (any, error) {
	if s.ui.SelectDirectory == nil {
		return nil, errors.New("当前平台未提供目录选择")
	}
	if !s.nasSelecting.CompareAndSwap(false, true) {
		return nil, errors.New("NAS 文件夹选择窗口已打开")
	}
	defer s.nasSelecting.Store(false)
	chosen, err := s.ui.SelectDirectory("选择 NAS 共享文件夹")
	if err != nil || chosen == "" {
		return nil, err
	}
	if !filepath.IsAbs(chosen) {
		return nil, errors.New("NAS 文件夹必须是绝对路径")
	}
	canonical, err := filepath.EvalSymlinks(chosen)
	if err != nil {
		return nil, fmt.Errorf("无法访问所选 NAS 文件夹：%w", err)
	}
	root, err := s.openNASRoot(canonical)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	selection := nasSelection{id: core.NewID(), path: canonical}
	listing, err := listNASDirectory(selection, root, "")
	if err != nil {
		return nil, fmt.Errorf("无法读取所选 NAS 文件夹：%w", err)
	}
	if err = s.store.SaveNASRoot(canonical); err != nil {
		return nil, fmt.Errorf("无法保存 NAS 文件夹：%w", err)
	}
	s.mu.Lock()
	s.nas = selection
	s.mu.Unlock()
	return listing, nil
}

func (s *Service) selectedNASRoot(rootID string, allowBootstrap bool) (nasSelection, error) {
	s.mu.Lock()
	selection := s.nas
	s.mu.Unlock()
	if selection.loadErr != nil {
		return nasSelection{}, selection.loadErr
	}
	if !(allowBootstrap && rootID == "") && (rootID == "" || selection.id != rootID) {
		return nasSelection{}, errors.New("NAS 文件夹已变化，请刷新后重新选择文件")
	}
	return selection, nil
}

func (s *Service) callNAS(op string, args []json.RawMessage) (any, error) {
	switch op {
	case "nas:select":
		if err := decode(args); err != nil {
			return nil, err
		}
		return s.selectNASRoot()
	case "nas:list":
		var relative, rootID string
		var err error
		if len(args) == 1 {
			err = decode(args, &relative)
		} else {
			err = decode(args, &relative, &rootID)
		}
		if err != nil {
			return nil, err
		}
		selection, err := s.selectedNASRoot(rootID, relative == "")
		if err != nil || selection.path == "" {
			return nil, err
		}
		root, err := s.openNASRoot(selection.path)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		listing, err := listNASDirectory(selection, root, relative)
		if err != nil {
			return nil, fmt.Errorf("无法读取 NAS 目录：%w", err)
		}
		if _, err = s.selectedNASRoot(selection.id, false); err != nil {
			return nil, err
		}
		return listing, nil
	case "nas:upload", "nas:download":
		var session, remote, rootID, relative string
		var paths []string
		if op == "nas:upload" {
			if err := decode(args, &session, &remote, &rootID, &paths); err != nil {
				return nil, err
			}
			if err := core.ValidateRemotePath(remote); err != nil {
				return nil, err
			}
		} else if err := decode(args, &session, &paths, &rootID, &relative); err != nil {
			return nil, err
		}
		if err := core.ValidateID(session); err != nil {
			return nil, err
		}
		selection, err := s.selectedNASRoot(rootID, false)
		if err != nil {
			return nil, err
		}
		if _, err = s.ssh.SFTP(session); err != nil {
			return nil, err
		}
		root, err := s.openNASRoot(selection.path)
		if err != nil {
			return nil, err
		}
		defer root.Close()
		if _, err = s.selectedNASRoot(selection.id, false); err != nil {
			return nil, err
		}
		if op == "nas:upload" {
			return nil, s.transfers.UploadFromRoot(session, remote, root, paths)
		}
		return nil, s.transfers.DownloadToRoot(session, paths, root, relative)
	}
	return nil, errors.New("未知的 NAS 操作")
}
