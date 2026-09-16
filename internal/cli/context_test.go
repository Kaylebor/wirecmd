package cli

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Kaylebor/wirecmd/internal/config"
)

func TestResolveInvocationContextChoosesDeepestImplicitCandidate(t *testing.T) {
	base := t.TempDir()
	gitRoot := filepath.Join(base, "git")
	workspace := filepath.Join(gitRoot, "nested")
	cwd := filepath.Join(workspace, "src")
	for _, path := range []string{gitRoot, workspace, cwd} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	original := runGitRoot
	runGitRoot = func(context.Context, string) ([]byte, error) { return []byte(gitRoot + "\n"), nil }
	t.Cleanup(func() { runGitRoot = original })

	resolved, appErr := resolveInvocationContext(context.Background(), configContext{CWD: cwd, WorkspaceDirectory: workspace, Discovered: true}, &loadedConfiguration{Config: &config.Config{GitRoot: config.GitRoot{Enabled: true}}})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if resolved.ProjectRoot != workspace {
		t.Fatalf("project root = %q, want %q", resolved.ProjectRoot, workspace)
	}

	runGitRoot = func(context.Context, string) ([]byte, error) { return []byte(workspace + "\n"), nil }
	resolved, appErr = resolveInvocationContext(context.Background(), configContext{CWD: cwd, WorkspaceDirectory: gitRoot, Discovered: true}, &loadedConfiguration{Config: &config.Config{GitRoot: config.GitRoot{Enabled: true}}})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if resolved.ProjectRoot != workspace {
		t.Fatalf("project root = %q, want deeper Git root %q", resolved.ProjectRoot, workspace)
	}
}

func TestResolveInvocationContextGitDisabledDoesNotProbe(t *testing.T) {
	cwd := t.TempDir()
	called := false
	original := runGitRoot
	runGitRoot = func(context.Context, string) ([]byte, error) {
		called = true
		return nil, nil
	}
	t.Cleanup(func() { runGitRoot = original })

	resolved, appErr := resolveInvocationContext(context.Background(), configContext{CWD: cwd}, &loadedConfiguration{Config: &config.Config{GitRoot: config.GitRoot{Enabled: false}}})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if called {
		t.Fatal("Git was probed with git-root disabled")
	}
	if resolved.ProjectRoot != cwd {
		t.Fatalf("project root = %q, want caller CWD %q", resolved.ProjectRoot, cwd)
	}
}

