package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/toolcache"
)

func TestPersistentMetadataServesExactOfflineFocusedHelp(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
	configPath := helperConfig(t, "", "")

	serverCode, serverHelp, stderr := invoke(t, []string{"--direct", "--config", configPath, "helper", "--help"})
	if serverCode != exitOK || stderr != "" || !strings.Contains(serverHelp, "Tools for helper:") {
		t.Fatalf("direct server suffix help: code=%d stderr=%q output=%s", serverCode, stderr, serverHelp)
	}
	toolCode, toolHelp, stderr := invoke(t, []string{"--direct", "--config", configPath, "--help", "helper", "projected"})
	if toolCode != exitOK || stderr != "" || !strings.Contains(toolHelp, "--query") {
		t.Fatalf("direct tool help: code=%d stderr=%q output=%s", toolCode, stderr, toolHelp)
	}

	serverCode, cachedServerHelp, stderr := invoke(t, []string{"--config", configPath, "helper", "-h"})
	if serverCode != exitOK || stderr != "" || cachedServerHelp != serverHelp {
		t.Fatalf("offline server help: code=%d stderr=%q\ngot=%s\nwant=%s", serverCode, stderr, cachedServerHelp, serverHelp)
	}
	toolCode, cachedToolHelp, stderr := invoke(t, []string{"--config", configPath, "--help", "helper", "projected"})
	if toolCode != exitOK || stderr != "" || cachedToolHelp != toolHelp {
		t.Fatalf("offline tool help: code=%d stderr=%q\ngot=%s\nwant=%s", toolCode, stderr, cachedToolHelp, toolHelp)
	}
}

