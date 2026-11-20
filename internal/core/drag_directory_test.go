package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDragDirectoryPreservesTreeAndConsumesOnlyItsExport(t *testing.T) {
	fixture := newFileFixture(t)
	remote := "/home/test/folder with spaces"
	for _, name := range []string{remote, remote + "/nested", remote + "/nested/empty"} {
		if err := fixture.sftp.Mkdir(name); err != nil {
			t.Fatal(err)
		}
	}
	fileWrite(t, fixture.sftp, remote+"/nested/原文件.txt", []byte("retained folder contents"))
	transfers := NewTransfers(fixture, nil, nil)
	defer transfers.Close()
	root := filepath.Join(t.TempDir(), "cache")
	drag := NewDragDownloads(root, fixture, transfers)
	defer drag.Close()

	staged, err := drag.PrepareDirectory("session", remote)
	if err != nil {
		t.Fatal(err)
	}
	if staged.Name != "folder with spaces" || staged.Token == "" {
		t.Fatalf("unexpected staged directory: %+v", staged)
	}
	if _, err = drag.File("another", staged.Token); err == nil {
		t.Fatal("folder token was accepted by another session")
	}
	local, err := drag.File("session", staged.Token)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(local, "nested", "原文件.txt")); err != nil || string(got) != "retained folder contents" {
		t.Fatalf("nested file not preserved: %q, %v", got, err)
	}
	if items, err := os.ReadDir(filepath.Join(local, "nested", "empty")); err != nil || len(items) != 0 {
		t.Fatalf("empty subdirectory not preserved: %v, %v", items, err)
	}
	if _, err = drag.File("session", staged.Token); err == nil {
		t.Fatal("folder token survived its approved export")
	}
	transfers.mu.Lock()
	for _, job := range transfers.jobs {
		if job.transfer.Purpose != "drag" || job.transfer.DragRoot != remote || job.transfer.Status != "completed" {
			t.Errorf("unexpected folder staging transfer: %+v", job.transfer)
		}
	}
	transfers.mu.Unlock()
	second, err := drag.PrepareDirectory("session", remote)
	if err != nil || second.Token == staged.Token {
		t.Fatalf("folder re-preparation did not issue a new token: %+v, %v", second, err)
	}
	if _, err = os.Stat(local); err != nil {
		t.Fatalf("the old export may still be copying: %v", err)
	}
}

func TestDragDirectoryRejectsChangedLocalTree(t *testing.T) {
	fixture := newFileFixture(t)
	remote := "/home/test/empty folder"
	if err := fixture.sftp.Mkdir(remote); err != nil {
		t.Fatal(err)
	}
	transfers := NewTransfers(fixture, nil, nil)
	defer transfers.Close()
	drag := NewDragDownloads(filepath.Join(t.TempDir(), "cache"), fixture, transfers)
	defer drag.Close()

	staged, err := drag.PrepareDirectory("session", remote)
	if err != nil {
		t.Fatal(err)
	}
	drag.mu.Lock()
	local := drag.files[staged.Token].local
	drag.mu.Unlock()
	if err = os.WriteFile(filepath.Join(local, "unexpected.txt"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = drag.File("session", staged.Token); err == nil {
		t.Fatal("changed folder cache was accepted")
	}
	if _, err = drag.PrepareDirectory("session", remote); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(local); !os.IsNotExist(err) {
		t.Fatalf("stale unused cache should be removed, stat error: %v", err)
	}
}
