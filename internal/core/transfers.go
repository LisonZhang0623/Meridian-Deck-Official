package core

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"sync"
	"time"

	"github.com/pkg/sftp"
)

type transferJob struct {
	transfer            Transfer
	localPath, identity string
	resumeIdentity      string
	ctx                 context.Context
	cancel              context.CancelFunc
	committing          bool
	pauseRequested      bool
	discarding          bool
	partial             *transferPartial
	guard               *transferGuard
	nasSource           *nasUploadSource
	outcome             *transferOutcome
}

type transferOutcome struct {
	done chan struct{}
	err  error
}

type Transfers struct {
	conns   Connections
	emit    Emit
	confirm func(string) bool
	mu      sync.Mutex
	jobs    map[string]*transferJob
	order   []string
	active  int
	closed  bool
	wg      sync.WaitGroup
}

func NewTransfers(conns Connections, emit Emit, confirm func(string) bool) *Transfers {
	if emit == nil {
		emit = func(Event) {}
	}
	if confirm == nil {
		confirm = func(string) bool { return false }
	}
	return &Transfers{conns: conns, emit: emit, confirm: confirm, jobs: make(map[string]*transferJob)}
}

func (t *Transfers) Enqueue(id, direction, localPath, remotePath, purpose string) (string, error) {
	jobs, err := t.enqueue(id, direction, purpose, []transferSpec{{localPath: localPath, remotePath: remotePath}})
	if err != nil {
		return "", err
	}
	return jobs[0].transfer.ID, nil
}

type transferSpec struct {
	localPath, remotePath string
	guard                 *transferGuard
	nasSource             *nasUploadSource
	dragRoot              string
}

func (t *Transfers) enqueue(id, direction, purpose string, specs []transferSpec) ([]*transferJob, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	if direction != "upload" && direction != "download" {
		return nil, errors.New("未知的传输方向")
	}
	if purpose != "" && purpose != "drag" && purpose != "directory" && purpose != "sync" && purpose != "nas" {
		return nil, errors.New("未知的传输用途")
	}
	if len(specs) > maxTreeEntries {
		return nil, errors.New("单次传输任务过多")
	}
	identity, err := t.conns.Identity(id)
	if err != nil {
		return nil, err
	}
	if _, err = t.conns.SFTP(id); err != nil {
		return nil, err
	}
	resumeIdentity := resumeConnection(t.conns, id)
	for _, spec := range specs {
		if err = ValidateRemotePath(spec.remotePath); err != nil {
			return nil, err
		}
		if !filepath.IsAbs(spec.localPath) || len(spec.localPath) > 32768 {
			return nil, errors.New("本地文件必须使用有效绝对路径")
		}
		if err = ValidateEntryName(path.Base(spec.remotePath)); err != nil {
			return nil, err
		}
		if spec.guard != nil && spec.guard.identity != identity {
			return nil, errors.New("连接已变化，请重新选择目录")
		}
	}
	jobs := make([]*transferJob, 0, len(specs))
	for _, spec := range specs {
		name := path.Base(spec.remotePath)
		if direction == "upload" {
			name = filepath.Base(spec.localPath)
		}
		ctx, cancel := context.WithCancel(context.Background())
		jobs = append(jobs, &transferJob{transfer: Transfer{ID: NewID(), SessionID: id, Name: name, Direction: direction, RemotePath: spec.remotePath, Purpose: purpose, DragRoot: spec.dragRoot, Status: "queued"}, localPath: filepath.Clean(spec.localPath), identity: identity, resumeIdentity: resumeIdentity, guard: spec.guard, nasSource: spec.nasSource, ctx: ctx, cancel: cancel, outcome: &transferOutcome{done: make(chan struct{})}})
	}
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		for _, job := range jobs {
			job.cancel()
		}
		return nil, errors.New("传输队列已关闭")
	}
	for _, job := range jobs {
		t.jobs[job.transfer.ID] = job
		t.order = append(t.order, job.transfer.ID)
	}
	t.mu.Unlock()
	for _, job := range jobs {
		t.publish(job)
	}
	t.pump()
	return jobs, nil
}

