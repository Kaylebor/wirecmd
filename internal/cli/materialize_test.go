package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Kaylebor/wirecmd/internal/config"
)

func templateValue(text string) config.Value {
	return config.Value{Kind: config.ValueTemplate, Text: text}
}

func TestMaterializeServerExpandsEveryProviderInput(t *testing.T) {
	secret := templateValue("client-${wirecmd.cwd}")
	server := config.Server{
		Name:  "remote",
		Scope: config.ScopeGlobal,
		HTTP: &config.HTTP{
			Endpoint: templateValue("https://example.test${wirecmd.project-root}"),
			Query:    []config.HTTPField{{Name: "cwd", Value: templateValue("${wirecmd.cwd}")}},
			Headers:  []config.HTTPField{{Name: "X-Global", Value: templateValue("${wirecmd.global-root}")}},
			OAuth: &config.OAuth{
				ClientID:     templateValue("id-${wirecmd.project-root}"),
				ClientSecret: &secret,
				RedirectURI:  templateValue("http://127.0.0.1:8765/${wirecmd.cwd}"),
			},
		},
	}
	context := invocationContext{configContext: configContext{CWD: "/work/repo/sub"}, ProjectRoot: "/work/repo", GlobalRoot: "/home/user/.config/wirecmd"}

	got, appErr := materializeServer(server, context)
	if appErr != nil {
		t.Fatal(appErr)
	}
	assertLiteral := func(name string, value config.Value, want string) {
		t.Helper()
		if value.Kind != config.ValueLiteral || value.Text != want {
			t.Fatalf("%s = %#v, want literal %q", name, value, want)
		}
	}
	assertLiteral("endpoint", got.HTTP.Endpoint, "https://example.test/work/repo")
	assertLiteral("query", got.HTTP.Query[0].Value, "/work/repo/sub")
	assertLiteral("header", got.HTTP.Headers[0].Value, "/home/user/.config/wirecmd")
	assertLiteral("client ID", got.HTTP.OAuth.ClientID, "id-/work/repo")
	assertLiteral("client secret", *got.HTTP.OAuth.ClientSecret, "client-/work/repo/sub")
	assertLiteral("redirect URI", got.HTTP.OAuth.RedirectURI, "http://127.0.0.1:8765//work/repo/sub")
	target, _, targetErr := makeHTTPTarget(*got.HTTP, nil)
	if targetErr != nil {
		t.Fatal(targetErr)
	}
	if target.endpoint != "https://example.test/work/repo?cwd=%2Fwork%2Frepo%2Fsub" {
		t.Fatalf("materialized HTTP endpoint = %q", target.endpoint)
	}
	transport := target.httpClient.Transport.(*configuredHeaderTransport)
	if transport.headers.Get("X-Global") != "/home/user/.config/wirecmd" {
		t.Fatalf("materialized HTTP headers = %#v", transport.headers)
	}
	sse := *got.HTTP
	sse.Kind = config.HTTPTransportSSE
	sse.OAuth = nil
	sseTarget, _, targetErr := makeHTTPTarget(sse, nil)
	if targetErr != nil || !sseTarget.sse {
		t.Fatalf("materialized SSE target = %#v, %v", sseTarget, targetErr)
	}
	identity, err := oauthCredentialIdentity(*got.HTTP, target.endpoint, got.HTTP.OAuth.ClientSecret.Text)
	if err != nil || len(identity) == 0 {
		t.Fatalf("materialized OAuth identity = %x, %v", identity, err)
	}

	if server.HTTP.Endpoint.Kind != config.ValueTemplate || server.HTTP.Query[0].Value.Kind != config.ValueTemplate || server.HTTP.OAuth.ClientSecret.Kind != config.ValueTemplate {
		t.Fatal("materialization mutated cached source configuration")
	}
}

