package discovery

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

func testEnvironment(t *testing.T) (configHome, stateHome string) {
	t.Helper()
	home := t.TempDir()
	configHome = filepath.Join(home, "config")
	stateHome = filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	return configHome, stateHome
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("wirecmd {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestTrustedDiscoveryOrderAndBoundary(t *testing.T) {
	configHome, _ := testEnvironment(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	child := filepath.Join(workspace, "a", "b")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(configHome, "wirecmd", "config.kdl")
	rootConfig := filepath.Join(workspace, projectConfig)
	nearConfig := filepath.Join(workspace, "a", projectConfig)
	writeFile(t, global)
	writeFile(t, rootConfig)
	writeFile(t, nearConfig)
	writeFile(t, filepath.Join(filepath.Dir(workspace), projectConfig)) // outside the trust boundary

	trusted, err := Trust(workspace)
	if err != nil || trusted != workspace {
		t.Fatalf("Trust() = %q, %v", trusted, err)
	}
	paths, err := Paths(child)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{global, rootConfig, nearConfig}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("Paths() = %#v, want %#v", paths, want)
	}
	canonical, ok, root, err := Status(child)
	if err != nil || !ok || root != workspace || canonical != child {
		t.Fatalf("Status() = %q, %v, %q, %v", canonical, ok, root, err)
	}
	if roots, err := List(); err != nil || !reflect.DeepEqual(roots, []string{workspace}) {
		t.Fatalf("List() = %#v, %v", roots, err)
	}
}

func TestUntrustedAndNotFound(t *testing.T) {
	configHome, _ := testEnvironment(t)
	workspace := t.TempDir()
	child := filepath.Join(workspace, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(workspace, projectConfig))
	_, err := Paths(child)
	var untrusted *UntrustedError
	if !errors.As(err, &untrusted) || untrusted.Workspace != workspace {
		t.Fatalf("Paths() error = %#v", err)
	}
	if err := os.Remove(filepath.Join(workspace, projectConfig)); err != nil {
		t.Fatal(err)
	}
	_, err = Paths(child)
	var notFound *NotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("Paths() error = %#v", err)
	}
	writeFile(t, filepath.Join(configHome, "wirecmd", "config.kdl"))
	if paths, err := Paths(child); err != nil || len(paths) != 1 {
		t.Fatalf("global Paths() = %#v, %v", paths, err)
	}
}

func TestUntrustAndDiscoveredSymlinkRejection(t *testing.T) {
	testEnvironment(t)
	workspace := t.TempDir()
	if _, err := Trust(workspace); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "target.kdl")
	writeFile(t, target)
	if err := os.Symlink(target, filepath.Join(workspace, projectConfig)); err != nil {
		t.Fatal(err)
	}
	if _, err := Paths(workspace); err == nil {
		t.Fatal("Paths() accepted a discovered symlink")
	}
	if _, err := Untrust(workspace); err != nil {
		t.Fatal(err)
	}
	if _, trusted, _, err := Status(workspace); err != nil || trusted {
		t.Fatalf("Status() trusted=%v err=%v", trusted, err)
	}
}

func TestConcurrentTrustUpdatesAndPrivateState(t *testing.T) {
	_, stateHome := testEnvironment(t)
	base := t.TempDir()
	const count = 8
	var wg sync.WaitGroup
	errs := make(chan error, count)
	for i := 0; i < count; i++ {
		path := filepath.Join(base, string(rune('a'+i)))
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := Trust(path)
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
	if roots, err := List(); err != nil || len(roots) != count {
		t.Fatalf("List() count=%d err=%v", len(roots), err)
	}
	for _, path := range []string{filepath.Join(stateHome, "wirecmd"), filepath.Join(stateHome, "wirecmd", "trust.json"), filepath.Join(stateHome, "wirecmd", "trust.lock")} {
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		want := os.FileMode(0o600)
		if info.IsDir() {
			want = 0o700
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s permissions = %o, want %o", path, info.Mode().Perm(), want)
		}
	}
}

func TestUnsafeTrustStateRejected(t *testing.T) {
	_, stateHome := testEnvironment(t)
	dir := filepath.Join(stateHome, "wirecmd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "trust.json"), []byte(`{"version":1,"workspaces":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := List(); err == nil {
		t.Fatal("List() accepted unsafe trust state permissions")
	}
}

func TestRelativeXDGValuesUseHomeFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "relative-config")
	t.Setenv("XDG_STATE_HOME", "relative-state")
	global := filepath.Join(home, ".config", "wirecmd", "config.kdl")
	writeFile(t, global)
	workspace := t.TempDir()
	paths, err := Paths(workspace)
	if err != nil || !reflect.DeepEqual(paths, []string{global}) {
		t.Fatalf("fallback Paths() = %#v, %v", paths, err)
	}
	if _, err := Trust(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "state", "wirecmd", "trust.json")); err != nil {
		t.Fatalf("fallback trust state: %v", err)
	}
}

func TestNearestNestedTrustRootWins(t *testing.T) {
	testEnvironment(t)
	outer := t.TempDir()
	nested := filepath.Join(outer, "nested")
	child := filepath.Join(nested, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	outerConfig := filepath.Join(outer, projectConfig)
	nestedConfig := filepath.Join(nested, projectConfig)
	writeFile(t, outerConfig)
	writeFile(t, nestedConfig)
	if _, err := Trust(outer); err != nil {
		t.Fatal(err)
	}
	if _, err := Trust(nested); err != nil {
		t.Fatal(err)
	}
	paths, err := Paths(child)
	if err != nil || !reflect.DeepEqual(paths, []string{nestedConfig}) {
		t.Fatalf("nested Paths() = %#v, %v", paths, err)
	}
}

func TestDeletedWorkspaceCanBeUntrusted(t *testing.T) {
	testEnvironment(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	if err := os.Mkdir(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Trust(workspace); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := Untrust(workspace); err != nil {
		t.Fatal(err)
	}
	if roots, err := List(); err != nil || len(roots) != 0 {
		t.Fatalf("List() after stale untrust = %#v, %v", roots, err)
	}
}
