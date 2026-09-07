package toolcache

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func newTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	stateDir := filepath.Join(t.TempDir(), "wirecmd")
	store, err := Open(Options{StateDir: stateDir})
	if err != nil {
		t.Fatal(err)
	}
	return store, stateDir
}

func TestReplaceLoadOpaqueCompleteCatalog(t *testing.T) {
	store, stateDir := newTestStore(t)
	identity := "opaque https://secret.example/mcp?token=not-stored"
	catalog := Catalog{Server: "server", Complete: true, Tools: []Tool{
		{Name: "zebra", InputSchema: json.RawMessage(` { "b": 2, "a": 1 } `)},
		{Name: "apple", Detailed: true},
	}}
	if err := store.Replace(identity, catalog); err != nil {
		t.Fatal(err)
	}
	got, err := store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Complete || len(got.Tools) != 2 || got.Tools[0].Name != "apple" || !got.Tools[0].Detailed || string(got.Tools[1].InputSchema) != `{"a":1,"b":2}` {
		t.Fatalf("catalog = %#v", got)
	}
	dir := filepath.Join(stateDir, metadataDir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var record string
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".json") {
			record = filepath.Join(dir, entry.Name())
		}
	}
	if record == "" || strings.Contains(record, "secret") || strings.Contains(record, "server") {
		t.Fatalf("record path = %q", record)
	}
	raw, err := os.ReadFile(record)
	if err != nil || strings.Contains(string(raw), identity) {
		t.Fatalf("record identity leakage/read error: %v %q", err, raw)
	}
	assertMode(t, stateDir, 0o700)
	assertMode(t, dir, 0o700)
	assertMode(t, record, 0o600)
	assertMode(t, filepath.Join(dir, metadataLock), 0o600)
}

func TestMergeAndMergeExactSemantics(t *testing.T) {
	store, _ := newTestStore(t)
	identity := "identity"
	if err := store.Merge(identity, "server", Tool{Name: "tool", Title: "old", Description: "old description", InputSchema: json.RawMessage(`{"old":true}`)}); err != nil {
		t.Fatal(err)
	}
	if err := store.Merge(identity, "server", Tool{Name: "tool", Title: "new", Detailed: true, OutputSchema: json.RawMessage(`{"new":true}`)}); err != nil {
		t.Fatal(err)
	}
	partial, err := store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	tool := partial.Tools[0]
	if partial.Complete || !tool.Detailed || tool.Title != "new" || tool.Description != "old description" || string(tool.InputSchema) != `{"old":true}` || string(tool.OutputSchema) != `{"new":true}` {
		t.Fatalf("merged tool = %#v", tool)
	}
	if err := store.Merge(identity, "server", Tool{Name: "tool", Description: "later summary"}); err != nil {
		t.Fatal(err)
	}
	if partial, err = store.Load(identity); err != nil || !partial.Tools[0].Detailed {
		t.Fatalf("partial update cleared detailed flag: %#v, %v", partial, err)
	}
	if err := store.Replace(identity, Catalog{Server: "server", Complete: true, Tools: []Tool{{Name: "tool", Title: "listing", Description: "listing description", InputSchema: json.RawMessage(`{"stale":true}`), Detailed: true}, {Name: "gone"}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.MergeExact(identity, "server", Tool{Name: "tool", Title: "focused", Detailed: false}); err != nil {
		t.Fatal(err)
	}
	exact, err := store.Load(identity)
	if err != nil {
		t.Fatal(err)
	}
	if !exact.Complete || len(exact.Tools) != 2 {
		t.Fatalf("exact catalog completeness/tools = %#v", exact)
	}
	tool = exact.Tools[1]
	if tool.Name != "tool" || tool.Title != "focused" || tool.Description != "" || len(tool.InputSchema) != 0 || len(tool.OutputSchema) != 0 || tool.Detailed {
		t.Fatalf("exact update did not clear stale fields: %#v", tool)
	}
}

func TestLoadDistinguishesMissCorruptAndUnsafe(t *testing.T) {
	store, stateDir := newTestStore(t)
	if _, err := store.Load("missing"); !errors.Is(err, ErrMiss) {
		t.Fatalf("missing = %v", err)
	}
	if err := store.Replace("present", Catalog{Server: "server", Complete: true}); err != nil {
		t.Fatal(err)
	}
	path := store.recordPath("present")
	if err := os.WriteFile(path, []byte(`{"version":999,"catalog":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("present"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("unsupported = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(stateDir, "target")
	if err := os.WriteFile(target, []byte("unsafe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load("present"); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("symlink = %v", err)
	}
}

func TestTextValidationAndUnsafeDirectory(t *testing.T) {
	store, stateDir := newTestStore(t)
	description := "first line\n\tsecond line"
	if err := store.Merge("id", "server", Tool{Name: "tool", Description: description}); err != nil {
		t.Fatalf("multiline description: %v", err)
	}
	for _, tool := range []Tool{{Name: "bad\tname"}, {Name: "tool", Title: "bad\ntitle"}, {Name: "tool", Description: "bad\u0085control"}, {Name: "tool", Description: "bad\u202Econtrol"}} {
		if err := store.Merge("id", "server", tool); !errors.Is(err, ErrInvalid) {
			t.Fatalf("hostile tool %#v = %v", tool, err)
		}
	}
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.Merge("id", "server", Tool{Name: "other"}); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("unsafe directory = %v", err)
	}
	linkState := filepath.Join(t.TempDir(), "wirecmd")
	targetState := filepath.Join(t.TempDir(), "target")
	if err := os.Mkdir(targetState, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(targetState, linkState); err != nil {
		t.Fatal(err)
	}
	linked, err := Open(Options{StateDir: linkState})
	if err != nil {
		t.Fatal(err)
	}
	if err := linked.Merge("id", "server", Tool{Name: "tool"}); !errors.Is(err, ErrUnsafeState) {
		t.Fatalf("symlink state directory = %v", err)
	}
}

func TestDefaultStateDirectoryUsesXDGAndFallback(t *testing.T) {
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	store, err := New()
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Merge("id", "server", Tool{Name: "tool"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateHome, "wirecmd", metadataDir)); err != nil {
		t.Fatalf("XDG cache directory: %v", err)
	}
	t.Setenv("XDG_STATE_HOME", "relative-state")
	home := t.TempDir()
	t.Setenv("HOME", home)
	fallback, err := defaultStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(home, ".local", "state", "wirecmd"); fallback != want {
		t.Fatalf("fallback = %q, want %q", fallback, want)
	}
}

func TestConcurrentMergeRetainsTools(t *testing.T) {
	store, _ := newTestStore(t)
	const count = 20
	var group sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		group.Add(1)
		go func(i int) {
			defer group.Done()
			errs <- store.Merge("id", "server", Tool{Name: string(rune('a' + i))})
		}(i)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := store.Load("id")
	if err != nil || len(catalog.Tools) != count || catalog.Complete {
		t.Fatalf("concurrent catalog = %#v, %v", catalog, err)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("mode %q = %o, want %o", path, got, want)
	}
}