func TestPersistentMetadataMissAndIdentityMismatchPreserveDaemonUnavailable(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
	configPath := helperConfig(t, "", "")
	code, output, _ := invoke(t, []string{"--config", configPath, "helper", "--help"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "daemon_unavailable" {
		t.Fatalf("cache miss: code=%d output=%s", code, output)
	}
	code, _, stderr := invoke(t, []string{"--direct", "--config", configPath, "helper", "--help"})
	if code != exitOK || stderr != "" {
		t.Fatalf("populate cache: code=%d stderr=%q", code, stderr)
	}
	changedPath := filepath.Join(t.TempDir(), "changed.kdl")
	helperConfigAt(t, changedPath, "", `env CACHE_IDENTITY="changed"`)
	code, output, _ = invoke(t, []string{"--config", changedPath, "helper", "--help"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "daemon_unavailable" {
		t.Fatalf("identity mismatch: code=%d output=%s", code, output)
	}
}

func TestOfflineMetadataDoesNotHideMissingSelectedSecret(t *testing.T) {
	const secretName = "WIRECMD_CACHE_REQUIRED_SECRET"
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
	t.Setenv(secretName, "available-while-caching")
	configPath := helperConfig(t, "", `env SECRET=(secret)"env://`+secretName+`"`)
	code, _, stderr := invoke(t, []string{"--direct", "--config", configPath, "helper", "--help"})
	if code != exitOK || stderr != "" {
		t.Fatalf("populate secret-bound cache: code=%d stderr=%q", code, stderr)
	}
	if err := os.Unsetenv(secretName); err != nil {
		t.Fatal(err)
	}
	code, output, _ := invoke(t, []string{"--config", configPath, "helper", "--help"})
	if code != exitConfiguration || decodeOutput(t, output)["error"].(map[string]any)["code"] != "secret_not_available" {
		t.Fatalf("missing secret hidden by cache: code=%d output=%s", code, output)
	}
}

func TestOfflineMetadataDoesNotHideInvalidResolvedHTTPValue(t *testing.T) {
	const secretName = "WIRECMD_CACHE_HTTP_HEADER"
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
	t.Setenv(secretName, "Bearer valid")
	fixture := newHTTPFixture(t)
	configPath := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.URL+`" { header Authorization=(secret)"env://`+secretName+`" } } }`)
	code, _, stderr := invoke(t, []string{"--direct", "--config", configPath, "remote", "--help"})
	if code != exitOK || stderr != "" {
		t.Fatalf("populate HTTP cache: code=%d stderr=%q", code, stderr)
	}
	t.Setenv(secretName, "bad\r\nInjected: yes")
	code, output, _ := invoke(t, []string{"--config", configPath, "remote", "--help"})
	if code != exitConfiguration || decodeOutput(t, output)["error"].(map[string]any)["code"] != "invalid_http_value" {
		t.Fatalf("invalid HTTP value hidden by cache: code=%d output=%s", code, output)
	}
}

func TestPersistentMetadataIsRedactedAndOpaque(t *testing.T) {
	const secret = "metadata-cache-secret"
	t.Setenv("GO_WIRECMD_HELPER", "1")
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
	t.Setenv("SECRET", secret)
	configPath := helperConfig(t, "", `env SECRET=(secret)"env://SECRET"`)
	code, output, stderr := invoke(t, []string{"--direct", "--config", configPath, "helper", "--help"})
	if code != exitOK || strings.Contains(output, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("live help leaked secret: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	cacheDir := filepath.Join(stateHome, "wirecmd", "toolcache")
	entries, err := os.ReadDir(cacheDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), "helper") || strings.Contains(entry.Name(), secret) {
			t.Fatalf("non-opaque cache filename %q", entry.Name())
		}
	}
	assertStateTreeDoesNotContain(t, cacheDir, secret)
}

func TestProjectedSchemaCacheIsRedacted(t *testing.T) {
	const secret = "projected-cache-secret"
	flag, ok := projectedFlag(secret)
	if !ok {
		t.Fatal("fixture secret must form a projected flag")
	}
	for _, daemonBacked := range []bool{false, true} {
		name := "direct"
		if daemonBacked {
			name = "daemon"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("GO_WIRECMD_HELPER", "1")
			stateHome := filepath.Join(t.TempDir(), "state")
			t.Setenv("XDG_STATE_HOME", stateHome)
			t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
			t.Setenv("SECRET", secret)
			configPath := helperConfig(t, "", `env SECRET=(secret)"env://SECRET"`)
			args := []string{"--config", configPath, "helper", "semantic_secret", "--" + flag, "value"}
			if daemonBacked {
				startTestDaemon(t)
			} else {
				args = append([]string{"--direct"}, args...)
			}
			code, output, stderr := invoke(t, args)
			if code != exitOK || strings.Contains(output, secret) || strings.Contains(stderr, secret) {
				t.Fatalf("projected call: code=%d stdout=%q stderr=%q", code, output, stderr)
			}
			assertStateTreeDoesNotContain(t, filepath.Join(stateHome, "wirecmd", "toolcache"), secret)
		})
	}
}

func TestDirectDiscoveryRefreshesCacheBeforeDownstreamFailure(t *testing.T) {
	for _, daemonBacked := range []bool{false, true} {
		name := "direct projected arguments"
		if daemonBacked {
			name = "daemon projected arguments"
		}
		t.Run(name, func(t *testing.T) {
			t.Setenv("GO_WIRECMD_HELPER", "1")
			t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
			t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
			configPath := helperConfig(t, "", "")
			args := []string{"--config", configPath, "helper", "projected", "--missing", "value"}
			if daemonBacked {
				startTestDaemon(t)
			} else {
				args = append([]string{"--direct"}, args...)
			}
			code, output, _ := invoke(t, args)
			if code != exitInvocation || decodeOutput(t, output)["error"].(map[string]any)["code"] != "projected_argument_unknown" {
				t.Fatalf("failed projected call: code=%d output=%s", code, output)
			}
			cfg, err := config.LoadEffective([]string{configPath})
			if err != nil {
				t.Fatal(err)
			}
			store, err := toolcache.New()
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := store.Load(toolMetadataIdentity(cfg, cfg.Servers[0], mustCWD(t)))
			if err != nil || len(catalog.Tools) != 1 || catalog.Tools[0].Name != "projected" || !catalog.Tools[0].Detailed {
				t.Fatalf("cached failed discovery = %#v, %v", catalog, err)
			}
		})
	}
	t.Run("HTTP priming", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
		t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
		fixture := newHTTPFixture(t)
		configPath := httpConfig(t, fixture.URL)
		code, _, _ := invoke(t, []string{"--direct", "--config", configPath, "remote", "missing_tool"})
		if code == exitOK {
			t.Fatal("missing HTTP tool unexpectedly succeeded")
		}
		code, output, stderr := invoke(t, []string{"--config", configPath, "remote", "--help"})
		if code != exitOK || stderr != "" || !strings.Contains(output, "Tools for remote:") {
			t.Fatalf("offline help after primed call failure: code=%d stderr=%q output=%s", code, stderr, output)
		}
	})
}

func TestDaemonDiscoveryRefreshesPersistentMetadata(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
	startTestDaemon(t)
	configPath := helperConfig(t, "", "")
	code, output, stderr := invoke(t, []string{"--config", configPath, "helper", "--help"})
	if code != exitOK || stderr != "" || !strings.Contains(output, "Tools for helper:") {
		t.Fatalf("daemon server help: code=%d stderr=%q output=%s", code, stderr, output)
	}
	cfg, err := config.LoadEffective([]string{configPath})
	if err != nil {
		t.Fatal(err)
	}
	store, err := toolcache.New()
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := store.Load(toolMetadataIdentity(cfg, cfg.Servers[0], mustCWD(t)))
	if err != nil {
		t.Fatal(err)
	}
	if !catalog.Complete || len(catalog.Tools) == 0 {
		t.Fatalf("daemon cached catalog = %#v", catalog)
	}
}

func TestMetadataWriteFailureDoesNotFailLiveOperation(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)
	if err := os.MkdirAll(filepath.Join(stateHome, "wirecmd"), 0o755); err != nil {
		t.Fatal(err)
	}
	configPath := helperConfig(t, "", "")
	code, output, stderr := invoke(t, []string{"--direct", "--config", configPath, "helper", "--help"})
	if code != exitOK || !strings.Contains(output, "Tools for helper:") || strings.Count(stderr, "offline focused help may be unavailable") != 1 {
		t.Fatalf("explicit help cache failure: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"--direct", "--config", configPath, "helper"})
	if code != exitOK || stderr != "" || !strings.Contains(output, `"tools"`) {
		t.Fatalf("ordinary discovery cache failure: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func mustCWD(t *testing.T) string {
	t.Helper()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return cwd
}

func assertStateTreeDoesNotContain(t *testing.T, root, secret string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), secret) {
			t.Fatalf("cache file %q leaked secret", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
