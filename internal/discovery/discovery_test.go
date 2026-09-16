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

func canonicalTestPath(t *testing.T, path string) string {
	t.Helper()
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(canonical)
}

func TestResolveCanonicalizesSymlinkedGlobalConfigSource(t *testing.T) {
	_, _ = testEnvironment(t)
	realHome := filepath.Join(t.TempDir(), "real-config")
	aliasHome := filepath.Join(t.TempDir(), "config-alias")
	if err := os.MkdirAll(realHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realHome, aliasHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", aliasHome)
	global := filepath.Join(realHome, "wirecmd", "config.kdl")
	writeFile(t, global)
	cwd := t.TempDir()

	result, err := Resolve(cwd)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Paths, []string{canonicalTestPath(t, global)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("global paths = %#v, want canonical %#v", got, want)
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
	rootConfig := projectConfigPath(workspace)
	nearConfig := projectConfigPath(filepath.Join(workspace, "a"))
	writeFile(t, global)
	writeFile(t, rootConfig)
	writeFile(t, nearConfig)
	writeFile(t, projectConfigPath(filepath.Dir(workspace))) // outside the trust boundary

	canonicalWorkspace := canonicalTestPath(t, workspace)
	canonicalChild := canonicalTestPath(t, child)
	canonicalRootConfig := canonicalTestPath(t, rootConfig)
	canonicalNearConfig := canonicalTestPath(t, nearConfig)
	trusted, err := Trust(workspace)
	if err != nil || trusted != canonicalWorkspace {
		t.Fatalf("Trust() = %q, %v", trusted, err)
	}
	paths, err := Paths(child)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{canonicalTestPath(t, global), canonicalRootConfig, canonicalNearConfig}
	if !reflect.DeepEqual(paths, want) {
		t.Fatalf("Paths() = %#v, want %#v", paths, want)
	}
	canonical, ok, root, err := Status(child)
	if err != nil || !ok || root != canonicalWorkspace || canonical != canonicalChild {
		t.Fatalf("Status() = %q, %v, %q, %v", canonical, ok, root, err)
	}
	if roots, err := List(); err != nil || !reflect.DeepEqual(roots, []string{canonicalWorkspace}) {
		t.Fatalf("List() = %#v, %v", roots, err)
	}
}

func TestResolveReturnsCanonicalContextAndNearestWorkspaceSource(t *testing.T) {
	configHome, _ := testEnvironment(t)
	workspace := filepath.Join(t.TempDir(), "workspace")
	child := filepath.Join(workspace, "nested", "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(configHome, "wirecmd", "config.kdl")
	rootConfig := projectConfigPath(workspace)
	nearestDirectory := filepath.Join(workspace, "nested")
	nearestConfig := projectConfigPath(nearestDirectory)
	writeFile(t, global)
	writeFile(t, rootConfig)
	writeFile(t, nearestConfig)
	if _, err := Trust(workspace); err != nil {
		t.Fatal(err)
	}

	result, err := Resolve(child)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.CWD, canonicalTestPath(t, child); got != want {
		t.Fatalf("CWD = %q, want %q", got, want)
	}
	if got, want := result.TrustedRoot, canonicalTestPath(t, workspace); got != want {
		t.Fatalf("TrustedRoot = %q, want %q", got, want)
	}
	if got, want := result.Paths, []string{canonicalTestPath(t, global), canonicalTestPath(t, rootConfig), canonicalTestPath(t, nearestConfig)}; !reflect.DeepEqual(got, want) {
		t.Fatalf("Paths = %#v, want %#v", got, want)
	}
	if got, want := result.WorkspaceConfig, canonicalTestPath(t, nearestConfig); got != want {
		t.Fatalf("WorkspaceConfig = %q, want %q", got, want)
	}
	if got, want := result.WorkspaceDirectory, canonicalTestPath(t, nearestDirectory); got != want {
		t.Fatalf("WorkspaceDirectory = %q, want %q", got, want)
	}
}

func TestResolveCanonicalizesSymlinkCallerDirectory(t *testing.T) {
	testEnvironment(t)
	realParent := t.TempDir()
	workspace := filepath.Join(realParent, "workspace")
	child := filepath.Join(workspace, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, projectConfigPath(workspace))
	if _, err := Trust(workspace); err != nil {
		t.Fatal(err)
	}
	aliasParent := t.TempDir()
	alias := filepath.Join(aliasParent, "workspace")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	result, err := Resolve(filepath.Join(alias, "child"))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.CWD, canonicalTestPath(t, child); got != want {
		t.Fatalf("CWD = %q, want %q", got, want)
	}
	if got, want := result.WorkspaceDirectory, canonicalTestPath(t, workspace); got != want {
		t.Fatalf("WorkspaceDirectory = %q, want %q", got, want)
	}
}

func TestUntrustedAndNotFound(t *testing.T) {
	configHome, _ := testEnvironment(t)
	workspace := t.TempDir()
	child := filepath.Join(workspace, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	writeFile(t, projectConfigPath(workspace))
	_, err := Paths(child)
	var untrusted *UntrustedError
	if !errors.As(err, &untrusted) || untrusted.Workspace != canonicalTestPath(t, workspace) {
		t.Fatalf("Paths() error = %#v", err)
	}
	if err := os.Remove(projectConfigPath(workspace)); err != nil {
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
	if err := os.MkdirAll(filepath.Dir(projectConfigPath(workspace)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, projectConfigPath(workspace)); err != nil {
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

func TestDiscoveredWorkspaceMetadataSymlinkRejected(t *testing.T) {
	testEnvironment(t)
	workspace := t.TempDir()
	outside := t.TempDir()
	writeFile(t, projectConfigPath(outside))
	if err := os.Symlink(filepath.Join(outside, projectConfigDirectory), filepath.Join(workspace, projectConfigDirectory)); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Trust(workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := Paths(workspace); err == nil {
		t.Fatal("Paths() accepted a symlinked workspace metadata directory")
	}
	if _, err := SecretStoreCandidates(workspace, nil, true); err == nil {
		t.Fatal("SecretStoreCandidates() accepted a symlinked workspace metadata directory")
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
	if err != nil || !reflect.DeepEqual(paths, []string{canonicalTestPath(t, global)}) {
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
	outerConfig := projectConfigPath(outer)
	nestedConfig := projectConfigPath(nested)
	writeFile(t, outerConfig)
	writeFile(t, nestedConfig)
	if _, err := Trust(outer); err != nil {
		t.Fatal(err)
	}
	if _, err := Trust(nested); err != nil {
		t.Fatal(err)
	}
	paths, err := Paths(child)
	if err != nil || !reflect.DeepEqual(paths, []string{canonicalTestPath(t, nestedConfig)}) {
		t.Fatalf("nested Paths() = %#v, %v", paths, err)
	}
}

func TestLegacyTopLevelConfigIsIgnored(t *testing.T) {
	testEnvironment(t)
	workspace := t.TempDir()
	legacy := filepath.Join(workspace, "wirecmd.kdl")
	writeFile(t, legacy)

	paths, err := Paths(workspace)
	var notFound *NotFoundError
	if !errors.As(err, &notFound) || paths != nil {
		t.Fatalf("Paths() = %#v, %v; want ordinary not found", paths, err)
	}

	if _, err := Trust(workspace); err != nil {
		t.Fatal(err)
	}
	paths, err = Paths(workspace)
	if !errors.As(err, &notFound) || paths != nil {
		t.Fatalf("trusted Paths() = %#v, %v; want ordinary not found", paths, err)
	}
}

func TestAutomaticSecretStoreCandidatesIncludeMissingPathsNearestFirst(t *testing.T) {
	configHome, _ := testEnvironment(t)
	workspace := t.TempDir()
	child := filepath.Join(workspace, "a", "b")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Trust(workspace); err != nil {
		t.Fatal(err)
	}
	canonicalChild, err := canonicalDirectory(child)
	if err != nil {
		t.Fatal(err)
	}
	canonicalWorkspace, err := canonicalDirectory(workspace)
	if err != nil {
		t.Fatal(err)
	}

	candidates, err := SecretStoreCandidates(child, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(canonicalChild, projectConfigDirectory, projectSecrets),
		filepath.Join(canonicalWorkspace, "a", projectConfigDirectory, projectSecrets),
		filepath.Join(canonicalWorkspace, projectConfigDirectory, projectSecrets),
		filepath.Join(configHome, "wirecmd", projectSecrets),
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("SecretStoreCandidates() = %#v, want %#v", candidates, want)
	}
	for _, candidate := range candidates {
		if !filepath.IsAbs(candidate) || filepath.Clean(candidate) != candidate {
			t.Fatalf("candidate %q is not absolute and clean", candidate)
		}
		if _, err := os.Lstat(candidate); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("candidate %q should be returned while missing: %v", candidate, err)
		}
	}
}

func TestAutomaticSecretStoreCandidatesUseNearestTrustedRoot(t *testing.T) {
	configHome, _ := testEnvironment(t)
	outer := t.TempDir()
	nested := filepath.Join(outer, "nested")
	child := filepath.Join(nested, "child")
	if err := os.MkdirAll(child, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := Trust(outer); err != nil {
		t.Fatal(err)
	}
	if _, err := Trust(nested); err != nil {
		t.Fatal(err)
	}
	canonicalChild, err := canonicalDirectory(child)
	if err != nil {
		t.Fatal(err)
	}
	canonicalNested, err := canonicalDirectory(nested)
	if err != nil {
		t.Fatal(err)
	}

	candidates, err := SecretStoreCandidates(child, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(canonicalChild, projectConfigDirectory, projectSecrets),
		filepath.Join(canonicalNested, projectConfigDirectory, projectSecrets),
		filepath.Join(configHome, "wirecmd", projectSecrets),
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("SecretStoreCandidates() = %#v, want %#v", candidates, want)
	}
}

func TestAutomaticSecretStoreCandidatesWithoutTrustUseGlobalOnly(t *testing.T) {
	configHome, _ := testEnvironment(t)
	workspace := t.TempDir()

	candidates, err := SecretStoreCandidates(workspace, []string{projectConfigPath(workspace)}, true)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(configHome, "wirecmd", projectSecrets)}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("SecretStoreCandidates() = %#v, want %#v", candidates, want)
	}
}

func TestExplicitSecretStoreCandidatesReverseConfigPrecedenceAndDeduplicate(t *testing.T) {
	configHome, stateHome := testEnvironment(t)
	project := t.TempDir()
	globalConfig := filepath.Join(configHome, "wirecmd", projectConfig)
	projectConfig := filepath.Join(project, ".wirecmd", projectConfig)
	secondProjectConfig := filepath.Join(project, ".wirecmd", "extra.kdl")
	if err := os.MkdirAll(filepath.Join(stateHome, "wirecmd"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stateHome, "wirecmd", "trust.json"), []byte("not JSON"), 0o600); err != nil {
		t.Fatal(err)
	}

	candidates, err := SecretStoreCandidates(project, []string{globalConfig, projectConfig, secondProjectConfig}, false)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		filepath.Join(project, ".wirecmd", projectSecrets),
		filepath.Join(configHome, "wirecmd", projectSecrets),
	}
	if !reflect.DeepEqual(candidates, want) {
		t.Fatalf("SecretStoreCandidates() = %#v, want %#v", candidates, want)
	}
}

func TestDeletedWorkspaceCanBeUntrusted(t *testing.T) {
	testEnvironment(t)
	directory := t.TempDir()
	realParent := filepath.Join(directory, "real")
	if err := os.Mkdir(realParent, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(directory, "alias")
	if err := os.Symlink(realParent, alias); err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(alias, "workspace")
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