func (t *Transfers) Upload(id, directory string, localPaths []string) error {
	if err := ValidateRemotePath(directory); err != nil {
		return err
	}
	if len(localPaths) > 100 {
		return errors.New("每次最多上传 100 个文件")
	}
	// Validate the entire selection before enqueuing any tasks.
	for _, local := range localPaths {
		if !filepath.IsAbs(local) || len(local) > 32768 {
			return errors.New("无效的本地文件路径")
		}
		if err := ValidateEntryName(filepath.Base(local)); err != nil {
			return err
		}
		info, err := os.Stat(local)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("暂不支持整个文件夹上传")
		}
	}
	specs := make([]transferSpec, 0, len(localPaths))
	for _, local := range localPaths {
		specs = append(specs, transferSpec{localPath: local, remotePath: path.Join(directory, filepath.Base(local))})
	}
	_, err := t.enqueue(id, "upload", "", specs)
	return err
}

func (t *Transfers) publish(job *transferJob) {
	t.mu.Lock()
	snapshot := job.transfer
	t.mu.Unlock()
	t.emit(Event{"type": "transfer", "transfer": snapshot})
}

func (t *Transfers) pump() {
	t.mu.Lock()
	var start []*transferJob
	if !t.closed {
		for _, id := range t.order {
			if t.active >= 2 {
				break
			}
			job := t.jobs[id]
			if job.transfer.Status != "queued" {
				continue
			}
			job.transfer.Status = "running"
			t.active++
			t.wg.Add(1)
			start = append(start, job)
		}
	}
	t.mu.Unlock()
	for _, job := range start {
		t.publish(job)
		go func(j *transferJob) {
			defer t.wg.Done()
			t.run(j)
			t.mu.Lock()
			t.active--
			t.mu.Unlock()
			t.pump()
		}(job)
	}
}

func (t *Transfers) Wait(id string) error {
	t.mu.Lock()
	job := t.jobs[id]
	if job == nil {
		t.mu.Unlock()
		return errors.New("传输任务不存在")
	}
	outcome := job.outcome
	t.mu.Unlock()
	<-outcome.done
	return outcome.err
}

func (t *Transfers) Cancel(id string) {
	t.mu.Lock()
	job := t.jobs[id]
	if job == nil || job.committing || (job.transfer.Status != "queued" && job.transfer.Status != "running") {
		t.mu.Unlock()
		return
	}
	job.pauseRequested = false
	job.cancel()
	queued := job.transfer.Status == "queued"
	if queued {
		job.transfer.Status = "cancelled"
		job.transfer.Speed = 0
		job.outcome.err = context.Canceled
		close(job.outcome.done)
	}
	t.mu.Unlock()
	if queued {
		t.publish(job)
	}
}

func (t *Transfers) Retry(id string) error {
	return t.retry(id, "")
}

// Resume may move a paused task to a new terminal only if both transports
// authenticated the same profile and server host keys.
func (t *Transfers) Resume(id, sessionID string) error {
	return t.retry(id, sessionID)
}

func (t *Transfers) retry(id, sessionID string) error {
	t.mu.Lock()
	job := t.jobs[id]
	if job == nil || job.discarding || (job.transfer.Status != "failed" && job.transfer.Status != "cancelled" && job.transfer.Status != "paused") {
		t.mu.Unlock()
		return errors.New("该任务不能重试")
	}
	if job.transfer.Purpose == "drag" {
		t.mu.Unlock()
		return errors.New("请从文件树重新准备拖出下载")
	}
	if t.closed {
		t.mu.Unlock()
		return errors.New("传输队列已关闭")
	}
	if sessionID == "" {
		sessionID = job.transfer.SessionID
	} else if job.partial == nil {
		t.mu.Unlock()
		return errors.New("该任务没有可续传的断点")
	}
	identity, err := t.conns.Identity(sessionID)
	if err != nil {
		t.mu.Unlock()
		return err
	}
	if (identity != job.identity || sessionID != job.transfer.SessionID) && (job.partial == nil || job.resumeIdentity == "" || resumeConnection(t.conns, sessionID) != job.resumeIdentity) {
		t.mu.Unlock()
		return errors.New("连接已变化，请重新发起传输")
	}
	job.ctx, job.cancel = context.WithCancel(context.Background())
	job.committing = false
	job.pauseRequested = false
	job.identity = identity
	job.transfer.SessionID = sessionID
	job.outcome = &transferOutcome{done: make(chan struct{})}
	job.transfer.Transferred = 0
	if job.partial != nil {
		job.transfer.Transferred = job.partial.offset
	}
	job.transfer.Speed = 0
	job.transfer.Error = ""
	job.transfer.Status = "queued"
	t.mu.Unlock()
	t.publish(job)
	t.pump()
	return nil
}

