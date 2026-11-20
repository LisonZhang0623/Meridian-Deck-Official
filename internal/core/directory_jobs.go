package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"

	"github.com/pkg/sftp"
)

func validateCopyTree(entries map[string]fileMark, local, remote string) error {
	if len(caseCollisions(treePaths(entries, nil))) > 0 {
		return errors.New("目录包含仅大小写不同的文件名")
	}
	for rel, mark := range entries {
		if unsupportedTreeEntry(mark) {
			return fmt.Errorf("整目录传输不支持符号链接或特殊文件 %q", rel)
		}
		if err := validateLocalTreeName(path.Base(rel)); err != nil {
			return err
		}
		if err := ValidateRemotePath(path.Join(remote, rel)); err != nil || len(filepath.Join(local, filepath.FromSlash(rel))) > 32768 {
			return errors.New("目录内的文件路径过长或无效")
		}
	}
	return nil
}

func (t *Transfers) UploadDirectory(id, local, remoteParent string) (int, error) {
	root, local, info, err := openedDirectory(local)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	name := filepath.Base(local)
	if err := ValidateEntryName(name); err != nil {
		return 0, err
	}
	ctx, c, identity, release, err := t.directoryChannel(id)
	if err != nil {
		return 0, err
	}
	defer release()
	remoteParent, err = remoteDirectory(c, remoteParent)
	if err != nil {
		return 0, err
	}
	remote := path.Join(remoteParent, name)
	if err := ValidateRemotePath(remote); err != nil {
		return 0, err
	}
	entries, err := scanTree(ctx, localTree(root), true)
	if err != nil {
		return 0, err
	}
	if err = validateCopyTree(entries, local, remote); err != nil {
		return 0, err
	}
	if err = t.sameDirectorySession(id, identity); err != nil {
		return 0, err
	}
	if err = checkParents(c.Lstat, remoteParent); err != nil {
		return 0, err
	}
	if err = c.Mkdir(remote); err != nil {
		if _, statErr := c.Lstat(remote); statErr == nil {
			return 0, errors.New("远程已存在同名路径，请选择其他位置，或使用目录比较与同步")
		}
		return 0, err
	}
	plan := DirectoryPlan{session: id, identity: identity, LocalPath: local, RemotePath: remote, localRootInfo: info, local: entries}
	return t.queueTree(ctx, c, root, plan, "upload", "directory")
}

func (t *Transfers) DownloadDirectory(id, remote, localParent string) (int, error) {
	_, jobs, err := t.downloadDirectoryJobs(id, remote, localParent, "directory", "")
	return len(jobs), err
}

func (t *Transfers) downloadDirectoryJobs(id, remote, localParent, purpose, expectedIdentity string) (DirectoryPlan, []*transferJob, error) {
	parent, localParent, _, err := openedDirectory(localParent)
	if err != nil {
		return DirectoryPlan{}, nil, err
	}
	defer parent.Close()
	ctx, c, identity, release, err := t.directoryChannel(id)
	if err != nil {
		return DirectoryPlan{}, nil, err
	}
	defer release()
	if expectedIdentity != "" && expectedIdentity != identity {
		return DirectoryPlan{}, nil, errors.New("连接已变化，请重新准备拖出下载")
	}
	remote, err = remoteDirectory(c, remote)
	if err != nil {
		return DirectoryPlan{}, nil, err
	}
	name := path.Base(remote)
	if err := validateLocalTreeName(name); err != nil {
		return DirectoryPlan{}, nil, err
	}
	local := filepath.Join(localParent, name)
	entries, err := scanTree(ctx, remoteTree(ctx, c, remote), true)
	if err != nil {
		return DirectoryPlan{}, nil, err
	}
	if err = validateCopyTree(entries, local, remote); err != nil {
		return DirectoryPlan{}, nil, err
	}
	if err = t.sameDirectorySession(id, identity); err != nil {
		return DirectoryPlan{}, nil, err
	}
	if err = parent.Mkdir(name, 0o700); err != nil {
		if os.IsExist(err) {
			return DirectoryPlan{}, nil, errors.New("本地已存在同名路径，请选择其他位置，或使用目录比较与同步")
		}
		return DirectoryPlan{}, nil, err
	}
	root, err := parent.OpenRoot(name)
	if err != nil {
		return DirectoryPlan{}, nil, err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return DirectoryPlan{}, nil, err
	}
	current, err := parent.Lstat(name)
	if err != nil || !current.IsDir() || !os.SameFile(info, current) {
		return DirectoryPlan{}, nil, errors.New("本地下载目录已变化")
	}
	plan := DirectoryPlan{session: id, identity: identity, LocalPath: local, RemotePath: remote, localRootInfo: info, remote: entries}
	jobs, err := t.queueTreeJobs(ctx, c, root, plan, "download", purpose)
	return plan, jobs, err
}

