package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"sshtool/internal/core"
)

type serviceEvents struct {
	mu    sync.Mutex
	items []core.Event
}

func (e *serviceEvents) emit(event core.Event) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.items = append(e.items, event)
}
func (e *serviceEvents) snapshot() []core.Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]core.Event(nil), e.items...)
}

func testService(t *testing.T) (*Service, Hooks, *serviceEvents) {
	t.Helper()
	directory := t.TempDir()
	events := &serviceEvents{}
	ui := UI{
		SelectFiles:      func() ([]string, error) { return []string{}, nil },
		SelectKey:        func() (string, error) { return "", nil },
		SaveFile:         func(string) (string, error) { return "", nil },
		ConfirmOverwrite: func(string) bool { return false },
		OpenURL:          func(string) error { return nil },
		DragFile:         func(string) error { return nil },
	}
	s, hooks, err := New(directory, "", filepath.Join(directory, "cache", "drag-downloads"), ui, events.emit)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hooks.Close)
	return s, hooks, events
}

func callArguments(t *testing.T, values ...any) []json.RawMessage {
	t.Helper()
	args := make([]json.RawMessage, len(values))
	for i, value := range values {
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		args[i] = raw
	}
	return args
}

func desktopTestProfile() core.Profile {
	return core.Profile{ID: "server", Name: "回环测试", Host: "127.0.0.1", Port: 22, Username: "test", AuthType: "password", Timeout: 5, Tags: []string{"GPU"}}
}

func TestServiceBootstrapUsesEmptyArraysAndDefaults(t *testing.T) {
	s, _, _ := testService(t)
	result := s.Call("bootstrap", nil)
	if !result.OK {
		t.Fatal(result.Error)
	}
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		OK    bool `json:"ok"`
		Value struct {
			Profiles []core.Profile `json:"profiles"`
			Settings core.Settings  `json:"settings"`
		} `json:"value"`
	}
	if err = json.Unmarshal(raw, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Value.Profiles == nil || len(envelope.Value.Profiles) != 0 || envelope.Value.Settings != core.DefaultSettings() {
		t.Fatalf("unexpected bootstrap %s", raw)
	}
	commands := s.Call("commands:list", nil)
	if !commands.OK {
		t.Fatal(commands.Error)
	}
	commandsJSON, _ := json.Marshal(commands.Value)
	if string(commandsJSON) != "[]" {
		t.Fatalf("commands should serialize as empty array: %s", commandsJSON)
	}
}

func TestServiceProfileSaveEmitsProfilesWithoutCredentials(t *testing.T) {
	s, _, events := testService(t)
	password := "never-export-this-test-password"
	result := s.Call("profile:save", callArguments(t, desktopTestProfile(), core.Credentials{Password: &password}))
	if !result.OK {
		t.Fatal(result.Error)
	}
	items := events.snapshot()
	if len(items) != 1 || items[0]["type"] != "profiles" {
		t.Fatalf("profiles event missing: %#v", items)
	}
	raw, err := json.Marshal(items)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), password) || strings.Contains(string(raw), "password\":") {
		t.Fatalf("credential field escaped into public events: %s", raw)
	}
	profiles, ok := items[0]["profiles"].([]core.Profile)
	if !ok || len(profiles) != 1 || profiles[0].ID != "server" {
		t.Fatal("unexpected profiles payload")
	}
	bootstrap := s.Call("bootstrap", nil)
	if !bootstrap.OK {
		t.Fatal(bootstrap.Error)
	}
	deleted := s.Call("profile:delete", callArguments(t, "server"))
	if !deleted.OK {
		t.Fatal(deleted.Error)
	}
	items = events.snapshot()
	if len(items) != 2 {
		t.Fatal("delete did not emit profile change")
	}
}

func TestServiceRejectsUnknownOperationsAndMalformedArguments(t *testing.T) {
	s, _, _ := testService(t)
	for _, test := range []struct {
		name, operation string
		args            []json.RawMessage
	}{
		{"unknown", "shell:run", nil},
		{"extra bootstrap argument", "bootstrap", callArguments(t, "unexpected")},
		{"missing profile argument", "profile:save", nil},
		{"wrong profile type", "profile:save", callArguments(t, "profile", core.Credentials{})},
		{"invalid JSON", "settings:save", []json.RawMessage{json.RawMessage(`{broken`)}},
		{"wrong setting types", "settings:save", []json.RawMessage{json.RawMessage(`{"terminalFontSize":"large"}`)}},
		{"wrong terminal sizes", "terminal:resize", callArguments(t, "terminal", true, 30)},
		{"missing forwarding ID", "forwards:dismiss", nil},
		{"invalid forwarding ID", "forwards:dismiss", callArguments(t, "../forward")},
		{"extra dismiss argument", "forwards:dismiss", callArguments(t, "forward-id", true)},
	} {
		t.Run(test.name, func(t *testing.T) {
			result := s.Call(test.operation, test.args)
			if result.OK || result.Error == "" {
				t.Fatalf("invalid call accepted: %#v", result)
			}
		})
	}
}