func (t *Transfers) Close() {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		t.wg.Wait()
		return
	}
	t.closed = true
	var queued []*transferJob
	for _, job := range t.jobs {
		if job.transfer.Status == "queued" {
			job.transfer.Status = "cancelled"
			job.outcome.err = context.Canceled
			job.cancel()
			close(job.outcome.done)
			queued = append(queued, job)
		} else if job.transfer.Status == "running" {
			job.cancel()
		}
	}
	t.mu.Unlock()
	for _, job := range queued {
		t.publish(job)
	}
	t.wg.Wait()
	t.clearRetainedOnClose()
}

func (t *Transfers) assertJob(job *transferJob) error {
	if err := job.ctx.Err(); err != nil {
		return err
	}
	identity, err := t.conns.Identity(job.transfer.SessionID)
	if err != nil {
		return err
	}
	if identity != job.identity {
		return errors.New("连接已变化，传输已停止")
	}
	return nil
}

func (t *Transfers) openChannel(job *transferJob) (*sftp.Client, func(), error) {
	ctx := job.ctx
	client, err := t.conns.Client(job.transfer.SessionID)
	if err != nil {
		return nil, nil, err
	}
	if client == nil {
		return nil, nil, errors.New("SSH 连接不可用")
	}
	// A private subsystem can be closed on cancellation without disrupting a peer terminal or file tree.
	type opened struct {
		client *sftp.Client
		err    error
	}
	ready := make(chan opened, 1)
	go func() { c, e := openSFTP(ctx, client); ready <- opened{c, e} }()
	select {
	case <-ctx.Done():
		go func() {
			result := <-ready
			if result.client != nil {
				result.client.Close()
			}
		}()
		return nil, nil, ctx.Err()
	case result := <-ready:
		if result.err != nil {
			return nil, nil, result.err
		}
		stop := make(chan struct{})
		var once sync.Once
		go func() {
			select {
			case <-ctx.Done():
				result.client.Close()
			case <-stop:
			}
		}()
		return result.client, func() { once.Do(func() { close(stop); result.client.Close() }) }, nil
	}
}

func (t *Transfers) update(job *transferJob, size, transferred int64, speed float64, publish bool) {
	t.mu.Lock()
	job.transfer.Size = size
	job.transfer.Transferred = transferred
	job.transfer.Speed = speed
	t.mu.Unlock()
	if publish {
		t.publish(job)
	}
}

type transferMeter struct {
	t             *Transfers
	job           *transferJob
	size, n, base int64
	start, last   time.Time
	digest        hash.Hash
	checkpoint    []byte
}

func (m *transferMeter) Write(p []byte) (int, error) {
	if err := m.job.ctx.Err(); err != nil {
		return 0, err
	}
	m.n += int64(len(p))
	_, _ = m.digest.Write(p)
	if len(p) > 0 {
		start := max(0, len(p)-transferBufferSize)
		m.checkpoint = append(m.checkpoint[:0], p[start:]...)
	}
	now := time.Now()
	elapsed := now.Sub(m.start).Seconds()
	if elapsed < .1 {
		elapsed = .1
	}
	publish := now.Sub(m.last) >= 120*time.Millisecond
	if publish {
		m.last = now
	}
	m.t.update(m.job, m.size, m.n, float64(m.n-m.base)/elapsed, publish)
	return len(p), nil
}

func (t *Transfers) run(job *transferJob) {
	var err error
	defer func() {
		t.mu.Lock()
		job.transfer.Speed = 0
		if err == nil {
			job.transfer.Status = "completed"
			job.transfer.Transferred = job.transfer.Size
		} else if job.pauseRequested && job.partial != nil {
			job.transfer.Status = "paused"
			job.transfer.Error = ""
		} else if job.ctx.Err() != nil {
			job.transfer.Status = "cancelled"
			job.transfer.Error = ""
		} else {
			job.transfer.Status = "failed"
			job.transfer.Error = err.Error()
		}
		job.outcome.err = err
		job.transfer.Resumable = job.partial != nil
		job.cancel()
		snapshot := job.transfer
		close(job.outcome.done)
		t.mu.Unlock()
		t.emit(Event{"type": "transfer", "transfer": snapshot})
	}()
	if err = t.assertJob(job); err != nil {
		return
	}
	c, release, openErr := t.openChannel(job)
	if openErr != nil {
		err = openErr
		return
	}
	defer release()
	if err = job.checkRemoteGuard(c); err != nil {
		return
	}
	if job.transfer.Direction == "upload" {
		err = t.upload(job, c)
	} else {
		err = t.download(job, c)
	}
}

