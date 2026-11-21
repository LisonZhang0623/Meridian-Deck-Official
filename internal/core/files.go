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
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
)

const TextLimit = 2 * 1024 * 1024
const ImageLimit = 20 * 1024 * 1024

type Files struct {
	conns  Connections
	osc7Mu sync.Mutex
}

func NewFiles(conns Connections) *Files { return &Files{conns: conns} }

func (f *Files) client(id, remote string) (*sftp.Client, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	if err := ValidateRemotePath(remote); err != nil {
		return nil, err
	}
	return f.conns.SFTP(id)
}

func fileMode(info os.FileInfo) uint32 {
	if attrs, ok := info.Sys().(*sftp.FileStat); ok {
		return attrs.Mode
	}
	mode := uint32(info.Mode().Perm())
	if info.IsDir() {
		mode |= 0o040000
	} else if info.Mode()&os.ModeSymlink != 0 {
		mode |= 0o120000
	} else {
		mode |= 0o100000
	}
	return mode
}

func (f *Files) List(id, remote string) (Directory, error) {
	c, err := f.client(id, remote)
	if err != nil {
		return Directory{}, err
	}
	resolved, err := c.RealPath(remote)
	if err != nil {
		return Directory{}, err
	}
	if err = ValidateRemotePath(resolved); err != nil {
		return Directory{}, err
	}
	entries, err := c.ReadDir(resolved)
	if err != nil {
		return Directory{}, err
	}
	result := Directory{Path: resolved, Entries: make([]RemoteEntry, 0, len(entries))}
	var links []int
	for _, entry := range entries {
		if entry.Name() == "." || entry.Name() == ".." {
			continue
		}
		// A malformed server entry must not make the UI operate outside this directory.
		if entry.Name() == "" || strings.ContainsAny(entry.Name(), "/\x00") || !utf8.ValidString(entry.Name()) {
			continue
		}
		kind := "file"
		if entry.Mode()&os.ModeSymlink != 0 {
			kind = "link"
			links = append(links, len(result.Entries))
		} else if entry.IsDir() {
			kind = "directory"
		}
		result.Entries = append(result.Entries, RemoteEntry{Name: entry.Name(), Path: path.Join(resolved, entry.Name()), Type: kind, Size: entry.Size(), Modified: entry.ModTime().UnixMilli(), Mode: fileMode(entry)})
	}
	var workers sync.WaitGroup
	for worker := range min(len(links), 8) {
		workers.Go(func() {
			for index := worker; index < len(links); index += 8 {
				readLink(c, &result.Entries[links[index]])
			}
		})
	}
	workers.Wait()
	sort.Slice(result.Entries, func(i, j int) bool {
		a, b := result.Entries[i], result.Entries[j]
		if (a.Type == "directory") != (b.Type == "directory") {
			return a.Type == "directory"
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return result, nil
}

func readLink(c *sftp.Client, entry *RemoteEntry) {
	entry.LinkTarget, _ = c.ReadLink(entry.Path)
	info, err := c.Stat(entry.Path)
	switch {
	case os.IsNotExist(err):
		entry.TargetType = "missing"
	case err == nil && info.IsDir():
		entry.TargetType = "directory"
	case err == nil && info.Mode().IsRegular():
		entry.TargetType = "file"
	}
}

func (f *Files) HardLinks(id, directory string) ([]string, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	if err := ValidateRemotePath(directory); err != nil {
		return nil, err
	}
	if !path.IsAbs(directory) {
		return nil, errors.New("硬链接查询需要绝对目录路径")
	}
	client, err := f.conns.Client(id)
	if err != nil {
		return nil, err
	}
	// SFTP v3 attributes carry no hard-link count. Scan just the directory's
	// immediate children on an independent SSH channel when exec is available.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	command := "CDPATH= cd -- " + ShellQuote(directory) + ` && exec find . ! -name . -prune -type f -links +1 -print0`
	output, _, err := execBounded(ctx, client, command, 512*1024)
	if err != nil {
		return nil, err
	}
	names := []string{}
	for candidate := range strings.SplitSeq(output, "\x00") {
		name, ok := strings.CutPrefix(candidate, "./")
		if ok && name != "" && name != "." && name != ".." && !strings.ContainsRune(name, '/') && utf8.ValidString(name) {
			names = append(names, name)
		}
	}
	return names, nil
}

func boundedRead(c *sftp.Client, remote string, limit int64) ([]byte, os.FileInfo, error) {
	info, err := c.Stat(remote)
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, nil, errors.New("只能预览普通文件")
	}
	if info.Size() < 0 || info.Size() > limit {
		return nil, nil, fmt.Errorf("文件超过 %d MB 预览限制，请下载查看", limit/1024/1024)
	}
	r, err := c.Open(remote)
	if err != nil {
		return nil, nil, err
	}
	defer r.Close()
	data, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, nil, err
	}
	if int64(len(data)) > limit {
		return nil, nil, errors.New("文件大小超过预览限制")
	}
	return data, info, nil
}

func contentVersion(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func renameReplacing(c *sftp.Client, source, target string) error {
	err := c.PosixRename(source, target)
	if err == nil {
		return nil
	}
	var status *sftp.StatusError
	if (errors.As(err, &status) && status.Code == 8) || strings.Contains(strings.ToLower(err.Error()), "not support") {
		// Standard rename may reject an existing target. Never delete it as a fallback.
		return c.Rename(source, target)
	}
	return err
}

func resolveFileTarget(c *sftp.Client, remote string) (string, error) {
	for range 40 {
		if err := ValidateRemotePath(remote); err != nil {
			return "", err
		}
		info, err := c.Lstat(remote)
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink == 0 {
			return remote, nil
		}
		if resolved, err := c.RealPath(remote); err == nil && resolved != remote {
			remote = resolved
			continue
		}
		target, err := c.ReadLink(remote)
		if err != nil {
			return "", err
		}
		if !path.IsAbs(target) {
			parent, err := c.RealPath(path.Dir(remote))
			if err != nil {
				return "", err
			}
			target = parent + "/" + target
		}
		remote = target
	}
	return "", errors.New("符号链接链过长或存在循环")
}

func (f *Files) Save(id, remote, content, version string) (Preview, error) {
	c, err := f.client(id, remote)
	if err != nil {
		return Preview{}, err
	}
	// UTF-16 input can occupy up to 1.5 times as many bytes once decoded
	// into the renderer's UTF-8 payload. Check the encoded limit below.
	if len(content) > TextLimit*3/2 || !utf8.ValidString(content) || strings.ContainsRune(content, 0) {
		return Preview{}, errors.New("编辑后的文本过大或不是有效文本")
	}
	if len(version) != 64 {
		return Preview{}, errors.New("无效的文件版本")
	}
	requestedPath := remote
	remote, err = resolveFileTarget(c, requestedPath)
	if err != nil {
		return Preview{}, err
	}
	original, info, err := boundedRead(c, remote, TextLimit)
	if err != nil {
		return Preview{}, err
	}
	if contentVersion(original) != version {
		return Preview{}, errors.New("CONFLICT:远程文件已被修改，请重新加载并比较后再保存")
	}
	encoding := detectEncoding(original)
	if imageMIME(original) != "" {
		return Preview{}, errors.New("只能保存文本文件")
	}
	if _, err = encoding.decode(original, true); err != nil {
		return Preview{}, err
	}
	data := encoding.encode(content)
	if len(data) > TextLimit {
		return Preview{}, errors.New("编辑后的文件超过 2 MiB 保存限制")
	}
	temporary := path.Join(path.Dir(remote), "."+path.Base(remote)+".sshtool-"+NewID()+".tmp")
	w, err := c.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return Preview{}, err
	}
	defer c.Remove(temporary)
	if err = w.Chmod(info.Mode().Perm()); err == nil {
		_, err = w.Write(data)
	}
	closeErr := w.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return Preview{}, err
	}
	current, _, err := boundedRead(c, remote, TextLimit)
	if err != nil {
		return Preview{}, err
	}
	if contentVersion(current) != version {
		return Preview{}, errors.New("CONFLICT:保存期间远程文件发生变化，请重新加载")
	}
	if requestedPath != remote {
		resolved, err := resolveFileTarget(c, requestedPath)
		if err != nil || resolved != remote {
			return Preview{}, errors.New("CONFLICT:保存期间符号链接目标已变化，请重新加载")
		}
	}
	if err = renameReplacing(c, temporary, remote); err != nil {
		return Preview{}, err
	}
	return f.Preview(id, requestedPath)
}

