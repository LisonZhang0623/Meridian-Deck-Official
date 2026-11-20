package core

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// This injected cipher exercises storage boundaries. Actual Windows encryption
// and tamper detection are exercised separately using NewSystemCipher.
type testCipher struct{ fail bool }

func (c testCipher) Encrypt(raw []byte) (string, error) {
	if c.fail {
		return "", errors.New("injected failure")
	}
	return "test:" + base64.StdEncoding.EncodeToString(raw), nil
}
func (c testCipher) Decrypt(value string) ([]byte, error) {
	if c.fail {
		return nil, errors.New("injected failure")
	}
	return base64.StdEncoding.DecodeString(strings.TrimPrefix(value, "test:"))
}
func storageTestProfile(id string) Profile {
	return Profile{ID: id, Name: "测试服务器", Host: "127.0.0.1", Port: 22, Username: "test", AuthType: "password", Timeout: 20, Keepalive: true, Tags: []string{"GPU"}}
}
func stringRef(s string) *string { return &s }
func fingerprintForTest() string {
	return "SHA256:" + base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
}

func TestResolveSSHLoginAcceptsRendererIdentity(t *testing.T) {
	store, err := NewStore(t.TempDir(), "", testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	jump := storageTestProfile("jump")
	jump.Username = "ops<&>"
	target := storageTestProfile("target")
	target.Host, target.Username, target.JumpProfileID = "İ.EXAMPLE", "user<&>", jump.ID
	for _, profile := range []Profile{jump, target} {
		if _, err = store.SaveProfile(profile, Credentials{}); err != nil {
			t.Fatal(err)
		}
	}
	public, err := store.Get(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode([]any{target.ID, "İ.example", target.Port, target.Username, target.AuthType, "", jump.ID, public.JumpIdentity}); err != nil {
		t.Fatal(err)
	}
	expected := strings.TrimSpace(buffer.String())
	login, err := store.ResolveSSHLogin(target.ID, Credentials{Password: stringRef("target-secret")}, Credentials{}, nil, &expected)
	if err != nil || login.Profile.Host != target.Host || len(login.Jumps) != 1 || login.Jumps[0].Profile.ID != jump.ID {
		t.Fatalf("matching renderer identity was rejected: %v", err)
	}
	target.Host = "i.example"
	if _, err = store.SaveProfile(target, Credentials{}); err != nil {
		t.Fatal(err)
	}
	if _, err = store.ResolveSSHLogin(target.ID, Credentials{}, Credentials{}, nil, &expected); err == nil {
		t.Fatal("a distinct Unicode hostname matched the earlier connection identity")
	}
}

func TestStoreCredentialsAndIdentity(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "", testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	p := storageTestProfile("one")
	p.RememberCredentials = true
	p, err = s.SaveProfile(p, Credentials{Password: stringRef("only-in-memory-password")})
	if err != nil {
		t.Fatal(err)
	}
	if !p.HasCredentials {
		t.Fatal("saved credentials not reported")
	}
	public, _ := json.Marshal(s.List())
	if bytes.Contains(public, []byte("only-in-memory")) {
		t.Fatal("secret leaked in public profiles")
	}
	raw, err := os.ReadFile(filepath.Join(dir, "settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("only-in-memory-password")) {
		t.Fatal("secret saved as plaintext")
	}
	reopened, err := NewStore(dir, "", testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	credentials, err := reopened.Credentials(p.ID)
	if err != nil || credentials.Password == nil || *credentials.Password != "only-in-memory-password" {
		t.Fatal("credentials not recovered", err)
	}
	p.Host = "localhost"
	p, err = reopened.SaveProfile(p, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	if p.HasCredentials {
		t.Fatal("identity change retained credentials")
	}
	p.RememberCredentials = true
	p, err = reopened.SaveProfile(p, Credentials{Password: stringRef("new")})
	if err != nil {
		t.Fatal(err)
	}
	p.RememberCredentials = false
	p, err = reopened.SaveProfile(p, Credentials{})
	if err != nil || p.HasCredentials {
		t.Fatal("disabling memory retained credentials", err)
	}
}

func TestStoreFailedEncryptionDoesNotMutate(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir, "", testCipher{fail: true})
	if err != nil {
		t.Fatal(err)
	}
	p := storageTestProfile("one")
	if _, err = s.SaveProfile(p, Credentials{}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	p.Name = "should-not-be-written"
	p.RememberCredentials = true
	if _, err = s.SaveProfile(p, Credentials{Password: stringRef("secret")}); err == nil {
		t.Fatal("expected encryption failure")
	}
	after, _ := os.ReadFile(filepath.Join(dir, "settings.json"))
	saved, _ := s.Get("one")
	if !bytes.Equal(before, after) || saved.Name == p.Name {
		t.Fatal("failed save changed configuration")
	}
}

func TestStoreMigratesOrdinaryDataWithoutOldSecrets(t *testing.T) {
	legacy, newDir := t.TempDir(), t.TempDir()
	old := emptyData()
	old.Version = 1
	p := storageTestProfile("jump")
	p.RememberCredentials = true
	p.LastConnectedAt = 123
	old.Profiles = []Profile{p}
	target := storageTestProfile("target")
	target.JumpProfileID = p.ID
	old.Profiles = append(old.Profiles, target)
	old.Secrets[p.ID] = "electron-safeStorage-opaque-ciphertext"
	old.Hosts["[127.0.0.1]:22"] = fingerprintForTest()
	old.Settings.TerminalFontSize = 18
	raw, _ := json.Marshal(old)
	if err := os.WriteFile(filepath.Join(legacy, "settings.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	commands := []byte(`{"version":1,"commands":[{"id":"cmd","name":"日志","group":"","profileId":"target","command":"tail -n {{lines}} -- {{path}}","description":""}]}`)
	if err := os.WriteFile(filepath.Join(legacy, "commands.json"), commands, 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(newDir, legacy, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.List()) != 2 || s.Settings().TerminalFontSize != 18 || s.Fingerprint("[127.0.0.1]:22") != fingerprintForTest() || s.MigrationNotice() == "" {
		t.Fatal("ordinary data not imported")
	}
	imported, _ := s.Get(p.ID)
	if imported.RememberCredentials || imported.HasCredentials || imported.LastConnectedAt != 123 {
		t.Fatal("credential reset or history not preserved")
	}
	creds, err := s.Credentials(p.ID)
	if err != nil || creds.Password != nil {
		t.Fatal("old credentials imported")
	}
	copyRaw, _ := os.ReadFile(filepath.Join(legacy, "settings.json"))
	if !bytes.Equal(raw, copyRaw) {
		t.Fatal("legacy data modified")
	}
	backup, _ := os.ReadFile(filepath.Join(newDir, "migration-backup", "settings.v1.json"))
	if !bytes.Equal(raw, backup) {
		t.Fatal("legacy backup absent")
	}
	commandStore, err := NewCommandsStore(newDir)
	if err != nil || len(commandStore.List()) != 1 {
		t.Fatal("commands not imported", err)
	}
	imported.Name = "new name"
	if _, err = s.SaveProfile(imported, Credentials{}); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewStore(newDir, legacy, testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := reopened.Get(p.ID)
	if updated.Name != "new name" || reopened.MigrationNotice() != "" {
		t.Fatal("migration not idempotent")
	}
}

func TestStoreCorruptConfigIsPreserved(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "settings.json")
	raw := []byte("broken private data")
	if err := os.WriteFile(file, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(dir, "", testCipher{}); err == nil {
		t.Fatal("corrupt config accepted")
	}
	after, _ := os.ReadFile(file)
	if !bytes.Equal(raw, after) {
		t.Fatal("corrupt file overwritten")
	}
	if _, err := NewStore(dir, dir, testCipher{}); err == nil {
		t.Fatal("shared old/new directory accepted")
	}
}

func TestStoreMigratesCommandOnlyLegacy(t *testing.T) {
	legacy, newDir := t.TempDir(), t.TempDir()
	commands := []byte(`{"version":1,"commands":[{"id":"cmd","name":"日志","group":"","command":"tail -n {{lines}} -- {{path}}","description":""}]}`)
	legacyCommands := filepath.Join(legacy, "commands.json")
	if err := os.WriteFile(legacyCommands, commands, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(newDir, legacy, nil)
	if err != nil {
		t.Fatal(err)
	}
	if store.MigrationNotice() == "" || len(store.List()) != 0 || store.Settings() != DefaultSettings() {
		t.Fatal("command-only migration should keep default settings and show an import notice")
	}
	commandStore, err := NewCommandsStore(newDir)
	if err != nil || len(commandStore.List()) != 1 || commandStore.List()[0].ID != "cmd" {
		t.Fatal("global favorite was not migrated", err)
	}
	for _, file := range []string{legacyCommands, filepath.Join(newDir, "migration-backup", "commands.v1.json")} {
		if raw, err := os.ReadFile(file); err != nil || !bytes.Equal(raw, commands) {
			t.Fatalf("old command data was not preserved at %s: %v", file, err)
		}
	}
	if _, err := os.Stat(filepath.Join(legacy, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("migration wrote settings into the old data directory", err)
	}
}

func TestStoreCorruptLegacyCommandsDoNotPartiallyMigrate(t *testing.T) {
	legacy, newDir := t.TempDir(), t.TempDir()
	data := emptyData()
	data.Version = 1
	raw, _ := json.Marshal(data)
	_ = os.WriteFile(filepath.Join(legacy, "settings.json"), raw, 0600)
	_ = os.WriteFile(filepath.Join(legacy, "commands.json"), []byte("broken"), 0600)
	if _, err := NewStore(newDir, legacy, testCipher{}); err == nil {
		t.Fatal("corrupt commands accepted")
	}
	if _, err := os.Stat(filepath.Join(newDir, "settings.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("partial active settings imported")
	}
}

func TestStoreHistoryAndJumpChanges(t *testing.T) {
	s, err := NewStore(t.TempDir(), "", testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	jump, err := s.SaveProfile(storageTestProfile("jump"), Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	p := storageTestProfile("target")
	p.JumpProfileID = jump.ID
	target, err := s.SaveProfile(p, Credentials{})
	if err != nil {
		t.Fatal(err)
	}
	oldIdentity := ProfileIdentity(target)
	if err = s.RecordConnected(target.ID, oldIdentity); err != nil {
		t.Fatal(err)
	}
	connected, _ := s.Get(target.ID)
	if connected.LastConnectedAt == 0 {
		t.Fatal("history absent")
	}
	connected.Name = "renamed"
	connected.LastConnectedAt = 1
	connected, err = s.SaveProfile(connected, Credentials{})
	if err != nil || connected.LastConnectedAt <= 1 {
		t.Fatal("client overwrote history", err)
	}
	jump.Host = "localhost"
	if _, err = s.SaveProfile(jump, Credentials{}); err != nil {
		t.Fatal(err)
	}
	if err = s.RecordConnected(target.ID, oldIdentity); err != nil {
		t.Fatal(err)
	}
	connected, _ = s.Get(target.ID)
	if connected.LastConnectedAt != 0 {
		t.Fatal("late old connection rewrote history")
	}
	if err = s.Delete(jump.ID); err == nil {
		t.Fatal("referenced jump deleted")
	}
	jump.JumpProfileID = target.ID
	if _, err = s.SaveProfile(jump, Credentials{}); err == nil {
		t.Fatal("cyclic jump accepted")
	}
}

func TestStoreConcurrentUpdatesAndCopies(t *testing.T) {
	s, err := NewStore(t.TempDir(), "", testCipher{})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			p := storageTestProfile(NewID())
			_, err := s.SaveProfile(p, Credentials{})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	profiles := s.List()
	if len(profiles) != 16 {
		t.Fatal("concurrent save lost updates")
	}
	id := profiles[0].ID
	profiles[0].Tags[0] = "changed"
	profiles[0].Name = "changed"
	actual, _ := s.Get(id)
	if actual.Tags[0] == "changed" || actual.Name == "changed" {
		t.Fatal("public profile aliases internal state")
	}
}

func TestValidationBoundaries(t *testing.T) {
	for _, s := range []string{"", "../x", "space id", strings.Repeat("a", 81)} {
		if ValidateID(s) == nil {
			t.Fatalf("accepted id %q", s)
		}
	}
	for _, s := range []string{"", "x\nrm", "\x00"} {
		if ValidateRemotePath(s) == nil {
			t.Fatalf("accepted path %q", s)
		}
	}
	for _, s := range []string{"", ".", "..", "a/b", "a\\b", "a\r"} {
		if ValidateEntryName(s) == nil {
			t.Fatalf("accepted filename %q", s)
		}
	}
	settings := DefaultSettings()
	settings.TerminalFontSize = 9
	if _, err := ValidateSettings(settings); err == nil {
		t.Fatal("invalid font accepted")
	}
	settings = DefaultSettings()
	settings.WindowCloseAction = ""
	if migrated, err := ValidateSettings(settings); err != nil || migrated.WindowCloseAction != CloseAsk {
		t.Fatalf("invalid migration for old window settings: %#v, %v", migrated, err)
	}
	settings.WindowCloseAction = "invalid"
	if _, err := ValidateSettings(settings); err == nil {
		t.Fatal("invalid window close action accepted")
	}
	p := storageTestProfile("id")
	p.Tags = []string{" GPU ", "GPU", "中文"}
	p, err := ValidateProfile(p)
	if err != nil || len(p.Tags) != 2 {
		t.Fatal("tag normalization failed", err)
	}
	p.Host = "server/path"
	if _, err := ValidateProfile(p); err == nil {
		t.Fatal("invalid host accepted")
	}
}

func TestAppearanceSettingsDefaultsAndPersistence(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"version":2,"profiles":[],"settings":{"sidebarSide":"left","sidebarPanel":"files","theme":"dark","terminalFontSize":17,"gpuRefreshSeconds":2}}`
	if err := os.WriteFile(filepath.Join(dir, "settings.json"), []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	settings := store.Settings()
	if settings.StartupAnimation != DefaultStartupAnimation || settings.EditorFontSize != 13 || settings.UIZoom != 100 || settings.SidebarWidth != 0 || settings.TerminalFontSize != 17 || settings.Theme != "dark" {
		t.Fatalf("wrong appearance migration: %+v", settings)
	}
	settings.EditorFontSize, settings.UIZoom, settings.SidebarWidth = 19, 125, 350
	settings.StartupAnimation = "orbit"
	if err := store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewStore(dir, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Settings() != settings {
		t.Fatalf("appearance preferences did not persist: %+v", reloaded.Settings())
	}
	settings.StartupAnimation = "random"
	if err := store.SaveSettings(settings); err != nil {
		t.Fatal(err)
	}
	reloaded, err = NewStore(dir, "", nil)
	if err != nil || reloaded.Settings().StartupAnimation != "random" {
		t.Fatalf("random startup animation did not persist: %v", err)
	}
	settings.StartupAnimation = "invalid"
	if err := store.SaveSettings(settings); err == nil {
		t.Fatal("invalid startup animation accepted")
	}
	settings.StartupAnimation = "off"
	if err := store.SaveSettings(settings); err != nil || store.Settings().StartupAnimation != "off" {
		t.Fatalf("could not disable startup animation: %v", err)
	}
	for _, invalid := range []struct{ editor, zoom, sidebar int }{{9, 100, 0}, {33, 100, 0}, {13, 79, 0}, {13, 151, 0}, {13, 100, 235}, {13, 100, 641}} {
		settings.EditorFontSize, settings.UIZoom, settings.SidebarWidth = invalid.editor, invalid.zoom, invalid.sidebar
		if err := store.SaveSettings(settings); err == nil {
			t.Fatalf("invalid appearance settings accepted: %+v", invalid)
		}
	}
}
