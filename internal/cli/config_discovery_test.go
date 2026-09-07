package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func discoveryEnvironment(t *testing.T) (configHome, stateHome string) {
	t.Helper()
	home := t.TempDir()
	configHome = filepath.Join(home, "config")
	stateHome = filepath.Join(home, "state")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)
	return configHome, stateHome
}

func copyConfig(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestConfigTrustAdminAndAutomaticDiscovery(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	discoveryEnvironment(t)
	workspace := t.TempDir()
	child := filepath.Join(workspace, "child")
	if err := os.Mkdir(child, 0o700); err != nil {
		t.Fatal(err)
	}
	explicit := helperConfig(t, "", "")
	copyConfig(t, explicit, filepath.Join(workspace, "wirecmd.kdl"))
	t.Chdir(child)

	code, output, _ := invoke(t, nil)
	if code != exitUserAction || decodeOutput(t, output)["error"].(map[string]any)["code"] != "workspace_untrusted" || !strings.Contains(output, "wirecmd config trust") {
		t.Fatalf("untrusted discovery: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"config", "trust", workspace})
	if code != exitOK || decodeOutput(t, output)["trust"].(map[string]any)["status"] != "trusted" {
		t.Fatalf("trust: code=%d output=%s", code, output)
	}
	code, output, stderr := invoke(t, []string{"--direct", "helper"})
	if code != exitOK || stderr != "" || !strings.Contains(output, `"tools"`) {
		t.Fatalf("automatic invocation: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, _ = invoke(t, []string{"config", "trust", "status"})
	if code != exitOK || decodeOutput(t, output)["trust"].(map[string]any)["trusted"] != true {
		t.Fatalf("status: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"config", "trust", "list"})
	if code != exitOK || len(decodeOutput(t, output)["trust"].(map[string]any)["workspaces"].([]any)) != 1 {
		t.Fatalf("list: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"config", "untrust", workspace})
	if code != exitOK || decodeOutput(t, output)["trust"].(map[string]any)["status"] != "untrusted" {
		t.Fatalf("untrust: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"--direct", "--config", explicit, "helper"})
	if code != exitOK {
		t.Fatalf("explicit bypass: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"--direct", "config", "trust"})
	if code != exitInvocation || decodeOutput(t, output)["error"].(map[string]any)["code"] != "config_admin_flags" {
		t.Fatalf("admin flags: code=%d output=%s", code, output)
	}
}

func TestGlobalDiscoveryAndNoConfig(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	configHome, _ := discoveryEnvironment(t)
	workspace := t.TempDir()
	t.Chdir(workspace)
	code, output, _ := invoke(t, nil)
	if code != exitConfiguration || decodeOutput(t, output)["error"].(map[string]any)["code"] != "config_not_found" {
		t.Fatalf("no config: code=%d output=%s", code, output)
	}
	copyConfig(t, helperConfig(t, "", ""), filepath.Join(configHome, "wirecmd", "config.kdl"))
	code, output, _ = invoke(t, []string{"--direct", "helper"})
	if code != exitOK || !strings.Contains(output, `"tools"`) {
		t.Fatalf("global discovery: code=%d output=%s", code, output)
	}
}

func TestConfigServerJSONEscape(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	discoveryEnvironment(t)
	source := helperConfig(t, "", "")
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "config.kdl")
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), `mcp "helper"`, `mcp "config"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	code, output, _ := invoke(t, []string{"--direct", "--config", path, "--json", `{}`, "config", "a_tool"})
	if code != exitOK || !strings.Contains(output, `"server":"config"`) {
		t.Fatalf("config server escape: code=%d output=%s", code, output)
	}
}

func TestTrustActionShellQuoteRoundTrips(t *testing.T) {
	want := "workspace 'quote'\n\tend"
	command := exec.Command("/bin/sh", "-c", "set -- "+shellQuote(want)+"; printf %s \"$1\"")
	got, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("shell-quoted value = %q, want %q", got, want)
	}
}

func TestDaemonDiscoveryKeepsCallerWorkspacesSeparate(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	discoveryEnvironment(t)
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	source := helperConfig(t, "", "")
	first := t.TempDir()
	second := t.TempDir()
	copyConfig(t, source, filepath.Join(first, "wirecmd.kdl"))
	copyConfig(t, source, filepath.Join(second, "wirecmd.kdl"))
	for _, workspace := range []string{first, second} {
		if code, output, _ := invoke(t, []string{"config", "trust", workspace}); code != exitOK {
			t.Fatalf("trust %s: code=%d output=%s", workspace, code, output)
		}
	}
	_ = startTestDaemon(t)
	for _, workspace := range []string{first, second} {
		t.Chdir(workspace)
		code, output, stderr := invoke(t, []string{"helper", "working_directory"})
		if code != exitOK || stderr != "" {
			t.Fatalf("workspace %s: code=%d stderr=%q output=%s", workspace, code, stderr, output)
		}
		cwd := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["cwd"]
		if cwd != workspace {
			t.Fatalf("workspace result = %v, want %s", cwd, workspace)
		}
	}
	code, output, _ := invoke(t, []string{"daemon", "status"})
	if code != exitOK || decodeOutput(t, output)["daemon"].(map[string]any)["cached_contexts"].(json.Number).String() != "2" {
		t.Fatalf("daemon contexts: code=%d output=%s", code, output)
	}
}