func transferMarks(c *sftp.Client, remote string, root *os.Root, name string) (fileMark, fileMark, os.FileInfo, os.FileInfo, error) {
	local, localInfo, err := readFileMark(root.Lstat, name)
	if err != nil {
		return fileMark{}, fileMark{}, nil, nil, err
	}
	remoteMark, remoteInfo, err := readFileMark(c.Lstat, remote)
	return local, remoteMark, localInfo, remoteInfo, err
}

func (job *transferJob) validateMarks(local, remote fileMark, localInfo os.FileInfo) error {
	if job.partial != nil {
		p := job.partial
		if p.local != local || p.remote != remote || p.localSource != nil && (localInfo == nil || !os.SameFile(p.localSource, localInfo)) {
			return errInvalidPartial
		}
	}
	if job.nasSource != nil && (local != markFile(job.nasSource.source) || localInfo == nil || !os.SameFile(job.nasSource.source, localInfo)) {
		return errors.New("NAS 源文件已变化，请重新选择上传文件")
	}
	if job.guard != nil && (job.guard.local != local || job.guard.remote != remote) {
		if job.guard.scoped {
			return errors.New("源文件或目标已变化，请重新拖拽")
		}
		return errors.New("文件在目录比较后发生变化，请重新比较")
	}
	return nil
}

func (t *Transfers) beginCommit(job *transferJob) error {
	if err := t.assertJob(job); err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if job.ctx.Err() != nil {
		return job.ctx.Err()
	}
	job.committing = true
	return nil
}

func (t *Transfers) meter(job *transferJob, size int64) *transferMeter {
	m := &transferMeter{t: t, job: job, size: size, start: time.Now(), digest: sha256.New()}
	if p := job.partial; p != nil {
		m.n, m.base, m.checkpoint = p.offset, p.offset, append([]byte(nil), p.checkpoint...)
	}
	return m
}

func (t *Transfers) upload(job *transferJob, c *sftp.Client) (result error) {
	root, name, err := job.openLocalRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	remote := job.transfer.RemotePath
	temporary := job.temporaryRemote()
	local, baseline, localInfo, remoteInfo, err := transferMarks(c, remote, root, name)
	if err != nil {
		return err
	}
	if err = job.validateMarks(local, baseline, localInfo); err != nil {
		if errors.Is(err, errInvalidPartial) {
			_ = t.removeRemotePartial(job, job.transfer.SessionID, job.identity)
			t.clearPartial(job)
		}
		return err
	}
	if local.kind != "file" || (baseline.kind != "" && baseline.kind != "file") {
		return errors.New("上传的源和目标必须是普通文件")
	}
	var source *os.File
	if job.nasSource == nil {
		source, err = root.Open(name)
	} else {
		source, err = openNASFile(root, name)
	}
	if err != nil {
		return err
	}
	defer source.Close()
	if opened, e := source.Stat(); e != nil || !os.SameFile(localInfo, opened) || markFile(opened) != local {
		return errors.New("本地源文件已变化，请重试")
	}
	if baseline.kind != "" && job.partial == nil && (job.guard == nil || !job.guard.replace) {
		answer := make(chan bool, 1)
		go func() { answer <- t.confirm(remote) }()
		select {
		case <-job.ctx.Done():
			return job.ctx.Err()
		case yes := <-answer:
			if !yes {
				job.cancel()
				return errors.New("已取消覆盖")
			}
		}
	}
	if err = t.assertJob(job); err != nil {
		return err
	}
	m := t.meter(job, local.size)
	var w *sftp.File
	if job.partial == nil {
		w, err = c.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
	} else {
		w, err = openRemotePartial(c, temporary, job, source, m)
	}
	if err != nil {
		if errors.Is(err, errInvalidPartial) {
			_ = t.removeRemotePartial(job, job.transfer.SessionID, job.identity)
			t.clearPartial(job)
		}
		return err
	}
	defer func() {
		_ = w.Close()
		if !t.retainPartial(job, m, local, baseline, localInfo, result) && result != nil {
			_ = t.removeRemotePartial(job, job.transfer.SessionID, job.identity)
		}
	}()
	mode := os.FileMode(0o600)
	if remoteInfo != nil {
		mode = remoteInfo.Mode().Perm()
	}
	if err = w.Chmod(mode); err != nil {
		return err
	}
	t.update(job, local.size, m.n, 0, true)
	if _, err = source.Seek(m.n, io.SeekStart); err != nil {
		return err
	}
	if err = copyTransfer(job, w, source, m); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	if err = job.checkRemoteGuard(c); err != nil {
		return err
	}
	currentLocal, currentRemote, currentSource, _, err := transferMarks(c, remote, root, name)
	if err != nil {
		return err
	}
	if currentLocal != local || currentRemote != baseline || currentSource == nil || !os.SameFile(localInfo, currentSource) {
		return fmt.Errorf("源或目标文件在上传期间发生变化: %w", errInvalidPartial)
	}
	if err = job.checkLocalParents(root, name); err != nil {
		return err
	}
	if err = t.beginCommit(job); err != nil {
		return err
	}
	if baseline.kind != "" {
		return renameReplacing(c, temporary, remote)
	}
	return c.Rename(temporary, remote)
}

