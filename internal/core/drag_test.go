package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

func TestDragRestartReclaimsAbandonedCache(t *testing.T) {
	const childRoot = "SSHTOOL_TEST_ABANDONED_DRAG_ROOT"
	const crashExit = 73
	if root := os.Getenv(childRoot); root != "" {
		run, lease, err := createDragRun(root)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(run, "snapshot.txt"), []byte("retained remote data"), 0o600); err != nil {
			t.Fatal(err)
		}
		runtime.KeepAlive(lease)
		os.Exit(crashExit) // Deliberately bypass normal cleanup and leave the OS to release the lease.
	}

	root := t.TempDir()
	unrelated := filepath.Join(root, "drag-unrelated")
	if err := os.Mkdir(unrelated, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unrelated, dragRunLease), make([]byte, len(dragRunMagic)), 0o600); err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	child := exec.Command(binary, "-test.run=^TestDragRestartReclaimsAbandonedCache$")
	child.Env = append(os.Environ(), childRoot+"="+root)
	if output, err := child.CombinedOutput(); child.ProcessState == nil || child.ProcessState.ExitCode() != crashExit {
		t.Fatalf("abrupt-exit fixture failed: %s (%v)", output, err)
	}
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 2 {
		t.Fatalf("fixture did not leave a previous-run cache: %+v, %v", entries, err)
	}
	NewDragDownloads(root, nil, nil)
	if entries, err := os.ReadDir(root); err != nil || len(entries) != 1 || entries[0].Name() != filepath.Base(unrelated) {
		t.Fatalf("startup should remove the abandoned run and preserve unrelated data: %+v, %v", entries, err)
	}
}

func TestDragPrepareCachesOpaqueTokenAndRejectsSessionChanges(t *testing.T) {
	f := newFileFixture(t)
	fileWrite(t, f.sftp, "/home/test/hello.txt", []byte("hello 世界"))
	transfers := NewTransfers(f, nil, nil)
	defer transfers.Close()
	root := filepath.Join(t.TempDir(), "cache")
	drag := NewDragDownloads(root, f, transfers)
	defer drag.Close()
	prepared, err := drag.Prepare("session", "/home/test/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Name != "hello.txt" || prepared.Token == "" || prepared.ExpiresAt <= time.Now().UnixMilli() {
		t.Fatalf("bad preparation: %+v", prepared)
	}
	local, err := drag.File("session", prepared.Token)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(local) != "hello.txt" {
		t.Fatalf("filename not preserved: %s", local)
	}
	if bytes, err := os.ReadFile(local); err != nil || string(bytes) != "hello 世界" {
		t.Fatalf("cache data invalid: %q, %v", bytes, err)
	}
	second, err := drag.Prepare("session", "/home/test/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if second.Token != prepared.Token {
		t.Fatal("valid cached preparation was not reused")
	}
	if _, err = drag.File("other", prepared.Token); err == nil {
		t.Fatal("token allowed a different session")
	}
	f.changeIdentity()
	if _, err = drag.File("session", prepared.Token); err == nil {
		t.Fatal("token survived reconnect identity change")
	}
	third, err := drag.Prepare("session", "/home/test/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if third.Token == prepared.Token {
		t.Fatal("reconnect reused old token")
	}
}

