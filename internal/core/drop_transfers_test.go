package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDroppedFolderRoundTripAndBatchValidation(t *testing.T) {
	fixture := newFileFixture(t)
	dir := t.TempDir()
	folder := filepath.Join(dir, "collection")
	destination := filepath.Join(dir, "archive", "drop-here")
	for _, name := range []string{filepath.Join(folder, "nested", "empty"), destination} {
		if err := os.MkdirAll(name, 0700); err != nil {
			t.Fatal(err)
		}
	}
	plain := filepath.Join(dir, "plain.txt")
	for name, content := range map[string]string{filepath.Join(folder, "nested", "data.txt"): "directory data", plain: "regular file"} {
		if err := os.WriteFile(name, []byte(content), 0600); err != nil {
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
	if err := transfers.UploadFromRoot("session", "/home/test", root, []string{"collection", "missing"}); err == nil {
		t.Fatal("accepted an invalid batch")
	}
	if len(transfers.order) != 0 {
		t.Fatal("invalid batch queued a job")
	}
	if _, err := fixture.sftp.Lstat("/home/test/collection"); !os.IsNotExist(err) {
		t.Fatalf("invalid batch created a directory: %v", err)
	}
	if err := transfers.UploadFromRoot("session", "/home/test", root, []string{"collection", "plain.txt"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range transfers.order {
		if err := transfers.Wait(id); err != nil {
			t.Fatal(err)
		}
	}
	if got := string(fileRead(t, fixture.sftp, "/home/test/collection/nested/data.txt")); got != "directory data" {
		t.Fatalf("uploaded directory data %q", got)
	}
	if info, err := fixture.sftp.Stat("/home/test/collection/nested/empty"); err != nil || !info.IsDir() {
		t.Fatalf("empty directory not uploaded: %v", err)
	}
	start := len(transfers.order)
	remotes := []string{"/home/test/collection", "/home/test/plain.txt"}
	if err := transfers.DownloadToRoot("session", remotes, root, "archive/drop-here"); err != nil {
		t.Fatal(err)
	}
	for _, id := range transfers.order[start:] {
		if err := transfers.Wait(id); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range map[string]string{"collection/nested/data.txt": "directory data", "plain.txt": "regular file"} {
		got, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(name)))
		if err != nil || string(got) != want {
			t.Fatalf("downloaded %s = %q, %v", name, got, err)
		}
	}
	if info, err := os.Stat(filepath.Join(destination, "collection", "nested", "empty")); err != nil || !info.IsDir() {
		t.Fatalf("empty directory not downloaded: %v", err)
	}
	if err := transfers.DownloadToRoot("session", remotes, root, "archive/drop-here"); err == nil {
		t.Fatal("download allowed overwriting the existing NAS destination")
	}
	if err := transfers.DownloadToRoot("session", remotes, root, "../outside"); err == nil {
		t.Fatal("download escaped the NAS root")
	}
}

func TestNativeDroppedFolderRequiresOriginalPathIdentity(t *testing.T) {
	fixture := newFileFixture(t)
	dir := t.TempDir()
	folder := filepath.Join(dir, "folder")
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(folder)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(info, info) {
		t.Fatal("could not snapshot native directory identity")
	}
	if err = os.Rename(folder, filepath.Join(dir, "original")); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	transfers := NewTransfers(fixture, nil, nil)
	defer transfers.Close()
	if err = transfers.UploadDropped("session", "/home/test", []LocalDropEntry{{Path: folder, Info: info}}); err == nil {
		t.Fatal("a different folder reused a native drop capability")
	}
	if _, err := fixture.sftp.Lstat("/home/test/folder"); !os.IsNotExist(err) {
		t.Fatalf("rejected folder upload wrote a destination: %v", err)
	}
	info, err = os.Lstat(folder)
	if err != nil {
		t.Fatal(err)
	}
	if err = transfers.UploadDropped("session", "/home/test", []LocalDropEntry{{Path: folder, Info: info}}); err != nil {
		t.Fatal(err)
	}
	if remote, err := fixture.sftp.Lstat("/home/test/folder"); err != nil || !remote.IsDir() {
		t.Fatalf("native folder was not uploaded: %v", err)
	}
}