func (f *Files) Create(id, parent, name string, directory bool) error {
	c, err := f.client(id, parent)
	if err != nil {
		return err
	}
	if err = ValidateEntryName(name); err != nil {
		return err
	}
	remote := path.Join(parent, name)
	if directory {
		return c.Mkdir(remote)
	}
	w, err := c.OpenFile(remote, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	if err != nil {
		return err
	}
	return w.Close()
}

func (f *Files) Rename(id, remote, name string) error {
	c, err := f.client(id, remote)
	if err != nil {
		return err
	}
	if err = ValidateEntryName(name); err != nil {
		return err
	}
	target := path.Join(path.Dir(remote), name)
	if target == remote {
		return nil
	}
	if _, err = c.Lstat(target); err == nil {
		return errors.New("目标名称已存在")
	} else if !os.IsNotExist(err) {
		return err
	}
	return c.Rename(remote, target)
}

func (f *Files) Delete(id, remote string, directory bool) error {
	c, err := f.client(id, remote)
	if err != nil {
		return err
	}
	if path.Clean(remote) == "/" || path.Clean(remote) == "." {
		return errors.New("不能删除根目录或当前目录")
	}
	info, err := c.Lstat(remote)
	if err != nil {
		return err
	}
	if directory {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return errors.New("目标不是普通目录")
		}
		return c.RemoveDirectory(remote)
	}
	if info.IsDir() {
		return errors.New("删除目录需要选择目录操作")
	}
	return c.Remove(remote)
}
