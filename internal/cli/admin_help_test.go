package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdministrativeHelpOffline(t *testing.T) {
	state := filepath.Join(t.TempDir(), "absent")
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "/not-an-available-runtime")
	t.Setenv("XDG_CONFIG_HOME", "/not-an-available-config")
	for _, args := range [][]string{
		{"daemon"}, {"daemon", "run"}, {"daemon", "status"}, {"daemon", "reload"},
		{"config"}, {"config", "trust"}, {"config", "trust", "/not-real"},
		{"config", "trust", "list"}, {"config", "trust", "status"},
		{"config", "trust", "status", "/not-real"}, {"config", "untrust", "/not-real"},
		{"auth"}, {"auth", "login"}, {"auth", "status"}, {"auth", "logout", "remote"},
	} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			code, output, stderr := invoke(t, append([]string{"--format", "json", "--color", "always", "--help"}, args...))
			if code != exitOK || stderr != "" || strings.Contains(output, "\x1b") || json.Valid([]byte(output)) || !strings.Contains(output, "wirecmd ") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, output, stderr)
			}
		})
	}
	code, _, _ := invoke(t, []string{"--config", "/missing", "--direct", "-h", "auth", "login", "remote"})
	if code != exitOK {
		t.Fatal("auth help must not load its selected configuration")
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("help touched trust/credential state: %v", err)
	}
}

func TestAdministrativeHelpInvalidForms(t *testing.T) {
	for _, args := range [][]string{
		{"--help", "daemon", "unknown"}, {"--help", "daemon", "reload", "extra"},
		{"--help", "config", "unknown"}, {"--help", "config", "trust", "status", "a", "b"},
		{"--help", "auth", "unknown"}, {"--help", "auth", "login", "a", "b"},
		{"--help", "--json", "{}", "daemon"}, {"--help", "--stdin", "auth"},
		{"--help", "--config", "missing", "daemon"}, {"--help", "--direct", "config"},
		{"--version", "--help", "daemon"},
	} {
		code, output, _ := invoke(t, args)
		if code != exitInvocation || !json.Valid([]byte(output)) {
			t.Fatalf("%v: code=%d output=%s", args, code, output)
		}
	}
}

func TestHelpPrefixSeparatorOwnership(t *testing.T) {
	for _, test := range []struct {
		args []string
		want bool
	}{
		{[]string{"--help", "--", "daemon", "status"}, true},
		{[]string{"--help", "--config", "--", "daemon"}, false},
		{[]string{"--help", "--config=--", "daemon"}, false},
		{[]string{"--help", "--config", "--", "--", "daemon"}, true},
		{[]string{"--help", "server", "tool", "--", "{}"}, false},
	} {
		opts, _, err := parseOptions(test.args)
		if err != nil || opts.helpServer != test.want {
			t.Fatalf("%v: separator=%v err=%v", test.args, opts.helpServer, err)
		}
	}
	// A real fixture proves the escape reaches ordinary tool help, not admin.
	t.Setenv("GO_WIRECMD_HELPER", "1")
	path := helperConfig(t, "", "")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"daemon", "config", "auth"} {
		config := writeConfig(t, strings.Replace(string(source), `server "helper"`, `server "`+name+`"`, 1))
		code, output, _ := invoke(t, []string{"--direct", "--config", config, "--help", "--", name, "a_tool"})
		if code != exitOK || !strings.Contains(output, "Input schema:") {
			t.Fatalf("%s escape: code=%d output=%s", name, code, output)
		}
	}
}

func TestNoArgumentHelp(t *testing.T) {
	text := renderToolHelp("test", toolDescription{Name: "empty", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)})
	if !strings.Contains(text, "No named arguments") || strings.Contains(text, "cannot be safely projected") {
		t.Fatal(text)
	}
}
