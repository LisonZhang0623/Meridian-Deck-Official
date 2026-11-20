package core

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/pkg/sftp"
)

const transferBufferSize = 64 * 1024

type transferPartial struct {
	offset             int64
	local, remote      fileMark
	digest, checkpoint []byte
	localSource        os.FileInfo
}

var errInvalidPartial = errors.New("断点文件或源文件已变化，请重新发起传输")

type resumableConnection interface {
	TransferResumeIdentity(string) (string, error)
}

func resumeConnection(conns Connections, id string) string {
	if resumable, ok := conns.(resumableConnection); ok {
		identity, _ := resumable.TransferResumeIdentity(id)
		return identity
	}
	return ""
}

func (job *transferJob) temporaryRemote() string {
	return path.Join(path.Dir(job.transfer.RemotePath), ".sshtool-"+job.transfer.ID+".part")
}

func (job *transferJob) openLocalRoot() (*os.Root, string, error) {
	base, name := filepath.Dir(job.localPath), filepath.Base(job.localPath)
	var expectedRoot os.FileInfo
	if job.guard != nil {
		base, name, expectedRoot = job.guard.localRoot, job.guard.localRelative, job.guard.localRootInfo
	} else if job.nasSource != nil {
		base, name, expectedRoot = job.nasSource.root, job.nasSource.relative, job.nasSource.rootInfo
	}
	root, err := os.OpenRoot(base)
	if err != nil {
		return nil, "", err
	}
	if expectedRoot != nil {
		info, e := root.Stat(".")
		if e != nil || !os.SameFile(info, expectedRoot) {
			root.Close()
			return nil, "", errors.New("本地目录已变化，请重新选择目录")
		}
		if e = job.checkLocalParents(root, name); e != nil {
			root.Close()
			return nil, "", e
		}
	}
	return root, name, nil
}

func (job *transferJob) checkLocalParents(root *os.Root, name string) error {
	parent := filepath.ToSlash(filepath.Dir(name))
	if job.nasSource != nil || job.guard != nil && job.guard.scoped {
		_, err := CheckNASDirectory(root, parent)
		return err
	}
	return checkParents(root.Lstat, parent)
}

func (job *transferJob) temporaryLocal(name string) string {
	return filepath.Join(filepath.Dir(name), ".sshtool-"+job.transfer.ID+".part")
}

func (t *Transfers) Pause(id string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	job := t.jobs[id]
	if job == nil || job.transfer.Purpose == "drag" || job.transfer.Status != "running" || job.committing || job.ctx.Err() != nil {
		return errors.New("该任务现在不能暂停")
	}
	job.pauseRequested = true
	job.cancel()
	return nil
}

// DiscardPartial only removes the random staging path allocated for this task.
// Uploads require a live connection to the same authenticated server.
func (t *Transfers) DiscardPartial(id, sessionID string) error {
	t.mu.Lock()
	job := t.jobs[id]
	if t.closed || job == nil || job.partial == nil || job.discarding || job.transfer.Status == "running" || job.transfer.Status == "queued" {
		t.mu.Unlock()
		return errors.New("没有可以丢弃的断点")
	}
	job.discarding = true
	t.mu.Unlock()
	var err error
	if job.transfer.Direction == "upload" {
		if sessionID == "" {
			sessionID = job.transfer.SessionID
		}
		identity, identityErr := t.conns.Identity(sessionID)
		if identityErr != nil || (identity != job.identity || sessionID != job.transfer.SessionID) && (job.resumeIdentity == "" || resumeConnection(t.conns, sessionID) != job.resumeIdentity) {
			err = errors.New("请先连接原服务器，再丢弃上传断点")
		} else {
			err = t.removeRemotePartial(job, sessionID, identity)
		}
	} else {
		err = removeLocalPartial(job)
	}
	t.mu.Lock()
	job.discarding = false
	if err == nil || os.IsNotExist(err) {
		job.partial = nil
		job.transfer.Resumable = false
		job.transfer.Transferred = 0
		if job.transfer.Status == "paused" {
			job.transfer.Status = "cancelled"
		}
		err = nil
	}
	t.mu.Unlock()
	t.publish(job)
	return err
}

func removeLocalPartial(job *transferJob) error {
	root, name, err := job.openLocalRoot()
	if err != nil {
		return err
	}
	defer root.Close()
	return root.Remove(job.temporaryLocal(name))
}

func (t *Transfers) removeRemotePartial(job *transferJob, id, identity string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return t.removeRemotePartialContext(ctx, job, id, identity)
}