type DirectoryDeletePlan struct {
	RemotePath        string
	Count             int
	session, identity string
	entries           map[string]fileMark
}

func (t *Transfers) PrepareDirectoryDelete(id, remote string) (DirectoryDeletePlan, error) {
	if !path.IsAbs(remote) || path.Clean(remote) != remote || remote == "/" {
		return DirectoryDeletePlan{}, errors.New("递归删除需要明确的绝对目录，不能删除根目录")
	}
	ctx, c, identity, release, err := t.directoryChannel(id)
	if err != nil {
		return DirectoryDeletePlan{}, err
	}
	defer release()
	resolved, err := remoteDirectory(c, remote)
	if err != nil {
		return DirectoryDeletePlan{}, err
	}
	if resolved != remote {
		return DirectoryDeletePlan{}, errors.New("不允许通过符号链接递归删除")
	}
	entries, err := scanTree(ctx, remoteTree(ctx, c, remote), false)
	if err != nil {
		return DirectoryDeletePlan{}, err
	}
	if err = t.sameDirectorySession(id, identity); err != nil {
		return DirectoryDeletePlan{}, err
	}
	return DirectoryDeletePlan{RemotePath: remote, Count: len(entries) + 1, session: id, identity: identity, entries: entries}, nil
}

func (t *Transfers) DeleteDirectoryTree(plan DirectoryDeletePlan) error {
	if plan.entries == nil || plan.RemotePath == "/" {
		return errors.New("无效的递归删除计划")
	}
	ctx, c, identity, release, err := t.directoryChannel(plan.session)
	if err != nil {
		return err
	}
	defer release()
	if identity != plan.identity {
		return errors.New("连接已变化，请重新确认删除")
	}
	remote, err := remoteDirectory(c, plan.RemotePath)
	if err != nil {
		return err
	}
	if remote != plan.RemotePath {
		return errors.New("远程目录已变化，请重新确认删除")
	}
	current, err := scanTree(ctx, remoteTree(ctx, c, remote), false)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(current, plan.entries) {
		return errors.New("远程目录内容已变化，请重新确认删除")
	}
	paths := treePaths(current, nil)
	sort.Slice(paths, func(i, j int) bool { return len(paths[i]) > len(paths[j]) })
	for _, rel := range paths {
		name := path.Join(remote, rel)
		mark := current[rel]
		if err := t.removeTreeEntry(ctx, c, plan, name, mark); err != nil {
			return fmt.Errorf("递归删除在 %q 停止，先前删除的项目无法撤销: %w", rel, err)
		}
	}
	return t.removeTreeEntry(ctx, c, plan, remote, fileMark{kind: "directory"})
}

func (t *Transfers) removeTreeEntry(ctx context.Context, c *sftp.Client, plan DirectoryDeletePlan, name string, expected fileMark) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.sameDirectorySession(plan.session, plan.identity); err != nil {
		return err
	}
	if err := checkParents(c.Lstat, path.Dir(name)); err != nil {
		return err
	}
	current, _, err := readFileMark(c.Lstat, name)
	if err != nil {
		return err
	}
	if current.kind != expected.kind || expected.kind != "directory" && current != expected {
		return errors.New("要删除的项目已变化")
	}
	if expected.kind == "directory" {
		return c.RemoveDirectory(name)
	}
	// Remove unlinks a symlink itself; traversal never follows the target.
	return c.Remove(name)
}
