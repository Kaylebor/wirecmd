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

func TestAdministrativeLeafHelpIsFocused(t *testing.T) {
	for _, test := range []struct {
		args   []string
		want   []string
		absent string
	}{
		{[]string{"daemon", "run"}, []string{"Run the foreground daemon.", "daemon run", "does not load configuration"}, "Daemon administration:"},
		{[]string{"daemon", "status"}, []string{"Inspect the running daemon.", "daemon status", "daemon_unavailable"}, "Daemon administration:"},
		{[]string{"daemon", "reload"}, []string{"Reload daemon-managed configuration and sessions.", "daemon reload", "Active requests finish"}, "Daemon administration:"},
		{[]string{"config", "trust"}, []string{"Trust a workspace", "config trust [PATH]", "Trust is recursive"}, "Configuration trust:"},
		{[]string{"config", "untrust"}, []string{"Remove one workspace trust entry.", "config untrust [PATH]", "trusted ancestor"}, "Configuration trust:"},
		{[]string{"config", "trust", "status"}, []string{"Inspect effective workspace trust.", "config trust status [PATH]", "nearest trust root"}, "Configuration trust:"},
		{[]string{"config", "trust", "list"}, []string{"List trusted workspace roots.", "config trust list", "does not search"}, "Configuration trust:"},
		{[]string{"auth", "login"}, []string{"Authenticate one configured HTTP server.", "auth login SERVER", "fresh browser authorization"}, "OAuth credentials:"},
		{[]string{"auth", "status"}, []string{"Inspect local credentials", "auth status SERVER", "without contacting the provider"}, "OAuth credentials:"},
		{[]string{"auth", "logout"}, []string{"Delete local credentials", "auth logout SERVER", "does not revoke provider-side tokens"}, "OAuth credentials:"},
	} {
		t.Run(strings.Join(test.args, "_"), func(t *testing.T) {
			code, output, stderr := invoke(t, append([]string{"--help"}, test.args...))
			if code != exitOK || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, output, stderr)
			}
			for _, want := range test.want {
				if !strings.Contains(output, want) {
					t.Fatalf("help missing %q:\n%s", want, output)
				}
			}
			if strings.Contains(output, test.absent) {
				t.Fatalf("leaf help repeated group page:\n%s", output)
			}
		})
	}
}

func TestMisplacedAdministrativeFlagsAreContextualAndDoNotTouchState(t *testing.T) {
	state := filepath.Join(t.TempDir(), "absent")
	t.Setenv("XDG_STATE_HOME", state)
	for _, test := range []struct {
		args    []string
		flag    string
		command string
	}{
		{[]string{"config", "trust", "--direct"}, "--direct", "wirecmd config trust"},
		{[]string{"config", "trust", "-direct"}, "-direct", "wirecmd config trust"},
		{[]string{"config", "untrust", "--config=project.kdl"}, "--config", "wirecmd config untrust"},
		{[]string{"config", "trust", "status", "--stdin"}, "--stdin", "wirecmd config trust status"},
		{[]string{"auth", "login", "remote", "--direct"}, "--direct", "wirecmd auth login"},
		{[]string{"auth", "status", "--direct"}, "--direct", "wirecmd auth status"},
		{[]string{"auth", "logout", "remote", "--json", `{}`}, "--json", "wirecmd auth logout"},
		{[]string{"--help", "daemon", "reload", "--direct"}, "--direct", "wirecmd daemon reload"},
		{[]string{"--help", "config", "trust", "--direct"}, "--direct", "wirecmd config trust"},
		{[]string{"--help", "config", "trust", "list", "--color=always"}, "--color", "wirecmd config trust list"},
		{[]string{"--help", "auth", "login", "remote", "--stdin"}, "--stdin", "wirecmd auth login"},
	} {
		t.Run(strings.Join(test.args, "_"), func(t *testing.T) {
			code, output, stderr := invoke(t, test.args)
			if code != exitInvocation || stderr != "" {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, output, stderr)
			}
			error := decodeOutput(t, output)["error"].(map[string]any)
			if error["code"] != "admin_misplaced_flag" || !strings.Contains(error["message"].(string), test.flag) || !strings.Contains(error["message"].(string), test.command) {
				t.Fatalf("unexpected error for %v: %s", test.args, output)
			}
		})
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatalf("misplaced administrative flags touched trust or credential state: %v", err)
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
		config := writeConfig(t, strings.Replace(string(source), `mcp "helper"`, `mcp "`+name+`"`, 1))
		code, output, _ := invoke(t, []string{"--direct", "--config", config, "--help", "--", name, "a_tool"})
		if code != exitOK || !strings.Contains(output, "Input schema:") {
			t.Fatalf("%s escape: code=%d output=%s", name, code, output)
		}
	}
}

