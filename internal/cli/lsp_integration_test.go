package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"github.com/Kaylebor/wirecmd/internal/config"
	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func TestLSPDefinitionDirectAndDaemon(t *testing.T) {
	t.Setenv("WIRECMD_CLI_LSP_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(input, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace, "wirecmd.kdl")
	writeSource(t, configPath, fmt.Sprintf("wirecmd {\nroot %s\nlsp \"fixture\" {\nscope \"workspace\"\nlanguage-id \"fixture\"\nstdio %s {\narg \"-test.run=TestCLILSPHelperProcess\"\n}\n}\n}\n", strconv.Quote(workspace), strconv.Quote(os.Args[0])))
	args := []string{"--config", configPath, "lsp", "definition", "--file", input, "--line", "1", "--column", "2"}
	invalidArgs := []string{"--config", configPath, "lsp", "definition", "--file", filepath.Join(workspace, "missing.go"), "--line", "1", "--column", "1"}
	badExecutableConfig := filepath.Join(workspace, "bad-executable.kdl")
	writeSource(t, badExecutableConfig, fmt.Sprintf("wirecmd {\nroot %s\nlsp \"fixture\" {\nscope \"workspace\"\nlanguage-id \"fixture\"\nstdio \"/wirecmd/does-not-exist\"\n}\n}\n", strconv.Quote(workspace)))
	directInvalid := append([]string{"--direct", "--config", badExecutableConfig}, invalidArgs[2:]...)
	code, output, stderr := invoke(t, directInvalid)
	if code != exitInvocation || stderr != "" || decodeOutput(t, output)["error"].(map[string]any)["code"] != "lsp_position_invalid" {
		t.Fatalf("direct invalid input started executable resolution: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	code, output, stderr = invoke(t, invalidArgs)
	if code != exitInvocation || stderr != "" || decodeOutput(t, output)["error"].(map[string]any)["code"] != "lsp_position_invalid" {
		t.Fatalf("invalid input: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	code, output, stderr = invoke(t, []string{"daemon", "status"})
	if code != exitOK || decodeOutput(t, output)["daemon"].(map[string]any)["active_instances"].(json.Number).String() != "0" {
		t.Fatalf("invalid input started an LSP instance: code=%d stdout=%s stderr=%q", code, output, stderr)
	}

	directCode, directOutput, directStderr := invoke(t, append([]string{"--direct"}, args...))
	if directCode != exitOK || directStderr != "" {
		t.Fatalf("direct: code=%d stdout=%s stderr=%q", directCode, directOutput, directStderr)
	}
	code, output, stderr = invoke(t, args)
	if code != exitOK || stderr != "" || !reflect.DeepEqual(decodeOutput(t, output), decodeOutput(t, directOutput)) {
		t.Fatalf("daemon: code=%d stdout=%s stderr=%q; direct=%s", code, output, stderr, directOutput)
	}
	lsp := decodeOutput(t, output)["lsp"].(map[string]any)
	locations := lsp["locations"].([]any)
	if len(locations) != 1 || locations[0].(map[string]any)["path"] != input+".definition" {
		t.Fatalf("locations = %#v", locations)
	}
	code, output, stderr = invoke(t, args)
	if code != exitOK || stderr != "" {
		t.Fatalf("retained call: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	code, output, stderr = invoke(t, []string{"daemon", "status"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["daemon"].(map[string]any)["active_instances"].(json.Number).String() != "1" {
		t.Fatalf("status: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	code, output, stderr = invoke(t, []string{"daemon", "reload"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["reload"].(map[string]any)["instances_retired"].(json.Number).String() != "1" {
		t.Fatalf("reload: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	code, output, stderr = invoke(t, args)
	if code != exitOK || stderr != "" {
		t.Fatalf("post-reload call: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
}

func TestLSPDefinitionUsesTrustedWorkspaceComposition(t *testing.T) {
	t.Setenv("WIRECMD_CLI_LSP_HELPER", "1")
	configHome, _ := discoveryEnvironment(t)
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(input, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	global := filepath.Join(configHome, "wirecmd", "config.kdl")
	if err := os.MkdirAll(filepath.Dir(global), 0o700); err != nil {
		t.Fatal(err)
	}
	writeSource(t, global, "wirecmd {\nlsp \"fixture\" {\nscope \"workspace\"\nlanguage-id \"fixture\"\n}\n}\n")
	writeSource(t, filepath.Join(workspace, "wirecmd.kdl"), fmt.Sprintf("wirecmd {\nroot \".\"\nlsp \"fixture\" {\nstdio %s {\narg \"-test.run=TestCLILSPHelperProcess\"\n}\n}\n}\n", strconv.Quote(os.Args[0])))
	if code, output, _ := invoke(t, []string{"config", "trust", workspace}); code != exitOK {
		t.Fatalf("trust: code=%d output=%s", code, output)
	}
	t.Chdir(workspace)
	code, output, stderr := invoke(t, []string{"--direct", "lsp", "definition", "--file", "input.go", "--line", "1", "--column", "2"})
	if code != exitOK || stderr != "" {
		t.Fatalf("discovered definition: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	if decodeOutput(t, output)["lsp"].(map[string]any)["file"] != input {
		t.Fatalf("output = %s", output)
	}
}

func TestLSPFingerprintAndSelectedSecrets(t *testing.T) {
	source, err := config.ParseString("lsp.kdl", `wirecmd {
        lsp "fixture" {
            scope "workspace"
            language-id "fixture"
            stdio "fixture-lsp" {
                arg (secret)"env://ARG_SECRET"
                env TOKEN=(secret)"env://TOKEN_SECRET"
                env PUBLIC="literal"
            }
        }
    }`)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Compose(source)
	if err != nil {
		t.Fatal(err)
	}
	inputs := selectedLSPSecretInputs(cfg.LSPs[0], func(name string) (string, bool) { return name + "-value", true })
	if len(inputs) != 2 || inputs["ARG_SECRET"].Value != "ARG_SECRET-value" || inputs["TOKEN_SECRET"].Value != "TOKEN_SECRET-value" {
		t.Fatalf("inputs = %#v", inputs)
	}
	first := lspExecutionFingerprint(cfg.LSPs[0], nil, "/workspace")
	changed := cfg.LSPs[0]
	changed.LanguageID = "changed"
	if second := lspExecutionFingerprint(changed, nil, "/workspace"); second == first {
		t.Fatal("language ID did not affect LSP execution fingerprint")
	}
}

func TestDaemonKeepsBrokenLSPUnavailable(t *testing.T) {
	t.Setenv("WIRECMD_CLI_LSP_HELPER", "exit-on-definition")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(input, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace, "config.kdl")
	writeSource(t, configPath, fmt.Sprintf("wirecmd {\nroot %s\nlsp \"fixture\" {\nscope \"workspace\"\nlanguage-id \"fixture\"\nstdio %s { arg \"-test.run=TestCLILSPHelperProcess\" }\n}\n}\n", strconv.Quote(workspace), strconv.Quote(os.Args[0])))
	args := []string{"--config", configPath, "lsp", "definition", "--file", input, "--line", "1", "--column", "2"}
	for attempt := 0; attempt < 2; attempt++ {
		code, output, _ := invoke(t, args)
		if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "lsp_instance_unavailable" {
			t.Fatalf("attempt %d: code=%d output=%s", attempt, code, output)
		}
	}
}

func TestCLILSPHelperProcess(t *testing.T) {
	if os.Getenv("WIRECMD_CLI_LSP_HELPER") == "" {
		return
	}
	stream := jsonrpc2.NewStream(&cliLSPStdio{Reader: os.Stdin, Writer: os.Stdout})
	_, connection, _ := protocol.NewServer(context.Background(), cliLSPServer{UnimplementedServer: protocol.UnimplementedServer{}, mode: os.Getenv("WIRECMD_CLI_LSP_HELPER")}, stream)
	<-connection.Done()
}

type cliLSPStdio struct {
	io.Reader
	io.Writer
}

func (*cliLSPStdio) Close() error { return nil }

type cliLSPServer struct {
	protocol.UnimplementedServer
	mode string
}

func (cliLSPServer) Initialize(context.Context, *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	kind, open := protocol.TextDocumentSyncKindIncremental, true
	return &protocol.InitializeResult{Capabilities: protocol.ServerCapabilities{
		DefinitionProvider: protocol.Boolean(true),
		TextDocumentSync:   &protocol.TextDocumentSyncOptions{OpenClose: &open, Change: &kind},
	}}, nil
}

func (cliLSPServer) Initialized(context.Context, *protocol.InitializedParams) error { return nil }
func (cliLSPServer) Shutdown(context.Context) error                                 { return nil }
func (cliLSPServer) Exit(context.Context) error                                     { return nil }
func (cliLSPServer) DidOpen(context.Context, *protocol.DidOpenTextDocumentParams) error {
	return nil
}
func (cliLSPServer) DidChange(context.Context, *protocol.DidChangeTextDocumentParams) error {
	return nil
}
func (cliLSPServer) DidClose(context.Context, *protocol.DidCloseTextDocumentParams) error {
	return nil
}

func (server cliLSPServer) Definition(_ context.Context, params *protocol.DefinitionParams) (protocol.DefinitionResult, error) {
	if server.mode == "exit-on-definition" {
		os.Exit(0)
	}
	target := uri.File(params.TextDocument.URI.FsPath() + ".definition")
	return protocol.LocationSlice{{URI: target, Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 3}, End: protocol.Position{Line: 2, Character: 5}}}}, nil
}
