package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/pkg/sftp"
)

const (
	maxTreeEntries     = 2000
	maxTreeDepth       = 32
	maxComparisonBytes = 1024 * 1024 * 1024
	treeTimeout        = 90 * time.Second
)

type fileMark struct {
	kind     string
	size     int64
	modified int64
}

func markFile(info os.FileInfo) fileMark {
	if info == nil {
		return fileMark{}
	}
	kind := "unsupported"
	switch {
	case info.Mode().IsRegular() && info.Size() >= 0:
		kind = "file"
	case info.IsDir():
		kind = "directory"
	case info.Mode()&os.ModeSymlink != 0:
		kind = "link"
	}
	return fileMark{kind, info.Size(), info.ModTime().UnixNano()}
}

func readFileMark(lstat func(string) (os.FileInfo, error), name string) (fileMark, os.FileInfo, error) {
	info, err := lstat(name)
	if os.IsNotExist(err) {
		return fileMark{}, nil, nil
	}
	if err != nil {
		return fileMark{}, nil, err
	}
	return markFile(info), info, nil
}

func checkParents(lstat func(string) (os.FileInfo, error), name string) error {
	for {
		info, err := lstat(name)
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("目标父目录已变化，不能穿过符号链接")
		}
		parent := path.Dir(name)
		if parent == name {
			return nil
		}
		name = parent
	}
}

type transferGuard struct {
	identity                 string
	localRoot, localRelative string
	localRootInfo            os.FileInfo
	local, remote            fileMark
	replace                  bool
	scoped                   bool
}

func (job *transferJob) checkRemoteGuard(c *sftp.Client) error {
	return checkParents(c.Lstat, path.Dir(job.transfer.RemotePath))
}

type treeAccess struct {
	lstat   func(string) (os.FileInfo, error)
	readDir func(string) ([]os.FileInfo, error)
	open    func(string) (io.ReadCloser, error)
}

func remoteTree(ctx context.Context, c *sftp.Client, base string) treeAccess {
	return treeAccess{
		lstat:   func(rel string) (os.FileInfo, error) { return c.Lstat(path.Join(base, rel)) },
		readDir: func(rel string) ([]os.FileInfo, error) { return c.ReadDirContext(ctx, path.Join(base, rel)) },
		open:    func(rel string) (io.ReadCloser, error) { return c.Open(path.Join(base, rel)) },
	}
}

func localTree(root *os.Root) treeAccess {
	return treeAccess{
		lstat: func(rel string) (os.FileInfo, error) { return root.Lstat(filepath.FromSlash(rel)) },
		readDir: func(rel string) ([]os.FileInfo, error) {
			d, err := root.Open(filepath.FromSlash(rel))
			if err != nil {
				return nil, err
			}
			defer d.Close()
			info, err := d.Stat()
			if err != nil || !info.IsDir() {
				return nil, errors.New("本地目录在读取期间发生变化")
			}
			entries, err := d.Readdir(maxTreeEntries + 1)
			if err == io.EOF {
				err = nil
			}
			return entries, err
		},
		open: func(rel string) (io.ReadCloser, error) { return root.Open(filepath.FromSlash(rel)) },
	}
}

var transferStageName = regexp.MustCompile(`^\.sshtool-[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}\.part$`)

