package core

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/pkg/sftp"
)

const dragLifetime = 10 * time.Minute

var reservedDragName = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9¹²³]|lpt[1-9¹²³])(?:\.|$)`)

type cachedDrag struct {
	public                                        DragDownload
	sessionID, identity, remote, local, directory string
	size                                          int64
	remoteTime, localTime                         time.Time
	folder                                        *dragFolderSnapshot
	used                                          bool
}
type dragPending struct {
	done   chan struct{}
	result DragDownload
	err    error
}
type DragDownloads struct {
	root, run string
	runLease  *os.File
	conns     Connections
	transfers *Transfers
	mu        sync.Mutex
	closed    bool
	files     map[string]cachedDrag
	pending   map[string]*dragPending
	wg        sync.WaitGroup
	now       func() time.Time
}

func NewDragDownloads(root string, conns Connections, transfers *Transfers) *DragDownloads {
	root = filepath.Clean(root)
	reclaimDragRuns(root)
	return &DragDownloads{root: root, conns: conns, transfers: transfers, files: make(map[string]cachedDrag), pending: make(map[string]*dragPending), now: time.Now}
}

func dragFileName(remote string) (string, error) {
	if err := ValidateRemotePath(remote); err != nil {
		return "", err
	}
	name := path.Base(remote)
	if name == "" || name == "." || name == ".." || utf8.RuneCountInString(name) > 255 || strings.ContainsAny(name, `<>:"/\|?*`) || strings.HasSuffix(name, ".") || strings.HasSuffix(name, " ") || reservedDragName.MatchString(name) {
		return "", errors.New("此文件名不适合拖出到 Windows，请使用下载文件另选名称")
	}
	for _, r := range name {
		if r < 32 {
			return "", errors.New("此文件名不适合拖出到 Windows，请使用下载文件另选名称")
		}
	}
	return name, nil
}

func regularDragFile(c *sftp.Client, remote string) (os.FileInfo, error) {
	info, err := c.Lstat(remote)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("拖出下载仅支持普通文件，文件夹和符号链接请使用其他操作")
	}
	if info.Size() < 0 {
		return nil, errors.New("远程文件大小无效")
	}
	return info, nil
}

func (d *DragDownloads) assertIdentity(id, identity string) error {
	d.mu.Lock()
	closed := d.closed
	d.mu.Unlock()
	if closed {
		return errors.New("拖出下载已停止")
	}
	current, err := d.conns.Identity(id)
	if err != nil {
		return err
	}
	if current != identity {
		return errors.New("连接已变化，请重新准备拖出下载")
	}
	return nil
}

func (d *DragDownloads) Prepare(id, remote string) (DragDownload, error) {
	return d.prepareDrag(id, remote, false)
}

func (d *DragDownloads) PrepareDirectory(id, remote string) (DragDownload, error) {
	return d.prepareDrag(id, remote, true)
}

func (d *DragDownloads) prepareDrag(id, remote string, folder bool) (DragDownload, error) {
	if err := ValidateID(id); err != nil {
		return DragDownload{}, err
	}
	if err := ValidateRemotePath(remote); err != nil {
		return DragDownload{}, err
	}
	identity, err := d.conns.Identity(id)
	if err != nil {
		return DragDownload{}, err
	}
	key := id + "\x00" + identity + "\x00" + remote
	if folder {
		key += "\x00directory"
	}
	d.mu.Lock()
	if d.closed {
		d.mu.Unlock()
		return DragDownload{}, errors.New("拖出下载已停止")
	}
	if p := d.pending[key]; p != nil {
		d.mu.Unlock()
		<-p.done
		return p.result, p.err
	}
	p := &dragPending{done: make(chan struct{})}
	d.pending[key] = p
	d.wg.Add(1)
	d.mu.Unlock()
	prepare := d.prepare
	if folder {
		prepare = d.prepareDirectory
	}
	result, err := prepare(id, identity, remote)
	d.mu.Lock()
	p.result, p.err = result, err
	delete(d.pending, key)
	close(p.done)
	d.mu.Unlock()
	d.wg.Done()
	return result, err
}

