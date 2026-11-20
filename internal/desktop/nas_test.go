package desktop

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"sshtool/internal/core"
)

func newNASService(t *testing.T, configDir string, selected *string) (*Service, Hooks) {
	t.Helper()
	ui := UI{SelectDirectory: func(string) (string, error) { return *selected, nil }}
	service, hooks, err := New(configDir, "", filepath.Join(configDir, "cache", "drag-downloads"), ui, func(core.Event) {})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(hooks.Close)
	return service, hooks
}

func nasListing(t *testing.T, response Result) nasDirectory {
	t.Helper()
	if !response.OK {
		t.Fatal(response.Error)
	}
	listing, ok := response.Value.(nasDirectory)
	if !ok {
		t.Fatalf("expected a NAS listing, got %#v", response.Value)
	}
	return listing
}

func TestNASSelectionPersistsAndIsKeptOutOfPortableConfig(t *testing.T) {
	configDir := t.TempDir()
	selected := t.TempDir()
	service, hooks := newNASService(t, configDir, &selected)
	initial := service.Call("nas:list", callArguments(t, "", nil))
	if !initial.OK || initial.Value != nil {
		t.Fatalf("unexpected unconfigured NAS listing: %#v", initial)
	}
	if injected := service.Call("nas:select", callArguments(t, selected)); injected.OK {
		t.Fatal("renderer supplied a root to the native selection operation")
	}
	child := filepath.Join(selected, "工作 目录")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	fileName := "数据 sample.txt"
	if err := os.WriteFile(filepath.Join(child, fileName), []byte("NAS"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(selected)
	if err != nil {
		t.Fatal(err)
	}
	listing := nasListing(t, service.Call("nas:select", nil))
	if listing.Root != canonical || listing.RootID == "" || listing.Parent != nil || listing.Path != "" || len(listing.Entries) != 1 || listing.Entries[0].Type != "directory" {
		t.Fatalf("unexpected selected listing: %#v", listing)
	}
	childListing := nasListing(t, service.Call("nas:list", callArguments(t, "工作 目录", listing.RootID)))
	if childListing.Parent == nil || *childListing.Parent != "" || len(childListing.Entries) != 1 {
		t.Fatalf("unexpected child listing: %#v", childListing)
	}
	entry := childListing.Entries[0]
	if entry.Path != "工作 目录/"+fileName || entry.Name != fileName || entry.Type != "file" || entry.Size != 3 || entry.Modified <= 0 {
		t.Fatalf("unexpected file metadata: %#v", entry)
	}
	selected = ""
	if cancelled := service.Call("nas:select", nil); !cancelled.OK || cancelled.Value != nil {
		t.Fatalf("picker cancellation changed NAS selection: %#v", cancelled)
	}
	if current := nasListing(t, service.Call("nas:list", callArguments(t, "", nil))); current.RootID != listing.RootID {
		t.Fatal("picker cancellation replaced the existing NAS authorization")
	}
	portable := filepath.Join(t.TempDir(), "portable.json")
	if err = service.store.ExportConfig(portable); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(portable)
	if err != nil {
		t.Fatal(err)
	}
	encodedRoot, _ := json.Marshal(canonical)
	if strings.Contains(string(raw), string(encodedRoot)) || strings.Contains(string(raw), `"root"`) {
		t.Fatal("portable configuration included NAS authorization")
	}
	if err = service.store.ExportConfig(filepath.Join(configDir, "nas.json")); err == nil {
		t.Fatal("portable export could overwrite the NAS configuration")
	}
	hooks.Close()
	loaded, _ := newNASService(t, configDir, &selected)
	restored := nasListing(t, loaded.Call("nas:list", callArguments(t, "", nil)))
	if restored.Root != canonical || restored.RootID == listing.RootID || restored.RootID == "" {
		t.Fatalf("unexpected restored NAS listing: %#v", restored)
	}
	if stale := loaded.Call("nas:upload", callArguments(t, "test", "/remote", listing.RootID, []string{entry.Path})); stale.OK || !strings.Contains(stale.Error, "已变化") {
		t.Fatalf("stale drag authorization was not rejected: %#v", stale)
	}
}

func TestNASBrowsingRejectsOutsidePathsAndExpiredRoots(t *testing.T) {
	configDir := t.TempDir()
	selected := t.TempDir()
	service, _ := newNASService(t, configDir, &selected)
	if err := os.Mkdir(filepath.Join(selected, "ordinary"), 0700); err != nil {
		t.Fatal(err)
	}
	listing := nasListing(t, service.Call("nas:select", nil))
	for _, input := range []string{".", "..", "../outside", "ordinary/../ordinary", "/etc", `C:\Windows`, `ordinary\subdir`, t.TempDir()} {
		if response := service.Call("nas:list", callArguments(t, input, listing.RootID)); response.OK {
			t.Errorf("accepted path outside the NAS browsing contract: %q", input)
		}
	}
	if response := service.Call("nas:list", callArguments(t, "ordinary", nil)); response.OK {
		t.Fatal("non-root listing accepted without a NAS root identifier")
	}
	t.Run("directory symlink", func(t *testing.T) {
		if err := os.Symlink(t.TempDir(), filepath.Join(selected, "outside")); err != nil {
			t.Skipf("symlink creation unavailable: %v", err)
		}
		if response := service.Call("nas:list", callArguments(t, "outside", listing.RootID)); response.OK {
			t.Fatal("accepted a symlink directory")
		}
		refreshed := nasListing(t, service.Call("nas:list", callArguments(t, "", listing.RootID)))
		if len(refreshed.Entries) != 1 || refreshed.Entries[0].Name != "ordinary" {
			t.Fatalf("symlink entry was listed: %#v", refreshed.Entries)
		}
	})
	selected = configDir
	if response := service.Call("nas:select", nil); response.OK {
		t.Fatal("allowed a NAS root to contain the application's drag cache")
	}
	selected = t.TempDir()
	replacement := nasListing(t, service.Call("nas:select", nil))
	if replacement.RootID == listing.RootID {
		t.Fatal("changing the NAS root reused the old authorization")
	}
	if response := service.Call("nas:list", callArguments(t, "", listing.RootID)); response.OK {
		t.Fatal("accepted an expired NAS root identifier")
	}
}

func TestNASSelectionRejectsAncestorOfUncreatedCacheThroughAlias(t *testing.T) {
	selected := t.TempDir()
	alias := filepath.Join(t.TempDir(), "config-alias")
	if err := os.Symlink(selected, alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	service, _ := newNASService(t, alias, &selected)
	if response := service.Call("nas:select", nil); response.OK || !strings.Contains(response.Error, "拖出缓存以外") {
		t.Fatalf("expected the cache ancestor to be rejected: %#v", response)
	}
}