func TestHelpPrefixSeparatorPreservesReservedLeafCollisions(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	path := helperConfig(t, "", "")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		server string
		tool   string
	}{
		{"daemon", "status"},
		{"config", "trust"},
		{"auth", "login"},
	} {
		t.Run(test.server+"_"+test.tool, func(t *testing.T) {
			config := writeConfig(t, strings.Replace(string(source), `mcp "helper"`, `mcp "`+test.server+`"`, 1))
			code, output, _ := invoke(t, []string{"--direct", "--config", config, "--help", "--", test.server, test.tool})
			if code != exitProtocol || decodeOutput(t, output)["error"].(map[string]any)["code"] != "tool_not_found" {
				t.Fatalf("reserved leaf escape: code=%d output=%s", code, output)
			}
		})
	}
}

func TestAdministrativeNamesKeepNormalToolSuffixOwnership(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	path := helperConfig(t, "", "")
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		server string
		tool   string
		args   []string
	}{
		{"daemon", "status", []string{"--format", "pretty"}},
		{"config", "trust", []string{"--format", "pretty"}},
	} {
		t.Run(test.server+"_"+test.tool, func(t *testing.T) {
			config := writeConfig(t, strings.Replace(string(source), `mcp "helper"`, `mcp "`+test.server+`"`, 1))
			args := append([]string{"--direct", "--config", config, test.server, test.tool}, test.args...)
			code, output, _ := invoke(t, args)
			if code != exitProtocol || decodeOutput(t, output)["error"].(map[string]any)["code"] != "tool_not_found" {
				t.Fatalf("tool suffix was treated as administration: code=%d output=%s", code, output)
			}
		})
	}
	for _, test := range []struct {
		server string
		tool   string
	}{
		{"daemon", "status"},
		{"config", "trust"},
		{"auth", "login"},
	} {
		t.Run(test.server+"_json_"+test.tool, func(t *testing.T) {
			config := writeConfig(t, strings.Replace(string(source), `mcp "helper"`, `mcp "`+test.server+`"`, 1))
			code, output, _ := invoke(t, []string{"--direct", "--config", config, "--json", `{}`, test.server, test.tool})
			if code != exitProtocol || decodeOutput(t, output)["error"].(map[string]any)["code"] != "tool_call_failed" {
				t.Fatalf("JSON escape was treated as administration: code=%d output=%s", code, output)
			}
		})
		t.Run(test.server+"_stdin_"+test.tool, func(t *testing.T) {
			config := writeConfig(t, strings.Replace(string(source), `mcp "helper"`, `mcp "`+test.server+`"`, 1))
			code, output, _ := invokeWithInput(t, []string{"--direct", "--config", config, "--stdin", test.server, test.tool}, `{}`)
			if code != exitProtocol || decodeOutput(t, output)["error"].(map[string]any)["code"] != "tool_call_failed" {
				t.Fatalf("stdin escape was treated as administration: code=%d output=%s", code, output)
			}
		})
		t.Run(test.server+"_exact_"+test.tool, func(t *testing.T) {
			config := writeConfig(t, strings.Replace(string(source), `mcp "helper"`, `mcp "`+test.server+`"`, 1))
			call := `{"tool":"` + test.tool + `","arguments":{}}`
			code, output, _ := invoke(t, []string{"--direct", "--config", config, test.server, call})
			if code != exitProtocol || decodeOutput(t, output)["error"].(map[string]any)["code"] != "tool_call_failed" {
				t.Fatalf("exact-call escape was treated as administration: code=%d output=%s", code, output)
			}
		})
	}
}

func TestNoArgumentHelp(t *testing.T) {
	text := renderToolHelp("test", toolDescription{Name: "empty", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)})
	if !strings.Contains(text, "No named arguments") || strings.Contains(text, "cannot be safely projected") {
		t.Fatal(text)
	}
}
