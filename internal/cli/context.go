package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/discovery"
)

var gitRootTimeout = 5 * time.Second

type configContext struct {
	CallerCWD          string
	CWD                string
	Configs            []string
	Discovered         bool
	TrustedBoundary    string
	WorkspaceConfig    string
	WorkspaceDirectory string
}

type invocationContext struct {
	configContext
	ProjectRoot string
	GlobalRoot  string
}

type loadedConfiguration struct {
	Config       *config.Config
	DeclaredRoot *config.Root
}

func resolveConfigContext(callerCWD string, configured []string) (configContext, *appError) {
	canonicalCWD, err := canonicalDirectory(callerCWD)
	if err != nil {
		return configContext{}, transportError("caller_cwd_unavailable", err.Error(), "run Wirecmd from an accessible working directory")
	}
	result := configContext{CallerCWD: callerCWD, CWD: canonicalCWD, Discovered: len(configured) == 0}
	if len(configured) != 0 {
		result.Configs, err = absoluteConfigPaths(canonicalCWD, configured)
		if err != nil {
			return configContext{}, configurationError("config_path_invalid", err.Error(), "supply valid configuration paths")
		}
		return result, nil
	}
	discovered, err := discovery.Resolve(canonicalCWD)
	if err != nil {
		var untrusted *discovery.UntrustedError
		var notFound *discovery.NotFoundError
		switch {
		case errors.As(err, &untrusted):
			return configContext{}, userActionError("workspace_untrusted", err.Error(), "run wirecmd config trust "+shellQuote(untrusted.Workspace))
		case errors.As(err, &notFound):
			return configContext{}, configurationError("config_not_found", err.Error(), "create a global config.kdl or trusted workspace .wirecmd/config.kdl, or supply --config PATH")
		default:
			return configContext{}, configurationError("config_discovery_failed", err.Error(), "check Wirecmd configuration and trust state")
		}
	}
	result.CWD = discovered.CWD
	result.Configs = append([]string(nil), discovered.Paths...)
	result.TrustedBoundary = discovered.TrustedRoot
	result.WorkspaceConfig = discovered.WorkspaceConfig
	result.WorkspaceDirectory = discovered.WorkspaceDirectory
	return result, nil
}

func loadContextConfig(source configContext) (*loadedConfiguration, error) {
	composed, sources, err := config.LoadEffectiveSources(source.Configs, source.Discovered)
	if err != nil {
		return nil, err
	}
	loaded := &loadedConfiguration{Config: composed}
	switch {
	case !source.Discovered || source.WorkspaceConfig == "":
		loaded.DeclaredRoot = composed.Root
	default:
		for index, path := range source.Configs {
			if path == source.WorkspaceConfig {
				loaded.DeclaredRoot = sources[index].Root
				break
			}
		}
	}
	return loaded, nil
}

func resolveInvocationContext(ctx context.Context, source configContext, loaded *loadedConfiguration) (invocationContext, *appError) {
	resolved := invocationContext{configContext: source, GlobalRoot: globalWirecmdRoot()}
	declared := declaredProjectRoot(loaded.DeclaredRoot)
	if declared != "" {
		resolved.ProjectRoot = canonicalPath(declared)
		return resolved, nil
	}

	candidates := make([]string, 0, 2)
	if source.WorkspaceDirectory != "" {
		if candidate := validProjectCandidate(source.CWD, source.WorkspaceDirectory); candidate != "" {
			candidates = append(candidates, candidate)
		}
	}
	if loaded.Config.GitRoot.Enabled {
		if candidate := discoverGitRoot(ctx, source.CWD); candidate != "" {
			if candidate = validProjectCandidate(source.CWD, candidate); candidate != "" {
				candidates = append(candidates, candidate)
			}
		}
	}
	resolved.ProjectRoot = deepestPath(candidates)
	if resolved.ProjectRoot == "" && source.Discovered {
		resolved.ProjectRoot = validProjectCandidate(source.CWD, source.TrustedBoundary)
	}
	if resolved.ProjectRoot == "" {
		resolved.ProjectRoot = source.CWD
	}
	return resolved, nil
}

func declaredProjectRoot(root *config.Root) string {
	if root == nil {
		return ""
	}
	return resolveRoot(*root)
}

func canonicalDirectory(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", &os.PathError{Op: "chdir", Path: path, Err: errors.New("not a directory")}
	}
	return filepath.Clean(resolved), nil
}

func canonicalPath(path string) string {
	clean := filepath.Clean(path)
	suffix := make([]string, 0)
	for candidate := clean; ; candidate = filepath.Dir(candidate) {
		if resolved, err := filepath.EvalSymlinks(candidate); err == nil {
			for index := len(suffix) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, suffix[index])
			}
			return filepath.Clean(resolved)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return clean
		}
		suffix = append(suffix, filepath.Base(candidate))
	}
}

func validProjectCandidate(cwd, candidate string) string {
	if candidate == "" {
		return ""
	}
	resolved, err := canonicalDirectory(candidate)
	if err != nil || !pathContains(resolved, cwd) {
		return ""
	}
	return resolved
}

func pathContains(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func deepestPath(paths []string) string {
	deepest := ""
	for _, path := range paths {
		if deepest == "" || len(path) > len(deepest) {
			deepest = path
		}
	}
	return deepest
}

var runGitRoot = func(ctx context.Context, cwd string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, gitRootTimeout)
	defer cancel()
	command := exec.CommandContext(ctx, "git", "-C", cwd, "rev-parse", "--show-toplevel")
	command.Stderr = io.Discard
	output := &boundedOutput{limit: 64 * 1024}
	command.Stdout = output
	if err := command.Run(); err != nil {
		return nil, err
	}
	if output.overflow {
		return nil, errors.New("Git root output exceeded limit")
	}
	return output.Bytes(), nil
}

type boundedOutput struct {
	bytes.Buffer
	limit    int
	overflow bool
}

func (w *boundedOutput) Write(p []byte) (int, error) {
	original := len(p)
	remaining := w.limit - w.Len()
	if remaining < len(p) {
		w.overflow = true
		if remaining <= 0 {
			return original, nil
		}
		p = p[:remaining]
	}
	_, _ = w.Buffer.Write(p)
	return original, nil
}

func discoverGitRoot(ctx context.Context, cwd string) string {
	output, err := runGitRoot(ctx, cwd)
	if err != nil || len(output) > 64*1024 || bytes.IndexByte(output, 0) >= 0 {
		return ""
	}
	path := strings.TrimSpace(string(output))
	if path == "" || strings.ContainsAny(path, "\r\n") || !filepath.IsAbs(path) {
		return ""
	}
	return path
}

func globalWirecmdRoot() string {
	if configured := os.Getenv("XDG_CONFIG_HOME"); configured != "" {
		if filepath.IsAbs(configured) {
			return canonicalPath(filepath.Join(configured, "wirecmd"))
		}
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ""
	}
	return canonicalPath(filepath.Join(home, ".config", "wirecmd"))
}