func (t *Transfers) removeRemotePartialContext(ctx context.Context, job *transferJob, id, identity string) error {
	client, err := t.conns.Client(id)
	if err != nil {
		return err
	}
	c, err := openSFTP(ctx, client)
	if err != nil {
		return err
	}
	defer c.Close()
	current, err := t.conns.Identity(id)
	if err != nil || current != identity {
		return errors.New("连接已变化，未清理上传断点")
	}
	if err := job.checkRemoteGuard(c); err != nil {
		return err
	}
	return c.Remove(job.temporaryRemote())
}

func (t *Transfers) clearRetainedOnClose() {
	t.mu.Lock()
	var retained []*transferJob
	for _, job := range t.jobs {
		if job.partial != nil && !job.discarding {
			retained = append(retained, job)
		}
	}
	t.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, job := range retained {
		if job.transfer.Direction == "upload" {
			if ctx.Err() == nil {
				_ = t.removeRemotePartialContext(ctx, job, job.transfer.SessionID, job.identity)
			}
		} else {
			_ = removeLocalPartial(job)
		}
	}
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(p)
}

// Recompute the staged prefix before trusting its offset. The source is also
// checked against its original metadata and bytes at the checkpoint boundary.
func verifyPartial(ctx context.Context, partial *transferPartial, source, stage io.ReaderAt, stageSize int64) (hash.Hash, error) {
	if stageSize < partial.offset || len(partial.checkpoint) == 0 {
		return nil, errInvalidPartial
	}
	checkpoint := make([]byte, len(partial.checkpoint))
	if _, err := source.ReadAt(checkpoint, partial.offset-int64(len(checkpoint))); err != nil {
		return nil, err
	}
	if !bytes.Equal(checkpoint, partial.checkpoint) {
		return nil, errInvalidPartial
	}
	h := sha256.New()
	n, err := io.CopyBuffer(h, contextReader{ctx, io.NewSectionReader(stage, 0, partial.offset)}, make([]byte, transferBufferSize))
	if err != nil {
		return nil, err
	}
	if n != partial.offset || !bytes.Equal(h.Sum(nil), partial.digest) {
		return nil, errInvalidPartial
	}
	return h, nil
}

type resumeStage interface {
	io.ReaderAt
	io.Seeker
	Close() error
	Stat() (os.FileInfo, error)
	Truncate(int64) error
}

func restoreStage(stage resumeStage, job *transferJob, source io.ReaderAt, meter *transferMeter) error {
	info, err := stage.Stat()
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errInvalidPartial
	}
	meter.digest, err = verifyPartial(job.ctx, job.partial, source, stage, info.Size())
	if err != nil {
		return err
	}
	if info.Size() != job.partial.offset {
		if err = stage.Truncate(job.partial.offset); err != nil {
			return err
		}
	}
	_, err = stage.Seek(job.partial.offset, io.SeekStart)
	return err
}

func openRemotePartial(c *sftp.Client, name string, job *transferJob, source io.ReaderAt, meter *transferMeter) (*sftp.File, error) {
	mark, _, err := readFileMark(c.Lstat, name)
	if err != nil {
		return nil, err
	}
	if mark.kind != "file" {
		return nil, errInvalidPartial
	}
	f, err := c.OpenFile(name, os.O_RDWR)
	if err == nil {
		err = restoreStage(f, job, source, meter)
		if err != nil {
			_ = f.Close()
		}
	}
	return f, err
}

func openLocalPartial(root *os.Root, name string, job *transferJob, source io.ReaderAt, meter *transferMeter) (*os.File, error) {
	mark, expected, err := readFileMark(root.Lstat, name)
	if err != nil {
		return nil, err
	}
	if mark.kind != "file" {
		return nil, errInvalidPartial
	}
	f, err := root.OpenFile(name, os.O_RDWR, 0)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil {
		if !os.SameFile(expected, info) {
			err = errInvalidPartial
		} else {
			err = restoreStage(f, job, source, meter)
		}
	}
	if err != nil {
		_ = f.Close()
	}
	return f, err
}

func (t *Transfers) retainPartial(job *transferJob, meter *transferMeter, local, remote fileMark, localSource os.FileInfo, err error) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err == nil || t.closed || job.committing || job.transfer.Purpose == "drag" || meter.n == 0 || job.ctx.Err() != nil && !job.pauseRequested || errors.Is(err, errInvalidPartial) {
		job.partial = nil
		return false
	}
	job.partial = &transferPartial{offset: meter.n, local: local, remote: remote, localSource: localSource, digest: meter.digest.Sum(nil), checkpoint: meter.checkpoint}
	return true
}

func (t *Transfers) clearPartial(job *transferJob) {
	t.mu.Lock()
	job.partial = nil
	t.mu.Unlock()
}