func (d *DragDownloads) prepare(id, identity, remote string) (DragDownload, error) {
	name, err := dragFileName(remote)
	if err != nil {
		return DragDownload{}, err
	}
	c, err := d.conns.SFTP(id)
	if err != nil {
		return DragDownload{}, err
	}
	source, err := regularDragFile(c, remote)
	if err != nil {
		return DragDownload{}, err
	}
	if !filepath.IsAbs(d.root) {
		return DragDownload{}, errors.New("拖出缓存目录必须是绝对路径")
	}
	if err = d.assertIdentity(id, identity); err != nil {
		return DragDownload{}, err
	}
	d.mu.Lock()
	err = d.ensureRun()
	if err != nil {
		d.mu.Unlock()
		return DragDownload{}, err
	}
	var stale []string
	for token, entry := range d.files {
		if entry.folder != nil || entry.sessionID != id || entry.identity != identity || entry.remote != remote {
			continue
		}
		if entry.public.ExpiresAt > d.now().UnixMilli() && entry.size == source.Size() && entry.remoteTime.Equal(source.ModTime()) && d.localMatches(entry) {
			d.mu.Unlock()
			return entry.public, nil
		}
		delete(d.files, token)
		stale = append(stale, entry.directory)
	}
	run := d.run
	d.mu.Unlock()
	for _, directory := range stale {
		if err = d.removeItem(directory, run); err != nil {
			return DragDownload{}, err
		}
	}
	directory, err := os.MkdirTemp(run, "file-")
	if err != nil {
		return DragDownload{}, err
	}
	local := filepath.Join(directory, name)
	retained := false
	defer func() {
		if !retained {
			_ = d.removeItem(directory, run)
		}
	}()
	if err = d.assertIdentity(id, identity); err != nil {
		return DragDownload{}, err
	}
	task, err := d.transfers.Enqueue(id, "download", local, remote, "drag")
	if err != nil {
		return DragDownload{}, err
	}
	if err = d.transfers.Wait(task); err != nil {
		return DragDownload{}, err
	}
	if err = d.assertIdentity(id, identity); err != nil {
		return DragDownload{}, err
	}
	current, err := regularDragFile(c, remote)
	if err != nil {
		return DragDownload{}, err
	}
	if current.Size() != source.Size() || !current.ModTime().Equal(source.ModTime()) {
		return DragDownload{}, errors.New("源文件在下载期间发生变化，请重新准备拖出")
	}
	info, err := os.Lstat(local)
	if err != nil {
		return DragDownload{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() != source.Size() {
		return DragDownload{}, errors.New("拖出缓存文件不完整，请重新准备")
	}
	if err = d.assertIdentity(id, identity); err != nil {
		return DragDownload{}, err
	}
	entry := cachedDrag{public: DragDownload{Token: NewID(), Name: name, ExpiresAt: d.now().Add(dragLifetime).UnixMilli()}, sessionID: id, identity: identity, remote: remote, local: local, directory: directory, size: source.Size(), remoteTime: source.ModTime(), localTime: info.ModTime()}
	result, err := d.remember(entry)
	retained = err == nil
	return result, err
}

// ensureRun is called with d.mu held.
func (d *DragDownloads) ensureRun() error {
	if d.closed {
		return errors.New("拖出下载已停止")
	}
	if d.run == "" {
		var err error
		d.run, d.runLease, err = createDragRun(d.root)
		return err
	}
	return nil
}

func (d *DragDownloads) remember(entry cachedDrag) (DragDownload, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return DragDownload{}, errors.New("拖出下载已停止")
	}
	d.files[entry.public.Token] = entry
	return entry.public, nil
}

// localMatches is called while d.mu is held, so the run directory cannot change.
func (d *DragDownloads) localMatches(entry cachedDrag) bool {
	if d.run == "" || filepath.Dir(entry.directory) != d.run || filepath.Dir(entry.local) != entry.directory {
		return false
	}
	for _, directory := range []string{d.run, entry.directory} {
		info, err := os.Lstat(directory)
		if err != nil || info.Mode().Type() != os.ModeDir {
			return false
		}
	}
	if entry.folder != nil {
		return entry.folder.matches(entry.local)
	}
	info, err := os.Lstat(entry.local)
	return err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 && info.Size() == entry.size && info.ModTime().Equal(entry.localTime)
}

func (d *DragDownloads) File(id, token string) (string, error) {
	if err := ValidateID(id); err != nil {
		return "", err
	}
	if err := ValidateID(token); err != nil {
		return "", err
	}
	d.mu.Lock()
	entry, ok := d.files[token]
	closed := d.closed
	d.mu.Unlock()
	if closed || !ok || entry.sessionID != id {
		return "", errors.New("请先准备此文件的拖出下载")
	}
	if err := d.assertIdentity(id, entry.identity); err != nil {
		return "", err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	entry, ok = d.files[token]
	if d.closed || !ok || entry.used {
		return "", errors.New("请重新准备拖出下载")
	}
	if entry.public.ExpiresAt <= d.now().UnixMilli() {
		return "", errors.New("拖出文件已过期，请重新准备下载")
	}
	if !d.localMatches(entry) {
		return "", errors.New("拖出缓存已失效，请重新准备下载")
	}
	if entry.folder != nil {
		// Explorer can still be copying after the native drag loop returns.
		// Keep the snapshot until shutdown, but require new approval for reuse.
		entry.used = true
		d.files[token] = entry
	}
	return entry.local, nil
}

func (d *DragDownloads) removeItem(directory, run string) error {
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return err
	}
	if run == "" || filepath.Dir(absolute) != run || !strings.HasPrefix(absolute, run+string(filepath.Separator)) {
		return errors.New("无效的拖出缓存清理路径")
	}
	if err = verifyCacheChild(absolute, run); err != nil {
		return err
	}
	return os.RemoveAll(absolute)
}

func verifyCacheChild(directory, parent string) error {
	if _, err := os.Lstat(directory); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return err
	}
	realParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return err
	}
	realDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return err
	}
	if filepath.Dir(realDirectory) != realParent {
		return errors.New("缓存清理目标离开了预期目录")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("不能递归清理符号链接缓存目录")
	}
	return nil
}

// Stop/cancel transfers before calling Close so in-progress preparation can settle.
func (d *DragDownloads) Close() error {
	d.mu.Lock()
	d.closed = true
	d.mu.Unlock()
	d.wg.Wait()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.files = make(map[string]cachedDrag)
	if d.runLease != nil {
		_ = d.runLease.Close()
		d.runLease = nil
	}
	if d.run == "" {
		return nil
	}
	absolute, err := filepath.Abs(d.run)
	if err != nil {
		return err
	}
	if !filepath.IsAbs(d.root) || filepath.Dir(absolute) != d.root || !strings.HasPrefix(absolute, d.root+string(filepath.Separator)) {
		return errors.New("无效的拖出缓存清理路径")
	}
	if err = verifyCacheChild(absolute, d.root); err != nil {
		return err
	}
	if err = os.RemoveAll(absolute); err != nil {
		return err
	}
	d.run = ""
	return nil
}