func scanTree(ctx context.Context, side treeAccess, skipStaging bool) (map[string]fileMark, error) {
	entries := make(map[string]fileMark)
	var walk func(string, int) error
	walk = func(rel string, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > maxTreeDepth {
			return errors.New("目录层级超过 32 层")
		}
		if err := checkParents(side.lstat, rel); err != nil {
			return err
		}
		items, err := side.readDir(rel)
		if err != nil {
			return err
		}
		if len(items) > maxTreeEntries {
			return errors.New("单个目录最多扫描 2000 项")
		}
		for _, item := range items {
			name := item.Name()
			if name == "." || name == ".." || skipStaging && transferStageName.MatchString(name) {
				continue
			}
			if err := ValidateEntryName(name); err != nil {
				return fmt.Errorf("目录中有无效文件名 %q", name)
			}
			child := path.Join(rel, name)
			if _, exists := entries[child]; exists {
				return fmt.Errorf("目录返回重复路径 %q", child)
			}
			if len(entries) >= maxTreeEntries {
				return errors.New("单个目录树最多处理 2000 项")
			}
			info, err := side.lstat(child)
			if err != nil {
				return err
			}
			mark := markFile(info)
			entries[child] = mark
			if mark.kind == "directory" {
				if err = walk(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return entries, walk(".", 0)
}

func openedDirectory(name string) (*os.Root, string, os.FileInfo, error) {
	if !filepath.IsAbs(name) || len(name) > 32768 {
		return nil, "", nil, errors.New("请选择有效本地目录")
	}
	resolved, err := filepath.EvalSymlinks(name)
	if err != nil {
		return nil, "", nil, err
	}
	root, err := os.OpenRoot(resolved)
	if err != nil {
		return nil, "", nil, err
	}
	info, err := root.Stat(".")
	if err != nil || !info.IsDir() {
		root.Close()
		return nil, "", nil, errors.New("所选本地路径不是目录")
	}
	return root, resolved, info, nil
}

func remoteDirectory(c *sftp.Client, name string) (string, error) {
	if err := ValidateRemotePath(name); err != nil {
		return "", err
	}
	info, err := c.Lstat(name)
	if err != nil {
		return "", err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("请选择远程普通目录")
	}
	resolved, err := c.RealPath(name)
	if err != nil {
		return "", err
	}
	if !path.IsAbs(resolved) || path.Clean(resolved) != resolved {
		return "", errors.New("服务器返回了无效的目录路径")
	}
	if err := ValidateRemotePath(resolved); err != nil {
		return "", err
	}
	if err := checkParents(c.Lstat, resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

func (t *Transfers) directoryChannel(id string) (context.Context, *sftp.Client, string, func(), error) {
	if err := ValidateID(id); err != nil {
		return nil, nil, "", nil, err
	}
	identity, err := t.conns.Identity(id)
	if err != nil {
		return nil, nil, "", nil, err
	}
	sshClient, err := t.conns.Client(id)
	if err != nil {
		return nil, nil, "", nil, err
	}
	if sshClient == nil {
		return nil, nil, "", nil, errors.New("SSH 连接不可用")
	}
	ctx, cancel := context.WithTimeout(context.Background(), treeTimeout)
	c, err := openSFTP(ctx, sshClient)
	if err != nil {
		cancel()
		return nil, nil, "", nil, err
	}
	release := func() { cancel(); c.Close() }
	if err = t.sameDirectorySession(id, identity); err != nil {
		release()
		return nil, nil, "", nil, err
	}
	return ctx, c, identity, release, nil
}

func (t *Transfers) sameDirectorySession(id, identity string) error {
	current, err := t.conns.Identity(id)
	if err != nil {
		return err
	}
	if current != identity {
		return errors.New("连接已变化，请重新选择目录")
	}
	return nil
}

type DirectoryChange struct {
	Path       string `json:"path"`
	Status     string `json:"status"`
	LocalType  string `json:"localType,omitempty"`
	RemoteType string `json:"remoteType,omitempty"`
	LocalSize  int64  `json:"localSize,omitempty"`
	RemoteSize int64  `json:"remoteSize,omitempty"`
}

type DirectoryPlan struct {
	LocalPath         string            `json:"localPath"`
	RemotePath        string            `json:"remotePath"`
	Changes           []DirectoryChange `json:"changes"`
	session, identity string
	localRootInfo     os.FileInfo
	local, remote     map[string]fileMark
	fingerprints      map[string][2]string
}

func hashTreeFile(ctx context.Context, tree treeAccess, rel string, mark fileMark) (string, error) {
	if err := checkParents(tree.lstat, path.Dir(rel)); err != nil {
		return "", err
	}
	info, err := tree.lstat(rel)
	if err != nil || markFile(info) != mark {
		return "", fmt.Errorf("文件 %q 在比较期间发生变化", rel)
	}
	r, err := tree.open(rel)
	if err != nil {
		return "", err
	}
	defer r.Close()
	h := sha256.New()
	n, err := io.CopyBuffer(h, io.LimitReader(contextReader{ctx, r}, mark.size+1), make([]byte, transferBufferSize))
	if err != nil {
		return "", err
	}
	info, err = tree.lstat(rel)
	if err != nil || n != mark.size || markFile(info) != mark {
		return "", fmt.Errorf("文件 %q 在比较期间发生变化", rel)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func treePaths(first, second map[string]fileMark) []string {
	paths := make([]string, 0, len(first)+len(second))
	for rel := range first {
		paths = append(paths, rel)
	}
	for rel := range second {
		if _, exists := first[rel]; !exists {
			paths = append(paths, rel)
		}
	}
	sort.Strings(paths)
	return paths
}

func compareTrees(ctx context.Context, local, remote treeAccess, plan *DirectoryPlan) error {
	var hashBudget int64 = maxComparisonBytes
	plan.Changes = []DirectoryChange{}
	plan.fingerprints = make(map[string][2]string)
	paths := treePaths(plan.local, plan.remote)
	collisions := caseCollisions(paths)
	for _, rel := range paths {
		a, b := plan.local[rel], plan.remote[rel]
		status := "same"
		switch {
		case collisions[rel], unsupportedTreeEntry(a), unsupportedTreeEntry(b), a.kind != "" && b.kind != "" && a.kind != b.kind:
			status = "conflict"
		case a.kind == "":
			status = "remote-only"
		case b.kind == "":
			status = "local-only"
		case a.kind == "file" && a.size != b.size:
			status = "different"
		case a.kind == "file":
			if a.size > hashBudget/2 {
				return errors.New("同大小文件的内容比对超过 1 GiB，请缩小比较目录")
			}
			hashBudget -= 2 * a.size
			localHash, err := hashTreeFile(ctx, local, rel, a)
			if err != nil {
				return err
			}
			remoteHash, err := hashTreeFile(ctx, remote, rel, b)
			if err != nil {
				return err
			}
			plan.fingerprints[rel] = [2]string{localHash, remoteHash}
			if localHash != remoteHash {
				status = "different"
			}
		}
		if status != "same" {
			plan.Changes = append(plan.Changes, DirectoryChange{Path: rel, Status: status, LocalType: a.kind, RemoteType: b.kind, LocalSize: a.size, RemoteSize: b.size})
		}
	}
	return nil
}

func unsupportedTreeEntry(mark fileMark) bool {
	return mark.kind != "" && mark.kind != "file" && mark.kind != "directory"
}

func caseCollisions(paths []string) map[string]bool {
	collisions := map[string]bool{}
	if runtime.GOOS == "windows" || runtime.GOOS == "darwin" {
		seen := map[string]string{}
		for _, rel := range paths {
			key := strings.ToLower(rel)
			if other, exists := seen[key]; exists && other != rel {
				collisions[rel], collisions[other] = true, true
			}
			seen[key] = rel
		}
	}
	return collisions
}

func buildDirectoryPlan(ctx context.Context, root *os.Root, localRoot, remoteRoot string, localInfo os.FileInfo, c *sftp.Client, id, identity string) (DirectoryPlan, error) {
	l, r := localTree(root), remoteTree(ctx, c, remoteRoot)
	local, err := scanTree(ctx, l, true)
	if err != nil {
		return DirectoryPlan{}, err
	}
	remote, err := scanTree(ctx, r, true)
	if err != nil {
		return DirectoryPlan{}, err
	}
	plan := DirectoryPlan{LocalPath: localRoot, RemotePath: remoteRoot, localRootInfo: localInfo, session: id, identity: identity, local: local, remote: remote}
	err = compareTrees(ctx, l, r, &plan)
	return plan, err
}

func (t *Transfers) CompareDirectory(id, local, remote string) (DirectoryPlan, error) {
	root, local, info, err := openedDirectory(local)
	if err != nil {
		return DirectoryPlan{}, err
	}
	defer root.Close()
	ctx, c, identity, release, err := t.directoryChannel(id)
	if err != nil {
		return DirectoryPlan{}, err
	}
	defer release()
	remote, err = remoteDirectory(c, remote)
	if err != nil {
		return DirectoryPlan{}, err
	}
	plan, err := buildDirectoryPlan(ctx, root, local, remote, info, c, id, identity)
	if err == nil {
		err = t.sameDirectorySession(id, identity)
	}
	return plan, err
}

func (t *Transfers) SyncDirectory(plan DirectoryPlan, direction string) (int, error) {
	if direction != "upload" && direction != "download" {
		return 0, errors.New("请选择单向同步方向")
	}
	for _, item := range plan.Changes {
		if item.Status == "conflict" {
			return 0, errors.New("请先处理类型、文件名或符号链接冲突")
		}
	}
	root, local, info, err := openedDirectory(plan.LocalPath)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	if local != plan.LocalPath || !os.SameFile(info, plan.localRootInfo) {
		return 0, errors.New("本地目录已变化，请重新比较")
	}
	ctx, c, identity, release, err := t.directoryChannel(plan.session)
	if err != nil {
		return 0, err
	}
	defer release()
	if identity != plan.identity {
		return 0, errors.New("连接已变化，请重新比较")
	}
	remote, err := remoteDirectory(c, plan.RemotePath)
	if err != nil {
		return 0, err
	}
	if remote != plan.RemotePath {
		return 0, errors.New("远程目录已变化，请重新比较")
	}
	current, err := buildDirectoryPlan(ctx, root, local, remote, info, c, plan.session, identity)
	if err != nil {
		return 0, err
	}
	if !reflect.DeepEqual(current.local, plan.local) || !reflect.DeepEqual(current.remote, plan.remote) || !reflect.DeepEqual(current.fingerprints, plan.fingerprints) {
		return 0, errors.New("目录内容已变化，请重新比较后再同步")
	}
	return t.queueTree(ctx, c, root, plan, direction, "sync")
}

func validateLocalTreeName(name string) error {
	if runtime.GOOS == "windows" {
		_, err := dragFileName(name)
		return err
	}
	return ValidateEntryName(name)
}

func (t *Transfers) queueTree(ctx context.Context, c *sftp.Client, root *os.Root, plan DirectoryPlan, direction, purpose string) (int, error) {
	jobs, err := t.queueTreeJobs(ctx, c, root, plan, direction, purpose)
	return len(jobs), err
}

func treeTransferSpecs(plan DirectoryPlan, direction, purpose string) ([]string, []transferSpec, error) {
	source, target := plan.local, plan.remote
	if direction == "download" {
		source, target = target, source
	}
	paths := treePaths(source, nil)
	if len(caseCollisions(paths)) > 0 {
		return nil, nil, errors.New("目录包含仅大小写不同的文件名")
	}
	var directories []string
	var specs []transferSpec
	for _, rel := range paths {
		src, dest := source[rel], target[rel]
		if unsupportedTreeEntry(src) || unsupportedTreeEntry(dest) || dest.kind != "" && dest.kind != src.kind {
			return nil, nil, fmt.Errorf("不能复制特殊文件或覆盖不同类型的路径 %q", rel)
		}
		if err := validateLocalTreeName(path.Base(rel)); err != nil {
			return nil, nil, err
		}
		localRel := filepath.FromSlash(rel)
		localFile, remoteFile := filepath.Join(plan.LocalPath, localRel), path.Join(plan.RemotePath, rel)
		if err := ValidateRemotePath(remoteFile); err != nil || len(localFile) > 32768 {
			return nil, nil, errors.New("目录内的文件路径过长或无效")
		}
		if src.kind == "directory" {
			if dest.kind == "" {
				directories = append(directories, rel)
			}
			continue
		}
		if pair, exists := plan.fingerprints[rel]; exists && pair[0] == pair[1] {
			continue
		}
		guard := &transferGuard{identity: plan.identity, localRoot: plan.LocalPath, localRelative: localRel, localRootInfo: plan.localRootInfo, local: plan.local[rel], remote: plan.remote[rel], replace: purpose == "sync"}
		spec := transferSpec{localPath: localFile, remotePath: remoteFile, guard: guard}
		if purpose == "drag" {
			spec.dragRoot = plan.RemotePath
		}
		specs = append(specs, spec)
	}
	return directories, specs, nil
}

func (t *Transfers) queueTreeJobs(ctx context.Context, c *sftp.Client, root *os.Root, plan DirectoryPlan, direction, purpose string) ([]*transferJob, error) {
	directories, specs, err := treeTransferSpecs(plan, direction, purpose)
	if err != nil {
		return nil, err
	}
	for _, rel := range directories {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := t.sameDirectorySession(plan.session, plan.identity); err != nil {
			return nil, err
		}
		if direction == "upload" {
			dir := path.Join(plan.RemotePath, rel)
			if err := checkParents(c.Lstat, path.Dir(dir)); err != nil {
				return nil, err
			}
			if err := c.Mkdir(dir); err != nil {
				return nil, err
			}
		} else {
			if err := checkParents(root.Lstat, path.Dir(rel)); err != nil {
				return nil, err
			}
			if err := root.Mkdir(filepath.FromSlash(rel), 0o700); err != nil {
				return nil, err
			}
		}
	}
	if err := t.sameDirectorySession(plan.session, plan.identity); err != nil {
		return nil, err
	}
	return t.enqueue(plan.session, direction, purpose, specs)
}
