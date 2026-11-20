package core

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/pkg/sftp"
)

func writeLocalFixture(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func waitTransfersFrom(t *testing.T, transfers *Transfers, from int) int {
	t.Helper()
	transfers.mu.Lock()
	ids := append([]string(nil), transfers.order[from:]...)
	transfers.mu.Unlock()
	for _, id := range ids {
		if err := transfers.Wait(id); err != nil {
			t.Fatalf("transfer %s failed: %v", id, err)
		}
	}
	return from + len(ids)
}

func TestWholeDirectoryTransfersPreserveNestingAndEmptyDirectories(t *testing.T) {
	f := newFileFixture(t)
	transfers := NewTransfers(f, nil, nil)
	defer transfers.Close()
	local := filepath.Join(t.TempDir(), "work")
	payload := []byte("nested content")
	writeLocalFixture(t, filepath.Join(local, "nested", "data.txt"), payload)
	if err := os.Mkdir(filepath.Join(local, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	count, err := transfers.UploadDirectory("session", local, "/home/test")
	if err != nil || count != 1 {
		t.Fatalf("upload count=%d: %v", count, err)
	}
	index := waitTransfersFrom(t, transfers, 0)
	if got := fileRead(t, f.sftp, "/home/test/work/nested/data.txt"); !bytes.Equal(got, payload) {
		t.Fatalf("unexpected remote bytes %q", got)
	}
	if info, err := f.sftp.Stat("/home/test/work/empty"); err != nil || !info.IsDir() {
		t.Fatalf("missing remote empty directory: %v", err)
	}
	if _, err = transfers.UploadDirectory("session", local, "/home/test"); err == nil {
		t.Fatal("whole-directory upload accepted an existing target")
	}
	destination := t.TempDir()
	count, err = transfers.DownloadDirectory("session", "/home/test/work", destination)
	if err != nil || count != 1 {
		t.Fatalf("download count=%d: %v", count, err)
	}
	waitTransfersFrom(t, transfers, index)
	if got, err := os.ReadFile(filepath.Join(destination, "work", "nested", "data.txt")); err != nil || !bytes.Equal(got, payload) {
		t.Fatalf("unexpected local bytes %q: %v", got, err)
	}
	if info, err := os.Stat(filepath.Join(destination, "work", "empty")); err != nil || !info.IsDir() {
		t.Fatalf("missing local empty directory: %v", err)
	}
}

func TestDirectorySyncComparesContentsRejectsStalePreviewAndPreservesTargetOnlyFiles(t *testing.T) {
	f := newFileFixture(t)
	transfers := NewTransfers(f, nil, nil)
	defer transfers.Close()
	remote := "/home/test/sync"
	if err := f.sftp.Mkdir(remote); err != nil {
		t.Fatal(err)
	}
	local := t.TempDir()
	writeLocalFixture(t, filepath.Join(local, "both.txt"), []byte("left"))
	writeLocalFixture(t, filepath.Join(local, "same.txt"), []byte("same"))
	writeLocalFixture(t, filepath.Join(local, "new", "nested.txt"), []byte("new"))
	fileWrite(t, f.sftp, remote+"/both.txt", []byte("diff"))
	fileWrite(t, f.sftp, remote+"/same.txt", []byte("same"))
	fileWrite(t, f.sftp, remote+"/remote-only.txt", []byte("retain"))
	plan, err := transfers.CompareDirectory("session", local, remote)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, item := range plan.Changes {
		states[item.Path] = item.Status
	}
	if states["both.txt"] != "different" || states["remote-only.txt"] != "remote-only" || states["new/nested.txt"] != "local-only" || states["same.txt"] != "" {
		t.Fatalf("incorrect preview: %#v", plan.Changes)
	}
	fileWrite(t, f.sftp, remote+"/both.txt", []byte("edit"))
	if _, err := transfers.SyncDirectory(plan, "upload"); err == nil {
		t.Fatal("changed remote file bypassed the preview check")
	}
	plan, err = transfers.CompareDirectory("session", local, remote)
	if err != nil {
		t.Fatal(err)
	}
	count, err := transfers.SyncDirectory(plan, "upload")
	if err != nil || count != 2 {
		t.Fatalf("sync count=%d: %v", count, err)
	}
	index := waitTransfersFrom(t, transfers, 0)
	if got := string(fileRead(t, f.sftp, remote+"/both.txt")); got != "left" {
		t.Fatalf("sync wrote %q", got)
	}
	if got := string(fileRead(t, f.sftp, remote+"/new/nested.txt")); got != "new" {
		t.Fatalf("sync nested bytes: %q", got)
	}
	if got := string(fileRead(t, f.sftp, remote+"/remote-only.txt")); got != "retain" {
		t.Fatalf("target-only file changed: %q", got)
	}
	plan, err = transfers.CompareDirectory("session", local, remote)
	if err != nil {
		t.Fatal(err)
	}
	count, err = transfers.SyncDirectory(plan, "download")
	if err != nil || count != 1 {
		t.Fatalf("reverse sync count=%d: %v", count, err)
	}
	waitTransfersFrom(t, transfers, index)
	if got, err := os.ReadFile(filepath.Join(local, "remote-only.txt")); err != nil || string(got) != "retain" {
		t.Fatalf("reverse sync wrote %q: %v", got, err)
	}
}

func TestRecursiveDeleteRechecksPlanAndNeverFollowsLinks(t *testing.T) {
	f := newFileFixture(t)
	transfers := NewTransfers(f, nil, nil)
	defer transfers.Close()
	const remote = "/home/test/delete"
	if err := f.sftp.MkdirAll(remote + "/nested"); err != nil {
		t.Fatal(err)
	}
	fileWrite(t, f.sftp, "/home/test/outside.txt", []byte("outside"))
	fileWrite(t, f.sftp, remote+"/nested/data.txt", []byte("inside"))
	if err := f.sftp.Symlink("/home/test", remote+"/link"); err != nil {
		t.Fatal(err)
	}
	if _, err := transfers.PrepareDirectoryDelete("session", "/"); err == nil {
		t.Fatal("root deletion was not rejected")
	}
	plan, err := transfers.PrepareDirectoryDelete("session", remote)
	if err != nil || plan.Count != 4 {
		t.Fatalf("delete preview count=%d: %v", plan.Count, err)
	}
	fileWrite(t, f.sftp, remote+"/late.txt", []byte("new after confirmation"))
	if err := transfers.DeleteDirectoryTree(plan); err == nil {
		t.Fatal("a changed plan was deleted")
	}
	if _, err := f.sftp.Stat(remote + "/nested/data.txt"); err != nil {
		t.Fatalf("stale preview modified the tree: %v", err)
	}
	plan, err = transfers.PrepareDirectoryDelete("session", remote)
	if err != nil {
		t.Fatal(err)
	}
	if err := transfers.DeleteDirectoryTree(plan); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sftp.Stat(remote); !os.IsNotExist(err) {
		t.Fatalf("directory was not removed: %v", err)
	}
	if got := string(fileRead(t, f.sftp, "/home/test/outside.txt")); got != "outside" {
		t.Fatalf("outside file changed: %q", got)
	}
}

type resumeFixture struct {
	*fileFixture
	route string
}

type resumeFixtureReader struct{ sftp.FileReader }

func (r resumeFixtureReader) Fileread(request *sftp.Request) (io.ReaderAt, error) {
	file, err := r.FileReader.Fileread(request)
	// The example in-memory server marks the shared inode broken on
	// unexpected handle close. A regular filesystem leaves its bytes usable.
	return struct{ io.ReaderAt }{file}, err
}

type resumeFixtureWriter struct{ sftp.OpenFileWriter }

func (w resumeFixtureWriter) Filewrite(request *sftp.Request) (io.WriterAt, error) {
	file, err := w.OpenFileWriter.Filewrite(request)
	return struct{ io.WriterAt }{file}, err
}

func (w resumeFixtureWriter) OpenFile(request *sftp.Request) (sftp.WriterAtReaderAt, error) {
	file, err := w.OpenFileWriter.OpenFile(request)
	return struct{ sftp.WriterAtReaderAt }{file}, err
}

func resumableMemoryFiles(handlers sftp.Handlers) sftp.Handlers {
	handlers.FileGet = resumeFixtureReader{handlers.FileGet}
	handlers.FilePut = resumeFixtureWriter{handlers.FilePut.(sftp.OpenFileWriter)}
	return handlers
}

func (f resumeFixture) TransferResumeIdentity(id string) (string, error) {
	if _, err := f.Client(id); err != nil {
		return "", err
	}
	if id == "other" && f.route == "different" {
		return "different", nil
	}
	return "verified-fixture", nil
}

func pausedTransferFixture(t *testing.T, f Connections) *Transfers {
	t.Helper()
	var transfers *Transfers
	var once sync.Once
	transfers = NewTransfers(f, func(e Event) {
		if item, ok := e["transfer"].(Transfer); ok && item.Status == "running" && item.Transferred > 0 {
			once.Do(func() {
				if err := transfers.Pause(item.ID); err != nil {
					t.Error(err)
				}
			})
		}
	}, func(string) bool { return true })
	t.Cleanup(transfers.Close)
	return transfers
}

func assertPaused(t *testing.T, transfers *Transfers, id string) *transferJob {
	t.Helper()
	if err := transfers.Wait(id); err == nil {
		t.Fatal("transfer unexpectedly completed instead of pausing")
	}
	transfers.mu.Lock()
	defer transfers.mu.Unlock()
	job := transfers.jobs[id]
	if job.transfer.Status != "paused" || !job.transfer.Resumable || job.partial == nil || job.partial.offset <= 0 || job.partial.offset >= job.transfer.Size {
		t.Fatalf("no valid checkpoint: %+v", job.transfer)
	}
	return job
}

func TestTransferResumeAfterReconnectUsesVerifiedServerAndPreservesDestinationUntilCommit(t *testing.T) {
	f := newFileFixture(t, resumableMemoryFiles)
	transfers := pausedTransferFixture(t, resumeFixture{fileFixture: f})
	local := filepath.Join(t.TempDir(), "payload.bin")
	data := bytes.Repeat([]byte("resume-upload"), 32*1024)
	writeLocalFixture(t, local, data)
	const remote = "/home/test/upload.bin"
	fileWrite(t, f.sftp, remote, []byte("original"))
	id, err := transfers.Enqueue("session", "upload", local, remote, "")
	if err != nil {
		t.Fatal(err)
	}
	job := assertPaused(t, transfers, id)
	if got := string(fileRead(t, f.sftp, remote)); got != "original" {
		t.Fatalf("paused upload replaced target: %q", got)
	}
	if info, err := f.sftp.Stat(job.temporaryRemote()); err != nil || info.Size() < job.partial.offset {
		t.Fatalf("partial remote file missing: %v", err)
	}
	f.changeIdentity()
	if err := transfers.Resume(id, "other"); err != nil {
		t.Fatal(err)
	}
	if err := transfers.Wait(id); err != nil {
		t.Fatal(err)
	}
	if got := fileRead(t, f.sftp, remote); !bytes.Equal(got, data) {
		t.Fatal("resumed upload produced different bytes")
	}
	if _, err := f.sftp.Stat(job.temporaryRemote()); !os.IsNotExist(err) {
		t.Fatalf("successful resume leaked staging data: %v", err)
	}
}

func TestTransferResumeRejectsChangedServerAndTamperedLocalCheckpoint(t *testing.T) {
	f := newFileFixture(t, resumableMemoryFiles)
	transfers := pausedTransferFixture(t, resumeFixture{fileFixture: f, route: "different"})
	local := filepath.Join(t.TempDir(), "download.bin")
	writeLocalFixture(t, local, []byte("original"))
	const remote = "/home/test/download.bin"
	fileWrite(t, f.sftp, remote, bytes.Repeat([]byte("resume-download"), 32*1024))
	id, err := transfers.Enqueue("session", "download", local, remote, "")
	if err != nil {
		t.Fatal(err)
	}
	job := assertPaused(t, transfers, id)
	if err := transfers.Resume(id, "other"); err == nil {
		t.Fatal("resumed against a different verified route")
	}
	stage := filepath.Join(filepath.Dir(local), job.temporaryLocal(filepath.Base(local)))
	w, err := os.OpenFile(stage, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, err = w.WriteAt([]byte("bad"), 0)
	if closeErr := w.Close(); err != nil || closeErr != nil {
		t.Fatalf("write tampered fixture: %v %v", err, closeErr)
	}
	if err = transfers.Resume(id, "session"); err != nil {
		t.Fatal(err)
	}
	if err = transfers.Wait(id); !errors.Is(err, errInvalidPartial) {
		t.Fatalf("tampered checkpoint was accepted: %v", err)
	}
	if got, err := os.ReadFile(local); err != nil || string(got) != "original" {
		t.Fatalf("target changed despite invalid checkpoint: %q %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(local))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".sshtool-") {
			t.Fatalf("invalid checkpoint left on disk: %s", e.Name())
		}
	}
}

func TestTransferResumeDownloadUsesStagedPrefix(t *testing.T) {
	f := newFileFixture(t, resumableMemoryFiles)
	transfers := pausedTransferFixture(t, f)
	local := filepath.Join(t.TempDir(), "download.bin")
	writeLocalFixture(t, local, []byte("previous"))
	const remote = "/home/test/download.bin"
	data := bytes.Repeat([]byte("resume-download"), 32*1024)
	fileWrite(t, f.sftp, remote, data)
	id, err := transfers.Enqueue("session", "download", local, remote, "")
	if err != nil {
		t.Fatal(err)
	}
	job := assertPaused(t, transfers, id)
	stage := filepath.Join(filepath.Dir(local), job.temporaryLocal(filepath.Base(local)))
	if got, err := os.ReadFile(local); err != nil || string(got) != "previous" {
		t.Fatalf("paused download wrote final target: %q %v", got, err)
	}
	if err = transfers.Resume(id, "session"); err != nil {
		t.Fatal(err)
	}
	if err = transfers.Wait(id); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(local); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("resumed download mismatched: %v", err)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage remains after successful resume: %v", err)
	}
}