func TestResolveInvocationContextNearestWorkspaceRootOverridesAllCandidates(t *testing.T) {
	base := t.TempDir()
	project := filepath.Join(base, "project")
	cwd := filepath.Join(project, "src")
	declared := filepath.Join(base, "declared-outside-cwd")
	for _, path := range []string{filepath.Join(project, ".wirecmd"), cwd} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(project, ".wirecmd", "config.kdl")
	contents := "wirecmd {\n  root \"" + filepath.ToSlash(declared) + "\"\n}\n"
	if err := os.WriteFile(configPath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	original := runGitRoot
	runGitRoot = func(context.Context, string) ([]byte, error) {
		t.Fatal("declared root must bypass Git")
		return nil, nil
	}
	t.Cleanup(func() { runGitRoot = original })

	resolved, appErr := resolveInvocationContext(context.Background(), configContext{CWD: cwd, Discovered: true, WorkspaceConfig: configPath, WorkspaceDirectory: project}, &loadedConfiguration{Config: &config.Config{Root: &config.Root{Path: "/weaker/root"}, GitRoot: config.GitRoot{Enabled: true}}, DeclaredRoot: &config.Root{Path: declared}})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if resolved.ProjectRoot != declared {
		t.Fatalf("project root = %q, want declared root %q", resolved.ProjectRoot, declared)
	}
}

func TestLoadContextConfigDoesNotLeakWeakerRootIntoNearestWorkspace(t *testing.T) {
	base := t.TempDir()
	global := filepath.Join(base, "global.kdl")
	outer := filepath.Join(base, "outer", ".wirecmd", "config.kdl")
	nearest := filepath.Join(base, "outer", "project", ".wirecmd", "config.kdl")
	for _, path := range []string{global, outer, nearest} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(global, []byte("wirecmd { root \"/global\"; mcp \"example\" { stdio \"example\" } }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(outer, []byte("wirecmd { root \"/outer\" }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nearest, []byte("wirecmd {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadContextConfig(configContext{Configs: []string{global, outer, nearest}, Discovered: true, WorkspaceConfig: nearest})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Root == nil || loaded.Config.Root.Path != "/outer" {
		t.Fatalf("composed root = %#v, want weaker root retained in static config", loaded.Config.Root)
	}
	if loaded.DeclaredRoot != nil {
		t.Fatalf("nearest workspace inherited weaker root: %#v", loaded.DeclaredRoot)
	}
}

func TestResolveInvocationContextRejectsUnrelatedGitResult(t *testing.T) {
	base := t.TempDir()
	cwd := filepath.Join(base, "workspace", "src")
	boundary := filepath.Join(base, "workspace")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatal(err)
	}
	original := runGitRoot
	runGitRoot = func(context.Context, string) ([]byte, error) { return []byte(filepath.Join(base, "other") + "\n"), nil }
	t.Cleanup(func() { runGitRoot = original })

	resolved, appErr := resolveInvocationContext(context.Background(), configContext{CWD: cwd, Discovered: true, TrustedBoundary: boundary}, &loadedConfiguration{Config: &config.Config{GitRoot: config.GitRoot{Enabled: true}}})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if resolved.ProjectRoot != boundary {
		t.Fatalf("project root = %q, want trusted fallback %q", resolved.ProjectRoot, boundary)
	}
}

func TestResolveInvocationContextExplicitUsesStrongestRoot(t *testing.T) {
	cwd := t.TempDir()
	declared := filepath.Join(t.TempDir(), "not-created")
	original := runGitRoot
	runGitRoot = func(context.Context, string) ([]byte, error) {
		t.Fatal("declared root must bypass Git")
		return nil, nil
	}
	t.Cleanup(func() { runGitRoot = original })

	resolved, appErr := resolveInvocationContext(context.Background(), configContext{CWD: cwd}, &loadedConfiguration{Config: &config.Config{Root: &config.Root{Path: declared}, GitRoot: config.GitRoot{Enabled: true}}, DeclaredRoot: &config.Root{Path: declared}})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if resolved.ProjectRoot != declared {
		t.Fatalf("project root = %q, want %q", resolved.ProjectRoot, declared)
	}
}

func TestResolveInvocationContextGlobalOnlyUsesDeclaredRoot(t *testing.T) {
	cwd := t.TempDir()
	declared := t.TempDir()
	original := runGitRoot
	runGitRoot = func(context.Context, string) ([]byte, error) {
		t.Fatal("declared global root must bypass Git")
		return nil, nil
	}
	t.Cleanup(func() { runGitRoot = original })

	resolved, appErr := resolveInvocationContext(context.Background(), configContext{CWD: cwd, Discovered: true}, &loadedConfiguration{Config: &config.Config{Root: &config.Root{Path: declared}, GitRoot: config.GitRoot{Enabled: true}}, DeclaredRoot: &config.Root{Path: declared}})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if resolved.ProjectRoot != declared {
		t.Fatalf("project root = %q, want global declared root %q", resolved.ProjectRoot, declared)
	}
}

func TestGitRootProbeUsesFixedArgumentsAndInheritedEnvironment(t *testing.T) {
	bin := t.TempDir()
	cwd := t.TempDir()
	git := filepath.Join(bin, "git")
	script := `#!/bin/sh
if [ "$1" != "-C" ] || [ "$2" != "$WIRECMD_EXPECTED_CWD" ] || [ "$3" != "rev-parse" ] || [ "$4" != "--show-toplevel" ] || [ "$#" != 4 ]; then
  exit 64
fi
if [ "$GIT_WORK_TREE" != "$WIRECMD_EXPECTED_GIT_WORK_TREE" ]; then
  exit 65
fi
printf '%s\n' "$WIRECMD_EXPECTED_CWD"
`
	if err := os.WriteFile(git, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("WIRECMD_EXPECTED_CWD", cwd)
	t.Setenv("WIRECMD_EXPECTED_GIT_WORK_TREE", "inherited-value")
	t.Setenv("GIT_WORK_TREE", "inherited-value")

	if got := discoverGitRoot(context.Background(), cwd); got != cwd {
		t.Fatalf("Git root = %q, want %q", got, cwd)
	}
}

func TestGitRootProbeTimesOutQuietly(t *testing.T) {
	bin := t.TempDir()
	git := filepath.Join(bin, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\nexec sleep 5\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	originalTimeout := gitRootTimeout
	gitRootTimeout = 20 * time.Millisecond
	t.Cleanup(func() { gitRootTimeout = originalTimeout })

	started := time.Now()
	if got := discoverGitRoot(context.Background(), t.TempDir()); got != "" {
		t.Fatalf("timed-out Git root = %q, want empty fallback", got)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("Git timeout took %s", elapsed)
	}
}

func TestGlobalWirecmdRootCanonicalizesExistingSymlink(t *testing.T) {
	realHome := filepath.Join(t.TempDir(), "real-config")
	root := filepath.Join(realHome, "wirecmd")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasHome := filepath.Join(t.TempDir(), "config-alias")
	if err := os.Symlink(realHome, aliasHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_CONFIG_HOME", aliasHome)
	if got := globalWirecmdRoot(); got != root {
		t.Fatalf("global root = %q, want canonical %q", got, root)
	}
}

func TestValidateDaemonDiscoveryContext(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	cwd := filepath.Join(workspace, "child")
	configPath := filepath.Join(workspace, ".wirecmd", "config.kdl")
	valid := daemonRequest{CWD: cwd, Configs: []string{"/global/config.kdl", configPath}, Discovered: true, TrustedBoundary: workspace, WorkspaceConfig: configPath, WorkspaceDirectory: workspace}
	if appErr := validateDaemonDiscoveryContext(valid); appErr != nil {
		t.Fatalf("valid context rejected: %v", appErr)
	}
	tests := []struct {
		name    string
		request daemonRequest
	}{
		{"explicit metadata", daemonRequest{Discovered: false, TrustedBoundary: workspace}},
		{"unpaired workspace", daemonRequest{CWD: cwd, Discovered: true, WorkspaceConfig: configPath}},
		{"unrelated boundary", daemonRequest{CWD: cwd, Discovered: true, TrustedBoundary: filepath.Join(base, "other")}},
		{"missing ordered source", daemonRequest{CWD: cwd, Configs: []string{"/global/config.kdl"}, Discovered: true, TrustedBoundary: workspace, WorkspaceConfig: configPath, WorkspaceDirectory: workspace}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if appErr := validateDaemonDiscoveryContext(test.request); appErr == nil || appErr.code != "daemon_context_invalid" {
				t.Fatalf("invalid context result = %#v", appErr)
			}
		})
	}
}