func TestMaterializeStdioAndLSP(t *testing.T) {
	stdio := config.Stdio{
		Command: templateValue("${wirecmd.global-root}/bin/server"),
		Args:    []config.Value{templateValue("--root=${wirecmd.project-root}"), literalValue("literal")},
		Env:     []config.Environment{{Name: "CALLER", Value: templateValue("${wirecmd.cwd}")}},
	}
	context := invocationContext{configContext: configContext{CWD: "/work/repo/sub"}, ProjectRoot: "/work/repo", GlobalRoot: "/config/wirecmd"}

	got, appErr := materializeLSP(config.LSP{Name: "language", Stdio: stdio}, context)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if got.Stdio.Command.Text != "/config/wirecmd/bin/server" || got.Stdio.Args[0].Text != "--root=/work/repo" || got.Stdio.Args[1].Text != "literal" || got.Stdio.Env[0].Value.Text != "/work/repo/sub" {
		t.Fatalf("materialized LSP = %#v", got.Stdio)
	}
}

func TestMaterializationIdentityUsesResolvedValues(t *testing.T) {
	definition := config.LSP{Name: "language", Scope: config.ScopeGlobal, Stdio: config.Stdio{Command: literalValue("lsp"), Args: []config.Value{templateValue("${wirecmd.cwd}")}}}
	first, appErr := materializeLSP(definition, invocationContext{configContext: configContext{CWD: "/one"}, ProjectRoot: "/project", GlobalRoot: "/global"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	second, appErr := materializeLSP(definition, invocationContext{configContext: configContext{CWD: "/two"}, ProjectRoot: "/project", GlobalRoot: "/global"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if semanticLSP(definition).(map[string]any)["command"] == nil {
		t.Fatal("static semantic configuration omitted command source")
	}
	if lspExecutionFingerprint(first, nil, "/global") == lspExecutionFingerprint(second, nil, "/global") {
		t.Fatal("different materialized CWD values produced the same execution identity")
	}
	templateConfig := &config.Config{Servers: []config.Server{{Name: "server", Stdio: config.Stdio{Command: templateValue("${wirecmd.cwd}")}}}}
	literalConfig := &config.Config{Servers: []config.Server{{Name: "server", Stdio: config.Stdio{Command: literalValue("${wirecmd.cwd}")}}}}
	if configFingerprint(templateConfig) == configFingerprint(literalConfig) {
		t.Fatal("static configuration identity discarded the template source kind")
	}

	withoutContext := config.LSP{Name: "plain", Scope: config.ScopeGlobal, Stdio: config.Stdio{Command: literalValue("lsp")}}
	plainFirst, appErr := materializeLSP(withoutContext, invocationContext{configContext: configContext{CWD: "/project/one"}, ProjectRoot: "/project", GlobalRoot: "/global"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	plainSecond, appErr := materializeLSP(withoutContext, invocationContext{configContext: configContext{CWD: "/other/two"}, ProjectRoot: "/other", GlobalRoot: "/global"})
	if appErr != nil {
		t.Fatal(appErr)
	}
	if lspExecutionFingerprint(plainFirst, nil, "/global") != lspExecutionFingerprint(plainSecond, nil, "/global") {
		t.Fatal("context-independent global execution identity split across projects")
	}
}

func TestMaterializeUnavailableAndInvalidDestination(t *testing.T) {
	_, appErr := materializeStdio(config.Stdio{Command: templateValue("${wirecmd.global-root}/server")}, invocationContext{})
	if appErr == nil || appErr.code != "context_value_unavailable" {
		t.Fatalf("unavailable context error = %#v", appErr)
	}
	_, appErr = materializeServer(config.Server{HTTP: &config.HTTP{Endpoint: templateValue("file://${wirecmd.project-root}")}}, invocationContext{ProjectRoot: "/work"})
	if appErr == nil || appErr.code != "invalid_http_endpoint" {
		t.Fatalf("invalid materialized endpoint error = %#v", appErr)
	}
}

func TestDirectLSPStatusReportsMaterializedExecutableWithoutStartingIt(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd = canonicalPath(cwd)
	configPath := filepath.Join(t.TempDir(), "config.kdl")
	writeSource(t, configPath, `wirecmd {
        lsp "language" {
            selector language-id="go"
            stdio (template)"${wirecmd.cwd}/definitely-not-started"
        }
    }`)
	code, output, stderr := invoke(t, []string{"--direct", "--config", configPath, "lsp", "status"})
	if code != exitOK || stderr != "" {
		t.Fatalf("status: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	providers := decodeOutput(t, output)["lsp"].(map[string]any)["providers"].([]any)
	if got, want := providers[0].(map[string]any)["executable"], filepath.Join(cwd, "definitely-not-started"); got != want {
		t.Fatalf("executable = %s, want %s", strconv.Quote(got.(string)), strconv.Quote(want))
	}
}

func TestContextTemplatesReachStdioChildInDirectAndDaemonModes(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cwd = canonicalPath(cwd)
	workspace := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	configPath := filepath.Join(workspace, "config.kdl")
	writeSource(t, configPath, `wirecmd {
        root `+strconv.Quote(workspace)+`
        mcp "helper" {
            stdio `+strconv.Quote(os.Args[0])+` {
                arg "-test.run=TestHelperProcess"
                arg "--"
                env CALLER=(template)"${wirecmd.cwd}"
                env PROJECT=(template)"${wirecmd.project-root}"
                env GLOBAL=(template)"${wirecmd.global-root}"
            }
        }
    }`)
	expected := map[string]string{"CALLER": cwd, "PROJECT": canonicalPath(workspace), "GLOBAL": canonicalPath(filepath.Join(xdg, "wirecmd"))}
	payload, err := json.Marshal(expected)
	if err != nil {
		t.Fatal(err)
	}
	args := []string{"--config", configPath, "--json", string(payload), "helper", "environment_matches"}
	for _, prefix := range [][]string{{"--direct"}, nil} {
		if prefix == nil {
			t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
			startTestDaemon(t)
		}
		code, output, stderr := invoke(t, append(prefix, args...))
		if code != exitOK || stderr != "" {
			t.Fatalf("%v: code=%d stdout=%s stderr=%q", prefix, code, output, stderr)
		}
		data := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
		if matched, _ := data["matched"].(bool); !matched {
			t.Fatalf("%v: child environment did not match materialized context", prefix)
		}
	}
}

func TestMCPListingDoesNotResolveContextTemplates(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "relative")
	configPath := writeConfig(t, `wirecmd {
        mcp "static" {
            stdio (template)"${wirecmd.global-root}/never-started"
        }
    }`)
	code, output, stderr := invokeRaw(t, []string{"--direct", "--config", configPath, "mcp"})
	if code != exitOK || stderr != "" || !strings.Contains(output, `"name":"static"`) || !strings.Contains(output, `"transport":"stdio"`) {
		t.Fatalf("listing: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
}

func TestGlobalTemplateMaterializationControlsRetainedReuse(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("XDG_RUNTIME_DIR", testRuntimeDirectory(t))
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.Mkdir(filepath.Join(xdg, "wirecmd"), 0o700); err != nil {
		t.Fatal(err)
	}
	children := filepath.Join(t.TempDir(), "children")
	t.Setenv("WIRECMD_CHILD_COUNT_FILE", children)
	startTestDaemon(t)

	writeProvider := func(root string, templated bool) string {
		t.Helper()
		env := ""
		if templated {
			env = `env PROJECT=(template)"${wirecmd.project-root}"`
		}
		return writeConfig(t, `wirecmd {
            root `+strconv.Quote(root)+`
            mcp "helper" {
                scope "global"
                stdio `+strconv.Quote(os.Args[0])+` {
                    arg "-test.run=TestHelperProcess"
                    arg "--"
                    `+env+`
                }
            }
		}`)
	}
	call := func(path string) {
		t.Helper()
		code, output, stderr := invoke(t, []string{"--config", path, "helper"})
		if code != exitOK || stderr != "" {
			t.Fatalf("call: code=%d stdout=%s stderr=%q", code, output, stderr)
		}
	}

	call(writeProvider("/project/one", false))
	call(writeProvider("/project/two", false))
	if got := childProcessCount(t, children); got != 1 {
		t.Fatalf("context-independent global child count = %d, want 1", got)
	}
	if code, output, stderr := invoke(t, []string{"daemon", "reload"}); code != exitOK || stderr != "" {
		t.Fatalf("reload: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	call(writeProvider("/project/one", true))
	call(writeProvider("/project/two", true))
	if got := childProcessCount(t, children); got != 3 {
		t.Fatalf("project-dependent global child count = %d, want 3 total", got)
	}
}

func childProcessCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}
