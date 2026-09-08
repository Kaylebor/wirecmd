package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFocusedHelpRequiresDaemonAfterDirectDiscoveryAndSecretSwitch(t *testing.T) {
	const secretName = "WIRECMD_FOCUSED_HELP_SECRET"
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
	t.Setenv(secretName, "first-credential")
	configPath := helperConfig(t, "", `env SECRET=(secret)"env://`+secretName+`"`)

	serverArgs := []string{"--direct", "--config", configPath, "helper", "--help"}
	serverCode, serverHelp, serverStderr := invoke(t, serverArgs)
	if serverCode != exitOK || serverStderr != "" || !strings.Contains(serverHelp, "Tools for helper:") {
		t.Fatalf("direct server help: code=%d stderr=%q output=%s", serverCode, serverStderr, serverHelp)
	}
	toolArgs := []string{"--direct", "--config", configPath, "--help", "helper", "projected"}
	toolCode, toolHelp, toolStderr := invoke(t, toolArgs)
	if toolCode != exitOK || toolStderr != "" || !strings.Contains(toolHelp, "--query") {
		t.Fatalf("direct tool help: code=%d stderr=%q output=%s", toolCode, toolStderr, toolHelp)
	}

	if err := os.Unsetenv(secretName); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		args []string
	}{
		{name: "server", args: []string{"--config", configPath, "helper", "--help"}},
		{name: "tool", args: []string{"--config", configPath, "--help", "helper", "projected"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, output, _ := invoke(t, test.args)
			if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "daemon_unavailable" {
				t.Fatalf("offline focused help: code=%d output=%s", code, output)
			}
		})
	}

	t.Setenv(secretName, "second-credential")
	startTestDaemon(t)
	serverCode, daemonServerHelp, serverStderr := invoke(t, []string{"--config", configPath, "helper", "--help"})
	if serverCode != exitOK || serverStderr != "" || daemonServerHelp != serverHelp {
		t.Fatalf("daemon server help: code=%d stderr=%q got=%s want=%s", serverCode, serverStderr, daemonServerHelp, serverHelp)
	}
	toolCode, daemonToolHelp, toolStderr := invoke(t, []string{"--config", configPath, "--help", "helper", "projected"})
	if toolCode != exitOK || toolStderr != "" || daemonToolHelp != toolHelp {
		t.Fatalf("daemon tool help: code=%d stderr=%q got=%s want=%s", toolCode, toolStderr, daemonToolHelp, toolHelp)
	}
}