func TestServiceCanDismissAnAlreadyRemovedForward(t *testing.T) {
	s, _, _ := testService(t)
	if result := s.Call("forwards:dismiss", callArguments(t, "forward-id")); !result.OK {
		t.Fatal(result.Error)
	}
}

func TestServiceRejectsInvalidConnectionIdentifiersBeforeConnecting(t *testing.T) {
	s, _, events := testService(t)
	saved := s.Call("profile:save", callArguments(t, desktopTestProfile(), core.Credentials{}))
	if !saved.OK {
		t.Fatal(saved.Error)
	}
	for _, id := range []string{"", "../terminal", "terminal with space", strings.Repeat("a", 81)} {
		result := s.Call("ssh:connect", callArguments(t, id, "server", core.Credentials{}, core.Credentials{}))
		if result.OK || !strings.Contains(result.Error, "标识符") {
			t.Fatalf("invalid connection id %q accepted or reached network: %#v", id, result)
		}
		result = s.Call("ssh:newTerminal", callArguments(t, id, "source", "server", nil))
		if result.OK || !strings.Contains(result.Error, "标识符") {
			t.Fatalf("invalid terminal id %q accepted: %#v", id, result)
		}
	}
	for _, event := range events.snapshot() {
		if event["type"] == "state" {
			t.Fatal("invalid identifier created SSH state")
		}
	}
}

func TestServiceRejectsChangedSSHIdentityBeforeConnecting(t *testing.T) {
	s, _, events := testService(t)
	outer := desktopTestProfile()
	outer.ID = "outer"
	middle := desktopTestProfile()
	middle.ID, middle.JumpProfileID = "middle", outer.ID
	target := desktopTestProfile()
	target.JumpProfileID = middle.ID
	for _, profile := range []core.Profile{outer, middle, target} {
		if _, err := s.store.SaveProfile(profile, core.Credentials{}); err != nil {
			t.Fatal(err)
		}
	}
	saved, err := s.store.Get(target.ID)
	if err != nil {
		t.Fatal(err)
	}
	expectedIdentity := core.ProfileIdentity(saved)
	outer.ProxyAddress = "127.0.0.1:1080"
	if _, err := s.store.SaveProfile(outer, core.Credentials{}); err != nil {
		t.Fatal(err)
	}
	password := "prompted-for-original-target"
	credentials := core.Credentials{Password: &password}
	hopCredentials := map[string]core.Credentials{middle.ID: credentials, outer.ID: credentials}
	for _, call := range []struct {
		operation string
		args      []any
	}{
		{"ssh:connect", []any{"terminal", target.ID, credentials, credentials, expectedIdentity}},
		{"ssh:connectChain", []any{"terminal", target.ID, credentials, hopCredentials, expectedIdentity}},
		{"ssh:connectTmux", []any{"terminal", target.ID, credentials, hopCredentials, "work", expectedIdentity}},
	} {
		t.Run(call.operation, func(t *testing.T) {
			result := s.Call(call.operation, callArguments(t, call.args...))
			if result.OK || !strings.Contains(result.Error, "连接配置已变化") {
				t.Fatalf("stale SSH target was not rejected: %#v", result)
			}
		})
	}
	if len(events.snapshot()) != 0 {
		t.Fatal("a rejected connection emitted SSH state")
	}
}

