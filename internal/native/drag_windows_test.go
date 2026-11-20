//go:build windows

package native

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReleasedMouseDoesNotEnterShellDrag(t *testing.T) {
	original := leftButtonHeld
	leftButtonHeld = func() bool { return false }
	defer func() { leftButtonHeld = original }()
	// A released gesture must finish before filesystem or COM work. No native
	// interaction is performed by this regression test.
	if err := DragFile(0, "missing-file-that-must-not-be-opened"); err != nil {
		t.Fatalf("late drag request was not cancelled: %v", err)
	}
}

func TestFolderCreatesShellDragObject(t *testing.T) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	folder := filepath.Join(t.TempDir(), "folder with spaces")
	if err := os.Mkdir(folder, 0o700); err != nil {
		t.Fatal(err)
	}
	original := leftButtonHeld
	defer func() { leftButtonHeld = original }()
	checks := 0
	leftButtonHeld = func() bool {
		checks++
		return checks == 1
	}
	if err := DragFile(0, folder); err != nil {
		t.Fatalf("folder did not produce a Shell data object: %v", err)
	}
	if checks != 2 {
		t.Fatal("expected to reach the mouse release check before ShellDoDragDrop")
	}
}
