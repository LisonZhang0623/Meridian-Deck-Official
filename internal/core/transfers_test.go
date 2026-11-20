package core

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestTransfersUploadDownloadAndRetryPreserveExistingTargets(t *testing.T) {
	f := newFileFixture(t)
	var eventsMu sync.Mutex
	events := map[string]Transfer{}
	completed := make(chan Transfer, 8)
	transfers := NewTransfers(f, func(event Event) {
		if item, ok := event["transfer"].(Transfer); ok {
			eventsMu.Lock()
			events[item.ID] = item
			eventsMu.Unlock()
			if item.Status == "completed" {
				completed <- item
			}
		}
	}, func(string) bool { return true })
	defer transfers.Close()
	directory := t.TempDir()
	source := filepath.Join(directory, "upload.txt")
	data := []byte("upload 世界\n")
	if err := os.WriteFile(source, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := transfers.Upload("session", "/home/test", []string{source}); err != nil {
		t.Fatal(err)
	}
	transfers.mu.Lock()
	first := transfers.order[0]
	transfers.mu.Unlock()
	if err := transfers.Wait(first); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(fileRead(t, f.sftp, "/home/test/upload.txt"), data) {
		t.Fatal("upload bytes differ")
	}
	target := filepath.Join(directory, "existing.txt")
	if err := os.WriteFile(target, []byte("keep until complete"), 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := transfers.Enqueue("session", "download", target, "/home/test/not-yet.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = transfers.Wait(id); err == nil {
		t.Fatal("missing download should fail")
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "keep until complete" {
		t.Fatalf("failed download damaged target: %q, %v", got, err)
	}
	fileWrite(t, f.sftp, "/home/test/not-yet.txt", data)
	if err = transfers.Retry(id); err != nil {
		t.Fatal(err)
	}
	if err = transfers.Wait(id); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(target); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("retried download mismatch: %q, %v", got, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".sshtool-") {
			t.Fatalf("local temporary leaked: %s", entry.Name())
		}
	}
	var event Transfer
	deadline := time.After(5 * time.Second)
	for event.ID != id {
		select {
		case event = <-completed:
		case <-deadline:
			t.Fatal("completion event was not emitted")
		}
	}
	if event.Status != "completed" || event.Size != int64(len(data)) || event.Transferred != event.Size {
		t.Fatalf("unexpected final event %+v", event)
	}
}

func TestTransfersLimitConcurrencyCancelQueuedAndRetry(t *testing.T) {
	f := newFileFixture(t)
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, remote := range []string{"/home/test/one.txt", "/home/test/two.txt"} {
		fileWrite(t, f.sftp, remote, []byte("old"))
	}
	gate := make(chan struct{})
	entered := make(chan struct{}, 2)
	var mu sync.Mutex
	states := map[string]string{}
	active, maxActive := 0, 0
	transfers := NewTransfers(f, func(e Event) {
		item, ok := e["transfer"].(Transfer)
		if !ok {
			return
		}
		mu.Lock()
		previous := states[item.ID]
		states[item.ID] = item.Status
		if previous != "running" && item.Status == "running" {
			active++
			if active > maxActive {
				maxActive = active
			}
		}
		if previous == "running" && item.Status != "running" {
			active--
		}
		mu.Unlock()
	}, func(string) bool { entered <- struct{}{}; <-gate; return true })
	defer transfers.Close()
	first, err := transfers.Enqueue("session", "upload", source, "/home/test/one.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	second, err := transfers.Enqueue("session", "upload", source, "/home/test/two.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			close(gate)
			t.Fatal("overwrite confirmation was not reached")
		}
	}
	third, err := transfers.Enqueue("session", "upload", source, "/home/test/three.txt", "")
	if err != nil {
		close(gate)
		t.Fatal(err)
	}
	transfers.Cancel(third)
	if err = transfers.Wait(third); !errors.Is(err, context.Canceled) {
		close(gate)
		t.Fatalf("queued cancellation: %v", err)
	}
	if _, err = f.sftp.Stat("/home/test/three.txt"); !os.IsNotExist(err) {
		close(gate)
		t.Fatalf("cancelled queued task created target: %v", err)
	}
	if err = transfers.Retry(third); err != nil {
		close(gate)
		t.Fatal(err)
	}
	close(gate)
	for _, id := range []string{first, second, third} {
		if err = transfers.Wait(id); err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	observed := maxActive
	mu.Unlock()
	if observed != 2 {
		t.Fatalf("expected at most two active jobs, got %d", observed)
	}
	if got := string(fileRead(t, f.sftp, "/home/test/three.txt")); got != "new" {
		t.Fatalf("retry produced %q", got)
	}
}

func TestTransferCancelRunningUploadCleansTemporaryAndKeepsSharedConnection(t *testing.T) {
	f := newFileFixture(t)
	original := []byte("original must survive cancellation")
	fileWrite(t, f.sftp, "/home/test/target.txt", original)
	source := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(source, bytes.Repeat([]byte("cancel"), 1024*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	var transfers *Transfers
	var once sync.Once
	transfers = NewTransfers(f, func(e Event) {
		item, ok := e["transfer"].(Transfer)
		if ok && item.Status == "running" && item.Transferred > 0 {
			once.Do(func() { transfers.Cancel(item.ID) })
		}
	}, func(string) bool { return true })
	defer transfers.Close()
	id, err := transfers.Enqueue("session", "upload", source, "/home/test/target.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = transfers.Wait(id); err == nil {
		t.Fatal("running upload did not cancel")
	}
	if got := fileRead(t, f.sftp, "/home/test/target.txt"); !bytes.Equal(got, original) {
		t.Fatal("cancelled upload replaced original")
	}
	entries, err := f.sftp.ReadDir("/home/test")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".sshtool-") {
			t.Fatalf("cancelled remote temporary leaked: %s", entry.Name())
		}
	}
	if ok, _, err := f.client.SendRequest("fixture-ping", true, nil); err != nil || !ok {
		t.Fatalf("cancellation destroyed shared SSH connection: %v", err)
	}
}

func TestTransfersRejectChangedSessionAndCancelledOverwrite(t *testing.T) {
	f := newFileFixture(t)
	source := filepath.Join(t.TempDir(), "source.txt")
	if err := os.WriteFile(source, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	fileWrite(t, f.sftp, "/home/test/target.txt", []byte("keep"))
	transfers := NewTransfers(f, nil, func(string) bool { return false })
	defer transfers.Close()
	id, err := transfers.Enqueue("session", "upload", source, "/home/test/target.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = transfers.Wait(id); err == nil {
		t.Fatal("cancelled overwrite reported success")
	}
	if got := string(fileRead(t, f.sftp, "/home/test/target.txt")); got != "keep" {
		t.Fatal("cancelled overwrite changed original")
	}
	f.changeIdentity()
	if err = transfers.Retry(id); err == nil {
		t.Fatal("retry crossed a session generation")
	}
	if _, err = transfers.Enqueue("session", "download", "relative.txt", "/home/test/target.txt", ""); err == nil {
		t.Fatal("relative local path accepted")
	}
}

func TestTransferCancellationClosesSessionWhenSubsystemIgnoresEOF(t *testing.T) {
	f := newFileFixture(t)
	f.ignoreSFTPEOF.Store(true)
	fileWrite(t, f.sftp, "/home/test/target.txt", []byte("keep"))
	source := filepath.Join(t.TempDir(), "source.bin")
	if err := os.WriteFile(source, make([]byte, 256*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	var transfers *Transfers
	var once sync.Once
	transfers = NewTransfers(f, func(event Event) {
		item, ok := event["transfer"].(Transfer)
		if ok && item.Status == "running" && item.Transferred > 0 {
			once.Do(func() { transfers.Cancel(item.ID) })
		}
	}, func(string) bool { return true })
	defer transfers.Close()
	id, err := transfers.Enqueue("session", "upload", source, "/home/test/target.txt", "")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- transfers.Wait(id) }()
	select {
	case err = <-done:
		if err == nil {
			t.Fatal("cancelled task succeeded")
		}
	case <-time.After(3 * time.Second):
		f.client.Close()
		t.Fatal("cancellation waited for the SFTP subsystem to honor stdin EOF")
	}
	if got := string(fileRead(t, f.sftp, "/home/test/target.txt")); got != "keep" {
		t.Fatal("cancelled upload replaced target")
	}
	if ok, _, err := f.client.SendRequest("fixture-ping", true, nil); err != nil || !ok {
		t.Fatalf("cancel closed shared SSH transport: %v", err)
	}
}