func (t *Transfers) download(job *transferJob, c *sftp.Client) (result error) {
	root, name, err := job.openLocalRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	remote := job.transfer.RemotePath
	temporary := job.temporaryLocal(name)
	local, source, localInfo, _, err := transferMarks(c, remote, root, name)
	if err != nil {
		return err
	}
	if err = job.validateMarks(local, source, localInfo); err != nil {
		if errors.Is(err, errInvalidPartial) {
			_ = root.Remove(temporary)
			t.clearPartial(job)
		}
		return err
	}
	if source.kind != "file" || (local.kind != "" && local.kind != "file") {
		return errors.New("下载的源和目标必须是普通文件")
	}
	r, err := c.Open(remote)
	if err != nil {
		return err
	}
	defer r.Close()
	if info, e := r.Stat(); e != nil || markFile(info) != source {
		return errors.New("远程源文件已变化，请重试")
	}
	m := t.meter(job, source.size)
	var w *os.File
	if job.partial == nil {
		w, err = root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	} else {
		w, err = openLocalPartial(root, temporary, job, r, m)
	}
	if err != nil {
		if errors.Is(err, errInvalidPartial) {
			_ = root.Remove(temporary)
			t.clearPartial(job)
		}
		return err
	}
	defer func() {
		_ = w.Close()
		if !t.retainPartial(job, m, local, source, nil, result) && result != nil {
			_ = root.Remove(temporary)
		}
	}()
	t.update(job, source.size, m.n, 0, true)
	if _, err = r.Seek(m.n, io.SeekStart); err != nil {
		return err
	}
	if err = copyTransfer(job, w, r, m); err != nil {
		return err
	}
	if err = w.Sync(); err != nil {
		return err
	}
	if err = w.Close(); err != nil {
		return err
	}
	if err = job.checkRemoteGuard(c); err != nil {
		return err
	}
	currentLocal, currentRemote, currentTarget, _, err := transferMarks(c, remote, root, name)
	if err != nil {
		return err
	}
	if currentLocal != local || currentRemote != source || localInfo != nil && (currentTarget == nil || !os.SameFile(localInfo, currentTarget)) {
		return fmt.Errorf("源或目标文件在下载期间发生变化: %w", errInvalidPartial)
	}
	if err = job.checkLocalParents(root, name); err != nil {
		return err
	}
	if err = t.beginCommit(job); err != nil {
		return err
	}
	return root.Rename(temporary, name)
}

func copyTransfer(job *transferJob, target io.Writer, source io.Reader, meter *transferMeter) error {
	// Read at most one byte beyond the original size. A growing source must
	// not consume unbounded disk space or be committed as the expected file.
	remaining := meter.size - meter.n
	if remaining < 0 || remaining == int64(^uint64(0)>>1) {
		return errInvalidPartial
	}
	_, err := io.CopyBuffer(io.MultiWriter(target, meter), io.LimitReader(contextReader{job.ctx, source}, remaining+1), make([]byte, transferBufferSize))
	if err != nil {
		return err
	}
	if meter.n != meter.size {
		return fmt.Errorf("传输时源文件大小发生变化: %w", errInvalidPartial)
	}
	return nil
}
