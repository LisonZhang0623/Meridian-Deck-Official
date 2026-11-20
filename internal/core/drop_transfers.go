package core

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/pkg/sftp"
)

// LocalDropEntry pairs a path with its identity at the time of the native drop.
// Info must come from an open handle or have been checked with os.SameFile
// during the drop, since Windows can load file IDs lazily from the path.
type LocalDropEntry struct {
	Path string
	Info os.FileInfo
}

type dropBatch struct {
	directories []string
	specs       []transferSpec
	names       map[string]bool
}

func (batch *dropBatch) claim(name string) error {
	key := strings.ToLower(name)
	if batch.names[key] {
		return fmt.Errorf("拖拽选择中有同名路径 %q", name)
	}
	batch.names[key] = true
	return nil
}

func (batch *dropBatch) checkSize() error {
	if len(batch.directories)+len(batch.specs) > maxTreeEntries {
		return errors.New("每批拖拽最多处理 2000 个文件和文件夹")
	}
	return nil
}

func (batch *dropBatch) addTree(plan DirectoryPlan, direction string, scope *os.Root, scopeInfo os.FileInfo, scopePath string) error {
	directories, specs, err := treeTransferSpecs(plan, direction, "")
	if err != nil {
		return err
	}
	for i := range specs {
		spec := &specs[i]
		guard := spec.guard
		guard.localRelative = filepath.Join(scopePath, guard.localRelative)
		guard.localRoot, guard.localRootInfo, guard.scoped = scope.Name(), scopeInfo, true
		if direction == "upload" {
			info, err := scope.Lstat(guard.localRelative)
			if err != nil {
				return err
			}
			if markFile(info) != guard.local {
				return errors.New("文件夹中的源文件已变化，请重新拖拽")
			}
			spec.nasSource = &nasUploadSource{root: scope.Name(), relative: guard.localRelative, rootInfo: scopeInfo, source: info}
		}
	}
	destination := filepath.ToSlash(scopePath)
	if direction == "upload" {
		destination = plan.RemotePath
	}
	batch.directories = append(batch.directories, destination)
	for _, relative := range directories {
		batch.directories = append(batch.directories, path.Join(destination, relative))
	}
	batch.specs = append(batch.specs, specs...)
	return batch.checkSize()
}

func (t *Transfers) UploadDropped(id, remote string, entries []LocalDropEntry) error {
	return t.uploadDropEntries(id, remote, nil, entries)
}

// UploadFromRoot queues files and folders under a native-selected NAS root.
// Workers reopen and check the selected root before reading any bytes.
func (t *Transfers) UploadFromRoot(id, remote string, root *os.Root, paths []string) error {
	if root == nil || !filepath.IsAbs(root.Name()) {
		return errors.New("请先选择 NAS 文件夹")
	}
	entries := make([]LocalDropEntry, 0, len(paths))
	for _, relative := range paths {
		entries = append(entries, LocalDropEntry{Path: relative})
	}
	return t.uploadDropEntries(id, remote, root, entries)
}

func (t *Transfers) uploadDropEntries(id, remote string, root *os.Root, entries []LocalDropEntry) error {
	if len(entries) == 0 || len(entries) > 100 {
		return errors.New("请选择 1–100 个文件或文件夹")
	}
	ctx, client, identity, release, err := t.directoryChannel(id)
	if err != nil {
		return err
	}
	defer release()
	remote, err = remoteDirectory(client, remote)
	if err != nil {
		return err
	}
	batch := dropBatch{names: make(map[string]bool, len(entries))}
	for _, entry := range entries {
		if err = batch.planUpload(ctx, client, id, identity, remote, root, entry); err != nil {
			return err
		}
	}
	for _, directory := range batch.directories {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = t.sameDirectorySession(id, identity); err != nil {
			return err
		}
		if err = checkParents(client.Lstat, path.Dir(directory)); err != nil {
			return err
		}
		if err = client.Mkdir(directory); err != nil {
			return err
		}
	}
	if err = t.sameDirectorySession(id, identity); err != nil {
		return err
	}
	purpose := ""
	if len(batch.directories) != 0 {
		purpose = "directory"
	}
	_, err = t.enqueue(id, "upload", purpose, batch.specs)
	return err
}

