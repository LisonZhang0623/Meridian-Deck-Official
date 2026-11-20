package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUploadFromRootStreamsFilesAndConfirmsOverwrite(t *testing.T) {
	fixture := newFileFixture(t)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "子目录"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, contents := range map[string]string{"根文件.txt": "root file", "子目录/共享 文件.txt": "NAS contents"} {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	fileWrite(t, fixture.sftp, "/home/test/共享 文件.txt", []byte("existing"))
	confirmed := make(chan string, 2)
	transfers := NewTransfers(fixture, nil, func(name string) bool {
		confirmed <- name
		return true
	})
	defer transfers.Close()
	if err := transfers.UploadFromRoot("session", "/home/test", root, []string{"根文件.txt", "子目录/共享 文件.txt"}); err != nil {
		t.Fatal(err)
	}
	root.Close()
	for _, id := range transfers.order {
		if err := transfers.Wait(id); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(fileRead(t, fixture.sftp, "/home/test/根文件.txt")); got != "root file" {
		t.Fatalf("root file has contents %q", got)
	}
	if got := string(fileRead(t, fixture.sftp, "/home/test/共享 文件.txt")); got != "NAS contents" {
		t.Fatalf("nested file has contents %q", got)
	}
	select {
	case name := <-confirmed:
		if name != "/home/test/共享 文件.txt" || len(confirmed) != 0 {
			t.Fatalf("unexpected overwrite confirmation %q", name)
		}
	default:
		t.Fatal("existing destination was overwritten without confirmation")
	}
}

func TestUploadFromRootRejectsWholeBatchForInvalidSource(t *testing.T) {
	fixture := newFileFixture(t)
	parent := t.TempDir()
	dir := filepath.Join(parent, "nas")
	if err := os.MkdirAll(filepath.Join(dir, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{filepath.Join(parent, "outside.txt"), filepath.Join(dir, "ok.txt"), filepath.Join(dir, "folder", "ok.txt")} {
		if err := os.WriteFile(name, []byte("contents"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	transfers := NewTransfers(fixture, nil, nil)
	defer transfers.Close()
	for _, invalid := range []string{"", ".", "../outside.txt", "folder/../ok.txt", "folder//ok.txt", `folder\ok.txt`, "/outside.txt", "missing.txt", "folder/ok.txt"} {
		if err := transfers.UploadFromRoot("session", "/home/test", root, []string{"ok.txt", invalid}); err == nil {
			t.Fatalf("unexpectedly accepted %q", invalid)
		}
	}
	t.Run("file symlink", func(t *testing.T) {
		if err := os.Symlink(filepath.Join(parent, "outside.txt"), filepath.Join(dir, "link.txt")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if err := transfers.UploadFromRoot("session", "/home/test", root, []string{"ok.txt", "link.txt"}); err == nil {
			t.Fatal("accepted a symlink to a file outside the selected root")
		}
	})
	t.Run("directory symlink", func(t *testing.T) {
		if err := os.Symlink("folder", filepath.Join(dir, "linked-folder")); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		if _, err := CheckNASDirectory(root, "linked-folder"); err == nil {
			t.Fatal("browsed a symlink to a directory inside the selected root")
		}
		if err := transfers.UploadFromRoot("session", "/home/test", root, []string{"linked-folder/ok.txt"}); err == nil {
			t.Fatal("accepted a file reached through a symlink inside the selected root")
		}
	})
	if len(transfers.order) != 0 {
		t.Fatal("invalid selection queued a partial batch")
	}
}

func TestUploadFromRootRejectsSourceReplacedAfterQueueing(t *testing.T) {
	fixture := newFileFixture(t)
	dir := t.TempDir()
	source := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(source, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(source)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	transfers := NewTransfers(fixture, func(event Event) {
		item, ok := event["transfer"].(Transfer)
		if !ok || item.Status != "queued" {
			return
		}
		// Replace the file between admission and the first upload worker.
		if err := os.Rename(source, filepath.Join(dir, "original.txt")); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(source, []byte("replaced"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(source, info.ModTime(), info.ModTime()); err != nil {
			t.Fatal(err)
		}
	}, nil)
	defer transfers.Close()
	if err := transfers.UploadFromRoot("session", "/home/test", root, []string{"file.txt"}); err != nil {
		t.Fatal(err)
	}
	if err := transfers.Wait(transfers.order[0]); err == nil || !strings.Contains(err.Error(), "源文件已变化") {
		t.Fatalf("expected a source identity failure, got %v", err)
	}
	if _, err := fixture.sftp.Stat("/home/test/file.txt"); !os.IsNotExist(err) {
		t.Fatalf("unexpected remote file after rejected upload: %v", err)
	}
}
