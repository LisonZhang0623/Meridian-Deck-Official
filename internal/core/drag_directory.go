package core

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"reflect"
)

type dragFolderSnapshot struct {
	info    os.FileInfo
	entries map[string]fileMark
}

func cachedFolderEntries(directory string, expected os.FileInfo) (map[string]fileMark, error) {
	info, err := os.Lstat(directory)
	if err != nil || info.Mode().Type() != os.ModeDir || !os.SameFile(info, expected) {
		return nil, errors.New("拖出缓存文件夹已变化")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(opened, expected) {
		return nil, errors.New("拖出缓存文件夹已变化")
	}
	return scanTree(context.Background(), localTree(root), false)
}

func (folder *dragFolderSnapshot) matches(directory string) bool {
	entries, err := cachedFolderEntries(directory, folder.info)
	return err == nil && reflect.DeepEqual(entries, folder.entries)
}

func (d *DragDownloads) prepareDirectory(id, identity, remote string) (DragDownload, error) {
	if _, err := dragFileName(remote); err != nil {
		return DragDownload{}, err
	}
	if !filepath.IsAbs(d.root) {
		return DragDownload{}, errors.New("拖出缓存目录必须是绝对路径")
	}
	if err := d.assertIdentity(id, identity); err != nil {
		return DragDownload{}, err
	}
	d.mu.Lock()
	if err := d.ensureRun(); err != nil {
		d.mu.Unlock()
		return DragDownload{}, err
	}
	run := d.run
	var stale []string
	for token, entry := range d.files {
		if entry.folder == nil || entry.used {
			continue
		}
		if entry.public.ExpiresAt <= d.now().UnixMilli() || (entry.sessionID == id && entry.identity == identity && entry.remote == remote) {
			delete(d.files, token)
			stale = append(stale, entry.directory)
		}
	}
	d.mu.Unlock()
	for _, directory := range stale {
		if err := d.removeItem(directory, run); err != nil {
			return DragDownload{}, err
		}
	}
	if err := verifyCacheChild(run, d.root); err != nil {
		return DragDownload{}, err
	}
	item, err := os.MkdirTemp(run, "folder-")
	if err != nil {
		return DragDownload{}, err
	}
	retained := false
	defer func() {
		if !retained {
			_ = d.removeItem(item, run)
		}
	}()
	plan, jobs, err := d.transfers.downloadDirectoryJobs(id, remote, item, "drag", identity)
	if err != nil {
		return DragDownload{}, err
	}
	if err = d.waitDirectoryJobs(jobs); err != nil {
		return DragDownload{}, err
	}
	if err = d.verifyRemoteFolder(plan); err != nil {
		return DragDownload{}, err
	}
	name := path.Base(plan.RemotePath)
	local := filepath.Join(item, name)
	entries, err := cachedFolderEntries(local, plan.localRootInfo)
	if err != nil {
		return DragDownload{}, err
	}
	if len(entries) != len(plan.remote) {
		return DragDownload{}, errors.New("拖出缓存文件夹不完整，请重新准备")
	}
	for rel, source := range plan.remote {
		cached := entries[rel]
		if cached.kind != source.kind || source.kind == "file" && cached.size != source.size {
			return DragDownload{}, errors.New("拖出缓存文件夹不完整，请重新准备")
		}
	}
	if err = d.assertIdentity(id, identity); err != nil {
		return DragDownload{}, err
	}
	entry := cachedDrag{
		public:    DragDownload{Token: NewID(), Name: name, ExpiresAt: d.now().Add(dragLifetime).UnixMilli()},
		sessionID: id, identity: identity, remote: remote, local: local, directory: item,
		folder: &dragFolderSnapshot{info: plan.localRootInfo, entries: entries},
	}
	result, err := d.remember(entry)
	retained = err == nil
	return result, err
}

func (d *DragDownloads) waitDirectoryJobs(jobs []*transferJob) error {
	var failed error
	for _, job := range jobs {
		if err := d.transfers.Wait(job.transfer.ID); err != nil && failed == nil {
			failed = err
			for _, pending := range jobs {
				d.transfers.Cancel(pending.transfer.ID)
			}
		}
	}
	return failed
}

func (d *DragDownloads) verifyRemoteFolder(plan DirectoryPlan) error {
	ctx, c, identity, release, err := d.transfers.directoryChannel(plan.session)
	if err != nil {
		return err
	}
	defer release()
	if identity != plan.identity {
		return errors.New("连接已变化，请重新准备拖出下载")
	}
	entries, err := scanTree(ctx, remoteTree(ctx, c, plan.RemotePath), true)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(entries, plan.remote) {
		return errors.New("源文件夹在下载期间发生变化，请重新准备拖出")
	}
	return nil
}
