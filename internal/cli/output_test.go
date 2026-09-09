package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestOutputOptionsAndVersionOwnership(t *testing.T) {
	opts, positionals, err := parseOptions([]string{"--format", "pretty", "--color", "always", "--colour", "never", "server"})
	if err != nil || len(positionals) != 1 || positionals[0] != "server" {
		t.Fatalf("parseOptions() = %#v, %#v, %v", opts, positionals, err)
	}
	if opts.format != formatPretty || !opts.formatSet || opts.color != colorNever || !opts.colorSet {
		t.Fatalf("options = %#v", opts)
	}
	for _, args := range [][]string{{"--format", "yaml"}, {"--color", "sometimes"}, {"--colour", "bright"}} {
		if _, _, err := parseOptions(args); err == nil {
			t.Fatalf("parseOptions(%v) unexpectedly succeeded", args)
		}
	}
	code, output, stderr := invoke(t, []string{"--version", "--format", "pretty"})
	if code != exitInvocation || stderr != "" || !strings.Contains(output, "Code: version_usage") {
		t.Fatalf("version with format: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	code, output, stderr = invoke(t, []string{"--version", "--colour", "always"})
	if code != exitInvocation || stderr != "" || !strings.Contains(output, ansiReset) {
		t.Fatalf("version with color: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	_, positionals, err = parseOptions([]string{"server", "tool", "--format", "pretty", "--color=always", "--colour", "never"})
	if err != nil || strings.Join(positionals, "|") != "server|tool|--format|pretty|--color=always|--colour|never" {
		t.Fatalf("suffix output flags must belong to the tool: %v, %v", positionals, err)
	}
	if _, _, err := parseProjectedSuffix(positionals[2:]); err != nil {
		t.Fatalf("suffix output flags must remain projectable: %v", err)
	}
	opts, _, err = parseOptions([]string{"--format", "pretty", "daemon", "status"})
	if err != nil {
		t.Fatal(err)
	}
	if admin, ok := parseDaemonAdmin([]string{"daemon", "status"}, opts); !ok || admin.err != nil {
		t.Fatalf("daemon admin should accept output flags: %#v, %v", admin, ok)
	}
}

func TestPresentationDefaultsAndOverrides(t *testing.T) {
	previous := isOutputTerminal
	t.Cleanup(func() { isOutputTerminal = previous })
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "xterm-256color")
	isOutputTerminal = func(io.Writer) bool { return false }
	if got := presentationFor(options{}, &bytes.Buffer{}); got != (presentation{}) {
		t.Fatalf("non-terminal auto presentation = %#v", got)
	}
	isOutputTerminal = func(io.Writer) bool { return true }
	if got := presentationFor(options{}, &bytes.Buffer{}); got != (presentation{pretty: true, color: true}) {
		t.Fatalf("terminal auto presentation = %#v", got)
	}
	t.Setenv("NO_COLOR", "1")
	if got := presentationFor(options{}, &bytes.Buffer{}); got != (presentation{pretty: true}) {
		t.Fatalf("NO_COLOR presentation = %#v", got)
	}
	t.Setenv("NO_COLOR", "")
	t.Setenv("TERM", "dumb")
	if got := presentationFor(options{}, &bytes.Buffer{}); got != (presentation{pretty: true}) {
		t.Fatalf("TERM=dumb presentation = %#v", got)
	}
	if got := presentationFor(options{format: formatJSON, color: colorAlways}, &bytes.Buffer{}); got != (presentation{color: true}) {
		t.Fatalf("explicit JSON/color presentation = %#v", got)
	}
	if got := presentationFor(options{format: formatPretty, color: colorNever}, &bytes.Buffer{}); got != (presentation{pretty: true}) {
		t.Fatalf("explicit pretty/no-color presentation = %#v", got)
	}
}

func TestTerminalOutputRejectsDevNull(t *testing.T) {
	file, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if terminalOutput(file) {
		t.Fatal("/dev/null must not be treated as a terminal")
	}
}

func TestPrettyOutputFamiliesAndControlEscaping(t *testing.T) {
	style := presentation{pretty: true}
	for _, test := range []struct {
		name  string
		value any
		want  []string
	}{
		{"servers", serversEnvelope{OK: true, Servers: []serverSummary{{Name: "memory", Scope: "workspace", Transport: "stdio"}}}, []string{"SERVER", "memory", "workspace", "stdio"}},
		{"tools", toolsEnvelope{OK: true, Server: "memory", Tools: []toolSummary{{Name: "look", Title: "Look", Description: "first line\nsecond\tline\x1b[2J"}}}, []string{"Tools for memory", "NAME", "first line\nsecond\tline\\x1B[2J"}},
		{"resources", resourcesEnvelope{OK: true, Server: "memory\x1b[31m", Resources: []resourceSummary{{URI: "test://resource?token=[REDACTED]\x1b[2J", Name: "na\rme", Title: "Title", MIMEType: "text/plain", Size: 42, Description: "first line\nsecond\tline\x1b[2J"}}}, []string{"Resources for memory\\x1B[31m", "URI", "test://resource?token=[REDACTED]\\x1B[2J", "first line\nsecond\tline\\x1B[2J"}},
		{"resource-templates", resourceTemplatesEnvelope{OK: true, Server: "memory", ResourceTemplates: []resourceTemplateSummary{{URITemplate: "test://resource/{id}\x1b[2J", Name: "name", Title: "Title", MIMEType: "text/plain", Description: "template\x1b[2J"}}}, []string{"Resource templates for memory", "URI TEMPLATE", "test://resource/{id}\\x1B[2J", "template\\x1B[2J"}},
		{"auth", authEnvelope{OK: true, Auth: authStatus{Server: "remote", Status: "authenticated", Registration: "dynamic", ExpiresAt: "2026-01-01T00:00:00Z"}}, []string{"Authentication", "Registration: dynamic", "Expires: 2026"}},
		{"trust", map[string]any{"ok": true, "trust": map[string]any{"workspace": "/work", "status": "trusted"}}, []string{"Workspace trust", "Status: trusted", "Workspace: /work"}},
		{"trust-list", map[string]any{"ok": true, "trust": map[string]any{"workspaces": []string{"/one", "/two"}}}, []string{"Trusted workspaces", "/one", "/two"}},
		{"daemon", map[string]any{"ok": true, "daemon": map[string]any{"status": "running", "pid": 42, "protocol": 4, "active_instances": 1}}, []string{"Daemon", "PID: 42", "Active instances: 1"}},
		{"reload", map[string]any{"ok": true, "reload": map[string]any{"contexts_retired": 2, "instances_retired": 3}}, []string{"Daemon reload", "Contexts retired: 2", "Instances retired: 3"}},
		{"failure", failure{OK: false, Error: errorBody{Category: "transport", Code: "offline", Message: "not connected", Action: "start daemon"}, Result: &toolResult{Data: map[string]any{"count": 2}, Messages: []string{"failed"}}}, []string{"Error", "Category: transport", "Action: start daemon", "Result", "\"count\": 2"}},
		{"call", callEnvelope{OK: true, Server: "memory", Tool: "look", Result: toolResult{Data: map[string]any{"count": 1234567890123456789}, Messages: []string{}}}, []string{"\n  \"ok\": true", "1234567890123456789", "\"messages\": []"}},
		{"signature-help", lspSignatureEnvelope{OK: true, LSP: lspSignatureResult{Operation: lspSignatureHelp, File: "/work/main.go", Signatures: []lspSignature{{Provider: "fixture", Label: "call(value)", Active: true, Parameters: []lspSignatureParameter{{Label: "value", Active: true}}}}}}, []string{"\n    \"operation\": \"signature-help\"", "\"signatures\": [", "call(value)"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			writeOutput(&output, test.value, style)
			for _, want := range test.want {
				if !strings.Contains(output.String(), want) {
					t.Fatalf("output missing %q:\n%s", want, output.String())
				}
			}
			if strings.Contains(output.String(), "\x1b") {
				t.Fatalf("untrusted terminal control leaked: %q", output.String())
			}
		})
	}
}

func TestJSONColorAndCompactBaseline(t *testing.T) {
	value := map[string]any{"control": "\u009b", "count": 2, "enabled": true}
	var compact, colored bytes.Buffer
	writeOutput(&compact, value, presentation{})
	writeOutput(&colored, value, presentation{color: true})
	var baseline bytes.Buffer
	writeJSON(&baseline, value)
	if compact.String() != baseline.String() || strings.Contains(compact.String(), ansiReset) || strings.Count(compact.String(), "\n") != 1 {
		t.Fatalf("compact JSON changed: %q", compact.String())
	}
	if !strings.Contains(colored.String(), ansiReset) || !strings.Contains(colored.String(), `\u009b`) || strings.Count(colored.String(), "\n") != 1 {
		t.Fatalf("colored compact JSON = %q", colored.String())
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(stripANSI(colored.String())), &decoded); err != nil || decoded["control"] != "\u009b" || decoded["count"].(float64) != 2 {
		t.Fatalf("colored JSON changed semantics: decoded=%#v err=%v", decoded, err)
	}
}

func TestPrettyRenderingDirectAndDaemonShapesAgree(t *testing.T) {
	value := toolsEnvelope{OK: true, Server: "memory", Tools: []toolSummary{{Name: "look", Title: "Look", Description: "inspect"}}}
	daemonValue, ok := outputObject(value)
	if !ok {
		t.Fatal("could not normalize daemon-shaped value")
	}
	var direct, daemon bytes.Buffer
	writeOutput(&direct, value, presentation{pretty: true})
	writeOutput(&daemon, daemonValue, presentation{pretty: true})
	if direct.String() != daemon.String() {
		t.Fatalf("direct and daemon rendering differ:\ndirect:\n%sdaemon:\n%s", direct.String(), daemon.String())
	}
}

func TestPrettyOutputHandlesEmptyListsOptionalFieldsAndOrder(t *testing.T) {
	for _, value := range []any{
		map[string]any{"ok": true, "trust": map[string]any{"workspaces": nil}},
		map[string]any{"ok": true, "trust": map[string]any{"workspaces": []any{}}},
		toolsEnvelope{OK: true, Server: "memory", Tools: []toolSummary{}},
		resourcesEnvelope{OK: true, Server: "memory", Resources: []resourceSummary{}},
		resourceTemplatesEnvelope{OK: true, Server: "memory", ResourceTemplates: []resourceTemplateSummary{}},
		authEnvelope{OK: true, Auth: authStatus{Server: "remote", Status: "unauthenticated"}},
	} {
		var output bytes.Buffer
		writeOutput(&output, value, presentation{pretty: true})
		if output.Len() == 0 {
			t.Fatalf("empty/optional output rendered nothing for %#v", value)
		}
		if object, ok := outputObject(value); ok && object["trust"] != nil && !strings.Contains(output.String(), "Trusted workspaces") {
			t.Fatalf("empty trust list fell through to generic rendering: %q", output.String())
		}
		if object, ok := outputObject(value); ok && object["resources"] != nil && !strings.Contains(output.String(), "Resources for memory") {
			t.Fatalf("empty resource list fell through to generic rendering: %q", output.String())
		}
		if object, ok := outputObject(value); ok && object["resource_templates"] != nil && !strings.Contains(output.String(), "Resource templates for memory") {
			t.Fatalf("empty resource template list fell through to generic rendering: %q", output.String())
		}
	}
	var authOutput bytes.Buffer
	writeOutput(&authOutput, authEnvelope{OK: true, Auth: authStatus{Server: "remote", Status: "unauthenticated"}}, presentation{pretty: true})
	if strings.Contains(authOutput.String(), "Registration") || strings.Contains(authOutput.String(), "Expires") {
		t.Fatalf("optional auth fields leaked: %q", authOutput.String())
	}
	var output bytes.Buffer
	writeOutput(&output, toolsEnvelope{OK: true, Server: "memory", Tools: []toolSummary{{Name: "second"}, {Name: "first"}}}, presentation{pretty: true})
	if strings.Index(output.String(), "second") > strings.Index(output.String(), "first") {
		t.Fatalf("tool order changed: %q", output.String())
	}
}

func TestResourceListingsHonorPrettyColorAndJSONModes(t *testing.T) {
	value := resourcesEnvelope{OK: true, Server: "memory", Resources: []resourceSummary{{URI: "test://resource", Name: "resource", Description: "description"}}}
	var pretty, compact bytes.Buffer
	writeOutput(&pretty, value, presentation{pretty: true, color: true})
	writeOutput(&compact, value, presentation{})
	if !strings.Contains(pretty.String(), ansiReset) || !strings.Contains(pretty.String(), "Resources for") || strings.Contains(pretty.String(), "\"resources\"") {
		t.Fatalf("colored pretty resources = %q", pretty.String())
	}
	if strings.Count(compact.String(), "\n") != 1 || strings.Contains(compact.String(), ansiReset) {
		t.Fatalf("compact resource JSON = %q", compact.String())
	}
	decoded := decodeOutput(t, compact.String())
	if decoded["server"] != "memory" || len(decoded["resources"].([]any)) != 1 {
		t.Fatalf("compact resource JSON semantics changed: %#v", decoded)
	}
}

func TestOutputEncodingFailureUsesExistingFallback(t *testing.T) {
	var output bytes.Buffer
	writeOutput(&output, map[string]any{"unsupported": make(chan int)}, presentation{pretty: true})
	decoded := decodeOutput(t, output.String())
	if decoded["ok"] != false || decoded["error"].(map[string]any)["code"] != "output_encode_failed" {
		t.Fatalf("encoding fallback = %#v", decoded)
	}
}

func TestPrettyLSPHoverIsReadableAndTerminalSafe(t *testing.T) {
	value := lspHoverEnvelope{OK: true, LSP: lspHoverResult{
		Operation: lspHover,
		File:      "/work/main.go",
		Line:      2,
		Column:    3,
		Partial:   true,
		Hovers: []lspHoverEntry{{Provider: "first\x1b[31m", Contents: []lspHoverContent{
			{Kind: "markdown", Text: "**literal**\n\x1b[2J"},
			{Kind: "code", Language: "go", Text: "func main() {}"},
		}}},
		Providers: []lspHoverProviderOutcome{{Name: "first", Status: "ok", Hovers: 1}, {Name: "second", Status: "unsupported"}},
	}}
	var output bytes.Buffer
	writeOutput(&output, value, presentation{pretty: true})
	got := output.String()
	for _, want := range []string{"LSP hover", "/work/main.go", "first\\x1B[31m", "**literal**", "\\x1B[2J", "code (go)", "second: unsupported"} {
		if !strings.Contains(got, want) {
			t.Fatalf("pretty hover missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b[31m") || strings.Contains(got, "\x1b[2J") {
		t.Fatalf("upstream terminal control survived: %q", got)
	}
}

func stripANSI(value string) string {
	var output strings.Builder
	for i := 0; i < len(value); {
		if value[i] == '\x1b' && i+1 < len(value) && value[i+1] == '[' {
			i += 2
			for i < len(value) && (value[i] < '@' || value[i] > '~') {
				i++
			}
			if i < len(value) {
				i++
			}
			continue
		}
		output.WriteByte(value[i])
		i++
	}
	return output.String()
}

func TestFocusedHelpEscapesTerminalControls(t *testing.T) {
	output := renderToolHelp("ser\x1b[2Jver", toolDescription{Name: "to\r\nol", Title: "ti\u202etle", Description: "line one\nline two\tpart\x1b[2J", InputSchema: []byte(`{"type":"object","properties":{"bad\u001bname":{"type":"string\u001b","default":"\u009b\u202e","enum":["\u009b"]}}}`)})
	for _, want := range []string{`ser\x1B[2Jver`, `to\x0D\x0Aol`, `ti\u202Etle`, "line one\nline two\tpart\\x1B[2J", `string\x1B`, `\u009b`, `\u202e`} {
		if !strings.Contains(output, want) {
			t.Fatalf("help missing %q:\n%s", want, output)
		}
	}
	if strings.Contains(output, "\x1b") || strings.Contains(output, "\u009b") || strings.Contains(output, "\u202e") {
		t.Fatalf("help exposed terminal control: %q", output)
	}
}

func TestFocusedHelpExactFallbackRetainsRawToolIdentity(t *testing.T) {
	name := "to\x1b'ol"
	tool := toolDescription{Name: name, InputSchema: []byte(`{"type":"object"}`)}
	output := renderToolHelp("server", tool)
	envelope := `{"tool":` + compactJSON(name) + `,"arguments":{}}`
	if !strings.Contains(output, shellQuote(envelope)) {
		t.Fatalf("exact fallback lost its safe raw envelope:\n%s", output)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(envelope), &decoded); err != nil || decoded["tool"] != name {
		t.Fatalf("exact fallback identity = %#v, %v", decoded, err)
	}
}