func TestDragExpirationLocalMutationAndScopedCleanup(t *testing.T) {
	f := newFileFixture(t)
	fileWrite(t, f.sftp, "/home/test/hello.txt", []byte("original"))
	transfers := NewTransfers(f, nil, nil)
	defer transfers.Close()
	parent := t.TempDir()
	root := filepath.Join(parent, "cache")
	drag := NewDragDownloads(root, f, transfers)
	prepared, err := drag.Prepare("session", "/home/test/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	local, err := drag.File("session", prepared.Token)
	if err != nil {
		t.Fatal(err)
	}
	drag.mu.Lock()
	now := time.Now().Add(11 * time.Minute)
	drag.now = func() time.Time { return now }
	drag.mu.Unlock()
	if _, err = drag.File("session", prepared.Token); err == nil {
		t.Fatal("expired token allowed")
	}
	drag.mu.Lock()
	drag.now = time.Now
	drag.mu.Unlock()
	if err = os.WriteFile(local, []byte("changed file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = drag.File("session", prepared.Token); err == nil {
		t.Fatal("modified cache accepted")
	}
	out := filepath.Join(parent, "must-survive.txt")
	if err = os.WriteFile(out, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = drag.removeItem(parent, drag.run); err == nil {
		t.Fatal("cleanup accepted an external directory")
	}
	peer := NewDragDownloads(root, f, transfers)
	peerPrepared, err := peer.Prepare("session", "/home/test/hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if err = drag.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = peer.File("session", peerPrepared.Token); err != nil {
		t.Fatalf("cleanup removed another live run: %v", err)
	}
	if err = peer.Close(); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || string(got) != "keep" {
		t.Fatal("cleanup removed outside cache")
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("run cache not cleaned: %+v", entries)
	}
	if _, err = drag.Prepare("session", "/home/test/hello.txt"); err == nil {
		t.Fatal("closed drag service allowed preparation")
	}
}

func TestDragShutdownSettlesCancelledPreparationBeforeCleanup(t *testing.T) {
	f := newFileFixture(t)
	fileWrite(t, f.sftp, "/home/test/large.bin", make([]byte, 256*1024))
	entered := make(chan string, 1)
	release := make(chan struct{})
	var once sync.Once
	transfers := NewTransfers(f, func(event Event) {
		item, ok := event["transfer"].(Transfer)
		if ok && item.Purpose == "drag" && item.Status == "running" && item.Transferred > 0 {
			once.Do(func() { entered <- item.ID; <-release })
		}
	}, nil)
	root := filepath.Join(t.TempDir(), "cache")
	drag := NewDragDownloads(root, f, transfers)
	done := make(chan error, 1)
	go func() { _, err := drag.Prepare("session", "/home/test/large.bin"); done <- err }()
	select {
	case id := <-entered:
		transfers.Cancel(id)
	case <-time.After(5 * time.Second):
		close(release)
		transfers.Close()
		drag.Close()
		t.Fatal("preparation did not start")
	}
	close(release)
	transfers.Close()
	if err := drag.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled preparation succeeded")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled preparation remained blocked")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Fatalf("shutdown left cache files: %+v, %v", entries, err)
	}
}

func TestDragConcurrentPreparationAndWindowsNames(t *testing.T) {
	f := newFileFixture(t)
	fileWrite(t, f.sftp, "/home/test/hello.txt", []byte("snapshot"))
	transfers := NewTransfers(f, nil, nil)
	defer transfers.Close()
	drag := NewDragDownloads(filepath.Join(t.TempDir(), "cache"), f, transfers)
	defer drag.Close()
	var wg sync.WaitGroup
	results := make(chan DragDownload, 8)
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			prepared, err := drag.Prepare("session", "/home/test/hello.txt")
			if err != nil {
				errs <- err
			} else {
				results <- prepared
			}
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	token := ""
	for result := range results {
		if token == "" {
			token = result.Token
		}
		if result.Token != token {
			t.Fatal("concurrent prepare produced multiple snapshots")
		}
	}
	if _, err := drag.Prepare("session", "/home/test/images"); err == nil {
		t.Fatal("directory prepared for drag")
	}
	for _, remote := range []string{"/home/test/CON.txt", "/home/test/nul", "/home/test/com¹.log", "/home/test/bad?.txt", "/home/test/trailing.", "/home/test/trailing "} {
		if _, err := dragFileName(remote); err == nil {
			t.Fatalf("unsafe Windows name accepted: %q", remote)
		}
	}
	if name, err := dragFileName("/home/test/中文 result.txt"); err != nil || name != "中文 result.txt" {
		t.Fatalf("valid Unicode name rejected: %q %v", name, err)
	}
}