func (batch *dropBatch) planUpload(ctx context.Context, client *sftp.Client, id, identity, remoteParent string, scope *os.Root, entry LocalDropEntry) error {
	relative := entry.Path
	if scope == nil {
		if !filepath.IsAbs(entry.Path) || len(entry.Path) > 32768 || entry.Info == nil {
			return errors.New("拖入的本地路径无效")
		}
		var err error
		scope, err = os.OpenRoot(filepath.Dir(entry.Path))
		if err != nil {
			return err
		}
		defer scope.Close()
		relative = filepath.Base(entry.Path)
	}
	local, err := LocalizeNASPath(relative)
	if err != nil {
		return err
	}
	if _, err = CheckNASDirectory(scope, path.Dir(relative)); err != nil {
		return err
	}
	scopeInfo, err := scope.Stat(".")
	if err != nil {
		return err
	}
	info, err := scope.Lstat(local)
	if err != nil {
		return err
	}
	if entry.Info != nil && !os.SameFile(entry.Info, info) {
		return errors.New("拖入的本地路径已变化，请重新拖入")
	}
	name := path.Base(relative)
	if err = batch.claim(name); err != nil {
		return err
	}
	remote := path.Join(remoteParent, name)
	if err = ValidateRemotePath(remote); err != nil {
		return err
	}
	localPath := filepath.Join(scope.Name(), local)
	if len(localPath) > 32768 {
		return errors.New("本地文件路径过长")
	}
	target, _, err := readFileMark(client.Lstat, remote)
	if err != nil {
		return err
	}
	if info.Mode().IsRegular() {
		if target.kind != "" && target.kind != "file" {
			return fmt.Errorf("远程已有同名目录或特殊文件 %q", name)
		}
		source := &nasUploadSource{root: scope.Name(), relative: local, rootInfo: scopeInfo, source: info}
		guard := &transferGuard{identity: identity, localRoot: scope.Name(), localRelative: local, localRootInfo: scopeInfo, local: markFile(info), remote: target, scoped: true}
		batch.specs = append(batch.specs, transferSpec{localPath: localPath, remotePath: remote, guard: guard, nasSource: source})
		return batch.checkSize()
	}
	if info.Mode().Type() != os.ModeDir {
		return errors.New("拖拽上传仅支持普通文件和文件夹")
	}
	if target.kind != "" {
		return fmt.Errorf("远程已存在同名路径 %q，请选择其他位置", name)
	}
	root, err := scope.OpenRoot(local)
	if err != nil {
		return err
	}
	defer root.Close()
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		return errors.New("拖入的文件夹已变化，请重新拖入")
	}
	entries, err := scanTree(ctx, localTree(root), true)
	if err != nil {
		return err
	}
	plan := DirectoryPlan{session: id, identity: identity, LocalPath: localPath, RemotePath: remote, localRootInfo: info, local: entries}
	return batch.addTree(plan, "upload", scope, scopeInfo, local)
}

// DownloadToRoot sends remote entries into a selected NAS directory. The queued
// jobs continue to use the selected root as their filesystem boundary.
func (t *Transfers) DownloadToRoot(id string, remotes []string, root *os.Root, relative string) error {
	if len(remotes) == 0 || len(remotes) > 100 || root == nil || !filepath.IsAbs(root.Name()) {
		return errors.New("请选择 1–100 个远程文件或文件夹及 NAS 目标")
	}
	parentPath := "."
	if relative != "" {
		var err error
		if parentPath, err = LocalizeNASPath(relative); err != nil {
			return err
		}
	}
	parentInfo, err := CheckNASDirectory(root, relative)
	if err != nil {
		return err
	}
	rootInfo, err := root.Stat(".")
	if err != nil {
		return err
	}
	ctx, client, identity, release, err := t.directoryChannel(id)
	if err != nil {
		return err
	}
	defer release()
	batch := dropBatch{names: make(map[string]bool, len(remotes))}
	for _, remote := range remotes {
		if err = batch.planDownload(ctx, client, id, identity, remote, root, rootInfo, parentPath); err != nil {
			return err
		}
	}
	parent, err := root.OpenRoot(parentPath)
	if err != nil {
		return err
	}
	defer parent.Close()
	if opened, err := parent.Stat("."); err != nil || !os.SameFile(parentInfo, opened) {
		return errors.New("NAS 目标文件夹已变化，请重新拖拽")
	}
	for _, directory := range batch.directories {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = t.sameDirectorySession(id, identity); err != nil {
			return err
		}
		local, err := filepath.Rel(parentPath, filepath.FromSlash(directory))
		if err != nil || !filepath.IsLocal(local) {
			return errors.New("NAS 目标目录超出所选范围")
		}
		if _, err = CheckNASDirectory(parent, filepath.ToSlash(filepath.Dir(local))); err != nil {
			return err
		}
		if err = parent.Mkdir(local, 0700); err != nil {
			return err
		}
	}
	if err = t.sameDirectorySession(id, identity); err != nil {
		return err
	}
	_, err = t.enqueue(id, "download", "nas", batch.specs)
	return err
}

func (batch *dropBatch) planDownload(ctx context.Context, client *sftp.Client, id, identity, remote string, root *os.Root, rootInfo os.FileInfo, parent string) error {
	if err := ValidateRemotePath(remote); err != nil {
		return err
	}
	if !path.IsAbs(remote) || path.Clean(remote) != remote || remote == "/" {
		return errors.New("请选择明确的远程文件或文件夹")
	}
	name := path.Base(remote)
	if err := validateLocalTreeName(name); err != nil {
		return err
	}
	if err := batch.claim(name); err != nil {
		return err
	}
	if err := checkParents(client.Lstat, path.Dir(remote)); err != nil {
		return err
	}
	info, err := client.Lstat(remote)
	if err != nil {
		return err
	}
	local := filepath.Join(parent, name)
	localPath := filepath.Join(root.Name(), local)
	if len(localPath) > 32768 {
		return errors.New("NAS 目标文件路径过长")
	}
	if target, _, err := readFileMark(root.Lstat, local); err != nil {
		return err
	} else if target.kind != "" {
		return fmt.Errorf("NAS 已存在同名路径 %q，请选择其他位置", name)
	}
	if info.Mode().IsRegular() {
		guard := &transferGuard{identity: identity, localRoot: root.Name(), localRelative: local, localRootInfo: rootInfo, remote: markFile(info), scoped: true}
		batch.specs = append(batch.specs, transferSpec{localPath: localPath, remotePath: remote, guard: guard})
		return batch.checkSize()
	}
	if info.Mode().Type() != os.ModeDir {
		return errors.New("拖拽下载仅支持普通文件和文件夹")
	}
	entries, err := scanTree(ctx, remoteTree(ctx, client, remote), true)
	if err != nil {
		return err
	}
	plan := DirectoryPlan{session: id, identity: identity, LocalPath: localPath, RemotePath: remote, remote: entries}
	return batch.addTree(plan, "download", root, rootInfo, local)
}