func TestNativeFileGrantsRequireOpaqueTokens(t *testing.T) {
	s, hooks, _ := testService(t)
	file := filepath.Join(t.TempDir(), "selected.txt")
	if err := os.WriteFile(file, []byte("selected local file"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveFiles([]string{file}); err == nil {
		t.Fatal("absolute local path accepted as renderer token")
	}
	if _, err := s.resolveFiles([]string{"not-a-native-grant"}); err == nil {
		t.Fatal("ungranted token accepted")
	}
	tokens := hooks.Drop([]string{file})
	if len(tokens) != 1 || tokens[0] == file {
		t.Fatal("drop did not issue opaque capability")
	}
	if err := core.ValidateID(tokens[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.resolveFiles(tokens); err == nil {
		t.Fatal("drop grant bypassed confirmation through the file picker route")
	}
	if _, err := s.takeDroppedPaths(append(tokens, "ungranted")); err == nil {
		t.Fatal("mixed forged file grant accepted")
	}
	paths, err := s.takeDroppedPaths(tokens)
	resolved, resolveErr := filepath.EvalSymlinks(file)
	if err != nil || resolveErr != nil || len(paths) != 1 || paths[0].Path != resolved {
		t.Fatalf("valid drop grant not resolved: got %#v, want %q, errors: %v, %v", paths, resolved, err, resolveErr)
	}
	if _, err = s.takeDroppedPaths(tokens); err == nil {
		t.Fatal("drop grant could be reused")
	}
	tooMany := make([]string, 101)
	for i := range tooMany {
		tooMany[i] = tokens[0]
	}
	if _, err = s.takeDroppedPaths(tooMany); err == nil {
		t.Fatal("too many file grants accepted")
	}
	// A missing SSH connection is also rejected at the public upload boundary.
	result := s.Call("transfer:upload", callArguments(t, "missing-session", "/home/test", []string{file}))
	if result.OK {
		t.Fatal("upload started without a session or native capability")
	}
}

func TestNativeFileGrantsExcludeDragCacheAndBoundDropSize(t *testing.T) {
	s, hooks, _ := testService(t)
	cacheFile := filepath.Join(s.cacheRoot, "run", "prepared.txt")
	if err := os.MkdirAll(filepath.Dir(cacheFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, []byte("prepared"), 0600); err != nil {
		t.Fatal(err)
	}
	siblingFile := filepath.Join(s.cacheRoot+"-sibling", "external.txt")
	if err := os.MkdirAll(filepath.Dir(siblingFile), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(siblingFile, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	tokens := hooks.Drop([]string{cacheFile, s.cacheRoot, siblingFile})
	if len(tokens) != 1 {
		t.Fatalf("cache exclusion should retain only sibling: %#v", tokens)
	}
	paths, err := s.takeDroppedPaths(tokens)
	resolved, resolveErr := filepath.EvalSymlinks(siblingFile)
	if err != nil || resolveErr != nil || len(paths) != 1 || paths[0].Path != resolved {
		t.Fatalf("wrong filtered drop: got %#v, want %q, errors: %v, %v", paths, resolved, err, resolveErr)
	}
	s.dragging.Store(true)
	if got := hooks.Drop([]string{siblingFile}); len(got) != 0 {
		t.Fatal("internal native drag generated upload grants")
	}
	s.dragging.Store(false)
	files := make([]string, 101)
	directory := t.TempDir()
	for i := range files {
		files[i] = filepath.Join(directory, core.NewID()+".txt")
		if err := os.WriteFile(files[i], []byte("x"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if got := hooks.Drop(files); len(got) != 100 {
		t.Fatalf("drop limit incorrect: %d", len(got))
	}
}

func TestNativeFileGrantExpirationAndShutdown(t *testing.T) {
	s, hooks, _ := testService(t)
	file := filepath.Join(t.TempDir(), "external.txt")
	if err := os.WriteFile(file, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	tokens := hooks.Drop([]string{file})
	if len(tokens) != 1 {
		t.Fatal("grant absent")
	}
	s.mu.Lock()
	grant := s.grants[tokens[0]]
	grant.expires = time.Now().Add(time.Minute)
	s.grants[tokens[0]] = grant
	s.mu.Unlock()
	if _, err := s.takeDroppedPaths(tokens); err != nil {
		t.Fatal("future grant expired early", err)
	}
	tokens = hooks.Drop([]string{file})
	s.mu.Lock()
	grant.expires = time.Now().Add(-time.Nanosecond)
	s.grants[tokens[0]] = grant
	s.mu.Unlock()
	if _, err := s.takeDroppedPaths(tokens); err == nil {
		t.Fatal("expired grant accepted")
	}
	_ = hooks.Drop([]string{file})
	s.mu.Lock()
	_, retained := s.grants[tokens[0]]
	s.mu.Unlock()
	if retained {
		t.Fatal("expired grant was not pruned")
	}
	hooks.Close()
	hooks.Close()
	if got := hooks.Drop([]string{file}); len(got) != 0 {
		t.Fatal("drop accepted after close")
	}
	for _, op := range []string{"bootstrap", "profile:save", "transfer:upload", "drag:start", "not-an-operation"} {
		result := s.Call(op, nil)
		if result.OK || !strings.Contains(result.Error, "退出") {
			t.Fatalf("call %s accepted after close: %#v", op, result)
		}
	}
}

func TestNativeFileGrantsAcceptFoldersAndRejectRelativeAndMissingPaths(t *testing.T) {
	s, hooks, _ := testService(t)
	directory := t.TempDir()
	valid := filepath.Join(directory, "valid.txt")
	if err := os.WriteFile(valid, []byte("valid"), 0600); err != nil {
		t.Fatal(err)
	}
	tokens := hooks.Drop([]string{"", "relative.txt", filepath.Join(directory, "missing.txt"), directory, valid})
	if len(tokens) != 2 {
		t.Fatalf("expected directory and regular file only: %#v", tokens)
	}
	paths, err := s.takeDroppedPaths(tokens)
	resolved, resolveErr := filepath.EvalSymlinks(valid)
	if err != nil || resolveErr != nil || len(paths) != 2 || !paths[0].Info.IsDir() || paths[1].Path != resolved {
		t.Fatalf("valid file grant lost: %#v %v %v", paths, err, resolveErr)
	}
}

func TestNativeFileGrantsResolveSymlinksBeforeCacheFiltering(t *testing.T) {
	s, hooks, _ := testService(t)
	cacheFile := filepath.Join(s.cacheRoot, "prepared.txt")
	if err := os.MkdirAll(s.cacheRoot, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cacheFile, []byte("cached"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "cache-alias.txt")
	if err := os.Symlink(cacheFile, link); err != nil {
		t.Skipf("system does not permit test symlinks: %v", err)
	}
	if tokens := hooks.Drop([]string{link}); len(tokens) != 0 {
		t.Fatal("cache alias granted for upload")
	}
}
