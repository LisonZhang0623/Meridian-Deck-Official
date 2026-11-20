package core

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPortableConfigDoesNotExportCredentialsAndRemapsJumpConflicts(t *testing.T) {
	source, err := NewStore(t.TempDir(), "", testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	jump := storageTestProfile("jump")
	jump.RememberCredentials = true
	if _, err = source.SaveProfile(jump, Credentials{Password: stringRef("fixture-only-credential")}); err != nil {
		t.Fatal(err)
	}
	target := storageTestProfile("target")
	target.JumpProfileID = jump.ID
	if _, err = source.SaveProfile(target, Credentials{}); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "portable.json")
	if err = source.ExportConfig(file); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(file)
	if err != nil || bytes.Contains(raw, []byte("credential")) || bytes.Contains(raw, []byte(`"rememberCredentials": true`)) || bytes.Contains(raw, []byte(`"secrets"`)) {
		t.Fatal("portable config included credentials or could not be read", err)
	}
	destination, err := NewStore(t.TempDir(), "", testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	conflict := jump
	conflict.Host = "example.invalid"
	if _, err = destination.SaveProfile(conflict, Credentials{}); err != nil {
		t.Fatal(err)
	}
	imported, err := destination.ImportConfig(file)
	if err != nil || imported.Added != 2 {
		t.Fatalf("first import: %#v %v", imported, err)
	}
	newTarget, err := destination.Get(target.ID)
	if err != nil || newTarget.JumpProfileID == jump.ID {
		t.Fatalf("import did not remap the jump link: %#v %v", newTarget, err)
	}
	newJump, err := destination.Get(newTarget.JumpProfileID)
	if err != nil || newJump.Host != jump.Host || newJump.HasCredentials || newJump.RememberCredentials {
		t.Fatalf("imported wrong jump or remembered credentials: %#v %v", newJump, err)
	}
	if repeated, err := destination.ImportConfig(file); err != nil || repeated.Added != 0 || len(repeated.Profiles) != 3 {
		t.Fatalf("repeat import: %#v %v", repeated, err)
	}
}
