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
	"sync/atomic"
	"testing"
	"time"

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
	writeSource(t, configPath, fmt.Sprintf("wirecmd {\nroot %s\nlsp \"fixture\" {\nselector language-id=\"fixture\"\nstdio %s {\narg \"-test.run=TestCLILSPHelperProcess\"\n}\n}\n}\n", strconv.Quote(workspace), strconv.Quote(os.Args[0])))
	args := []string{"--config", configPath, "lsp", "definition", "--file", input, "--line", "1", "--column", "2"}
	invalidArgs := []string{"--config", configPath, "lsp", "definition", "--file", filepath.Join(workspace, "missing.go"), "--line", "1", "--column", "1"}
	badExecutableConfig := filepath.Join(workspace, "bad-executable.kdl")
	writeSource(t, badExecutableConfig, fmt.Sprintf("wirecmd {\nroot %s\nlsp \"fixture\" {\nselector language-id=\"fixture\"\nstdio \"/wirecmd/does-not-exist\"\n}\n}\n", strconv.Quote(workspace)))
	code, output, stderr := invoke(t, []string{"--direct", "--config", badExecutableConfig, "lsp", "status", "--file", input})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["lsp"].(map[string]any)["providers"].([]any)[0].(map[string]any)["runtime"].(map[string]any)["status"] != "not_checked" {
		t.Fatalf("direct status started or resolved the executable: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	directInvalid := append([]string{"--direct", "--config", badExecutableConfig}, invalidArgs[2:]...)
	code, output, stderr = invoke(t, directInvalid)
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
	writeSource(t, global, "wirecmd {\nlsp \"fixture\" {\nselector language-id=\"fixture\"\n}\n}\n")
	writeSource(t, filepath.Join(workspace, "wirecmd.kdl"), fmt.Sprintf("wirecmd {\nroot \".\"\nlsp \"fixture\" {\nstdio %s {\narg \"-test.run=TestCLILSPHelperProcess\"\n}\n}\n}\n", strconv.Quote(os.Args[0])))
	alias := filepath.Join(t.TempDir(), "workspace-alias")
	if err := os.Symlink(workspace, alias); err != nil {
		t.Fatal(err)
	}
	if code, output, _ := invoke(t, []string{"config", "trust", alias}); code != exitOK {
		t.Fatalf("trust: code=%d output=%s", code, output)
	}
	t.Setenv("PWD", alias)
	t.Chdir(alias)
	canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	code, output, stderr := invoke(t, []string{"--direct", "lsp", "definition", "--file", "input.go", "--line", "1", "--column", "2"})
	if code != exitOK || stderr != "" {
		t.Fatalf("discovered definition: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	if decodeOutput(t, output)["lsp"].(map[string]any)["file"] != filepath.Join(canonicalWorkspace, "input.go") {
		t.Fatalf("output = %s", output)
	}
}

func TestLSPFingerprintAndSelectedSecrets(t *testing.T) {
	source, err := config.ParseString("lsp.kdl", `wirecmd {
        lsp "fixture" {
            scope "workspace"
            selector language-id="fixture"
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
	changed.Selectors[0].LanguageID = "changed"
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
	writeSource(t, configPath, fmt.Sprintf("wirecmd {\nroot %s\nlsp \"fixture\" {\nselector language-id=\"fixture\"\nstdio %s { arg \"-test.run=TestCLILSPHelperProcess\" }\n}\n}\n", strconv.Quote(workspace), strconv.Quote(os.Args[0])))
	args := []string{"--config", configPath, "lsp", "definition", "--file", input, "--line", "1", "--column", "2"}
	for attempt := 0; attempt < 2; attempt++ {
		code, output, _ := invoke(t, args)
		if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "lsp_instance_unavailable" {
			t.Fatalf("attempt %d: code=%d output=%s", attempt, code, output)
		}
	}
}

func TestLSPMultiProviderPartialAndStatus(t *testing.T) {
	t.Setenv("WIRECMD_CLI_LSP_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.ts")
	if err := os.WriteFile(input, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace, "config.kdl")
	writeSource(t, configPath, fmt.Sprintf(`wirecmd {
root %s
lsp "angular" {
  implementation-id "angular.test"
  selector language-id="typescript" pattern="**/*.ts"
  stdio %s { arg "-test.run=TestCLILSPHelperProcess"; env WIRECMD_LSP_PROVIDER=".angular" }
}
lsp "typescript" {
  selector language-id="typescript" pattern="**/*"
  stdio %s { arg "-test.run=TestCLILSPHelperProcess"; env WIRECMD_LSP_PROVIDER=".typescript"; env WIRECMD_LSP_FAIL="1" }
}
}
`, strconv.Quote(workspace), strconv.Quote(os.Args[0]), strconv.Quote(os.Args[0])))
	args := []string{"--config", configPath, "lsp", "definition", "--file", input, "--line", "1", "--column", "2"}
	for _, prefix := range [][]string{{"--direct"}, {}} {
		code, output, stderr := invoke(t, append(prefix, args...))
		if code != exitOK || stderr != "" {
			t.Fatalf("%v: code=%d stdout=%s stderr=%q", prefix, code, output, stderr)
		}
		lsp := decodeOutput(t, output)["lsp"].(map[string]any)
		locations := lsp["locations"].([]any)
		providers := lsp["providers"].([]any)
		if lsp["partial"] != true || len(locations) != 1 || locations[0].(map[string]any)["provider"] != "angular" || len(providers) != 2 || providers[1].(map[string]any)["status"] != "failed" {
			t.Fatalf("multi-provider output = %#v", lsp)
		}
	}
	code, output, stderr := invoke(t, []string{"--config", configPath, "lsp", "status", "--file", input})
	if code != exitOK || stderr != "" {
		t.Fatalf("status: code=%d stdout=%s stderr=%q", code, output, stderr)
	}
	providers := decodeOutput(t, output)["lsp"].(map[string]any)["providers"].([]any)
	if len(providers) != 2 || providers[0].(map[string]any)["runtime"].(map[string]any)["status"] != "connected" || providers[1].(map[string]any)["runtime"].(map[string]any)["status"] != "connected" {
		t.Fatalf("status providers = %#v", providers)
	}
}

func TestLSPNavigationOperations(t *testing.T) {
	t.Setenv("WIRECMD_CLI_LSP_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(input, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace, "config.kdl")
	writeSource(t, configPath, fmt.Sprintf("wirecmd { root %s; lsp \"fixture\" { selector language-id=\"fixture\"; stdio %s { arg \"-test.run=TestCLILSPHelperProcess\" } } }", strconv.Quote(workspace), strconv.Quote(os.Args[0])))
	for _, test := range []struct {
		operation string
		extra     []string
		suffix    string
	}{
		{lspDeclaration, nil, ".declaration"},
		{lspTypeDefinition, nil, ".type-definition"},
		{lspImplementation, nil, ".implementation"},
		{lspReferences, nil, ".references-false"},
		{lspReferences, []string{"--include-declaration"}, ".references-true"},
	} {
		base := []string{"--config", configPath, "lsp", test.operation, "--file", input, "--line", "1", "--column", "2"}
		base = append(base, test.extra...)
		var direct any
		for _, prefix := range [][]string{{"--direct"}, {}} {
			code, output, stderr := invoke(t, append(prefix, base...))
			if code != exitOK || stderr != "" {
				t.Fatalf("%s %v: code=%d stdout=%s stderr=%q", test.operation, prefix, code, output, stderr)
			}
			decoded := decodeOutput(t, output)
			if direct == nil {
				direct = decoded
			} else if !reflect.DeepEqual(direct, decoded) {
				t.Fatalf("%s daemon output differs: direct=%#v daemon=%#v", test.operation, direct, decoded)
			}
			location := decoded["lsp"].(map[string]any)["locations"].([]any)[0].(map[string]any)
			if location["path"] != input+test.suffix {
				t.Fatalf("%s location = %#v", test.operation, location)
			}
		}
	}
}

func TestLSPProviderFailureContracts(t *testing.T) {
	t.Setenv("WIRECMD_CLI_LSP_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(input, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name string
		env  string
		code string
	}{
		{"unsupported", "WIRECMD_LSP_UNSUPPORTED", "lsp_capability_unavailable"},
		{"failed", "WIRECMD_LSP_FAIL", "lsp_definition_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			configPath := filepath.Join(workspace, test.name+".kdl")
			writeSource(t, configPath, fmt.Sprintf(`wirecmd {
root %s
lsp "first" { selector language-id="fixture"; stdio %s { arg "-test.run=TestCLILSPHelperProcess"; env %s="1" } }
lsp "second" { selector language-id="fixture"; stdio %s { arg "-test.run=TestCLILSPHelperProcess"; env %s="1" } }
}

`, strconv.Quote(workspace), strconv.Quote(os.Args[0]), test.env, strconv.Quote(os.Args[0]), test.env))
			for _, prefix := range [][]string{{"--direct"}, {}} {
				args := []string{"--config", configPath, "lsp", "definition", "--file", input, "--line", "1", "--column", "2"}
				code, output, stderr := invoke(t, append(prefix, args...))
				if code != exitProtocol || stderr != "" {
					t.Fatalf("%v: code=%d stdout=%s stderr=%q", prefix, code, output, stderr)
				}
				errorValue := decodeOutput(t, output)["error"].(map[string]any)
				providers := errorValue["details"].(map[string]any)["providers"].([]any)
				if errorValue["code"] != test.code || len(providers) != 2 || providers[0].(map[string]any)["name"] != "first" || providers[1].(map[string]any)["name"] != "second" || providers[0].(map[string]any)["status"] != test.name {
					t.Fatalf("%v output=%#v", prefix, errorValue)
				}
			}
		})
	}
}

func TestLSPProvidersRunConcurrently(t *testing.T) {
	t.Setenv("WIRECMD_CLI_LSP_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(input, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range [][]string{{"--direct"}, {}} {
		barrier := t.TempDir()
		configPath := filepath.Join(workspace, fmt.Sprintf("concurrent-%d.kdl", len(prefix)))
		writeSource(t, configPath, fmt.Sprintf(`wirecmd {
root %s
lsp "first" { selector language-id="fixture"; stdio %s { arg "-test.run=TestCLILSPHelperProcess"; env WIRECMD_LSP_PROVIDER="first"; env WIRECMD_LSP_BARRIER=%s } }
lsp "second" { selector language-id="fixture"; stdio %s { arg "-test.run=TestCLILSPHelperProcess"; env WIRECMD_LSP_PROVIDER="second"; env WIRECMD_LSP_BARRIER=%s } }
}

`, strconv.Quote(workspace), strconv.Quote(os.Args[0]), strconv.Quote(barrier), strconv.Quote(os.Args[0]), strconv.Quote(barrier)))
		args := []string{"--config", configPath, "lsp", "definition", "--file", input, "--line", "1", "--column", "2"}
		code, output, stderr := invoke(t, append(prefix, args...))
		if code != exitOK || stderr != "" {
			t.Fatalf("%v: code=%d stdout=%s stderr=%q", prefix, code, output, stderr)
		}
		providers := decodeOutput(t, output)["lsp"].(map[string]any)["providers"].([]any)
		if providers[0].(map[string]any)["status"] != "ok" || providers[1].(map[string]any)["status"] != "ok" {
			t.Fatalf("%v providers=%#v", prefix, providers)
		}
	}
}

func TestRetainedLSPProviderSerializesOperations(t *testing.T) {
	t.Setenv("WIRECMD_CLI_LSP_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	workspace := t.TempDir()
	input := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(input, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace, "serialized.kdl")
	writeSource(t, configPath, fmt.Sprintf(`wirecmd {
root %s
lsp "only" { selector language-id="fixture"; stdio %s { arg "-test.run=TestCLILSPHelperProcess"; env WIRECMD_LSP_DELAY_MS="100" } }
}
`, strconv.Quote(workspace), strconv.Quote(os.Args[0])))
	args := []string{"--config", configPath, "lsp", "definition", "--file", input, "--line", "1", "--column", "2"}
	type result struct {
		code           int
		output, stderr string
	}
	results := make(chan result, 2)
	for range 2 {
		go func() {
			code, output, stderr := invoke(t, args)
			results <- result{code: code, output: output, stderr: stderr}
		}()
	}
	for range 2 {
		result := <-results
		if result.code != exitOK || result.stderr != "" {
			t.Fatalf("concurrent retained call: code=%d stdout=%s stderr=%q", result.code, result.output, result.stderr)
		}
	}
}

func TestCLILSPHelperProcess(t *testing.T) {
	if os.Getenv("WIRECMD_CLI_LSP_HELPER") == "" {
		return
	}
	stream := jsonrpc2.NewStream(&cliLSPStdio{Reader: os.Stdin, Writer: os.Stdout})
	delay, _ := strconv.Atoi(os.Getenv("WIRECMD_LSP_DELAY_MS"))
	_, connection, _ := protocol.NewServer(context.Background(), cliLSPServer{UnimplementedServer: protocol.UnimplementedServer{}, mode: os.Getenv("WIRECMD_CLI_LSP_HELPER"), provider: os.Getenv("WIRECMD_LSP_PROVIDER"), fail: os.Getenv("WIRECMD_LSP_FAIL") == "1", unsupported: os.Getenv("WIRECMD_LSP_UNSUPPORTED") == "1", delay: time.Duration(delay) * time.Millisecond, active: &atomic.Int32{}}, stream)
	<-connection.Done()
}

type cliLSPStdio struct {
	io.Reader
	io.Writer
}

func (*cliLSPStdio) Close() error { return nil }

type cliLSPServer struct {
	protocol.UnimplementedServer
	mode        string
	provider    string
	fail        bool
	unsupported bool
	delay       time.Duration
	active      *atomic.Int32
}

func (server cliLSPServer) Initialize(context.Context, *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	kind, open := protocol.TextDocumentSyncKindIncremental, true
	capabilities := protocol.ServerCapabilities{
		TextDocumentSync: &protocol.TextDocumentSyncOptions{OpenClose: &open, Change: &kind},
	}
	if !server.unsupported {
		capabilities = protocol.ServerCapabilities{
			DeclarationProvider:    protocol.Boolean(true),
			DefinitionProvider:     protocol.Boolean(true),
			TypeDefinitionProvider: protocol.Boolean(true),
			ImplementationProvider: protocol.Boolean(true),
			ReferencesProvider:     protocol.Boolean(true),
			TextDocumentSync:       &protocol.TextDocumentSyncOptions{OpenClose: &open, Change: &kind},
		}
	}
	return &protocol.InitializeResult{Capabilities: capabilities, ServerInfo: protocol.ServerInfo{Name: "wirecmd-test-lsp", Version: protocol.NewOptional("test")}}, nil
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
	if server.fail {
		return nil, fmt.Errorf("provider failed")
	}
	if server.delay > 0 {
		if active := server.active.Add(1); active != 1 {
			server.active.Add(-1)
			return nil, fmt.Errorf("provider operations overlapped")
		}
		defer server.active.Add(-1)
		time.Sleep(server.delay)
	}
	if barrier := os.Getenv("WIRECMD_LSP_BARRIER"); barrier != "" {
		if err := os.WriteFile(filepath.Join(barrier, server.provider), nil, 0o600); err != nil {
			return nil, err
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			entries, err := os.ReadDir(barrier)
			if err != nil {
				return nil, err
			}
			if len(entries) >= 2 {
				break
			}
			if time.Now().After(deadline) {
				return nil, fmt.Errorf("providers did not overlap")
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	target := uri.File(params.TextDocument.URI.FsPath() + ".definition" + server.provider)
	return protocol.LocationSlice{{URI: target, Range: protocol.Range{Start: protocol.Position{Line: 2, Character: 3}, End: protocol.Position{Line: 2, Character: 5}}}}, nil
}

func (server cliLSPServer) Declaration(_ context.Context, params *protocol.DeclarationParams) (protocol.DeclarationResult, error) {
	target := uri.File(params.TextDocument.URI.FsPath() + ".declaration" + server.provider)
	return &protocol.Location{URI: target, Range: protocol.Range{}}, nil
}

func (server cliLSPServer) TypeDefinition(_ context.Context, params *protocol.TypeDefinitionParams) (protocol.DefinitionResult, error) {
	target := uri.File(params.TextDocument.URI.FsPath() + ".type-definition" + server.provider)
	return protocol.LocationSlice{{URI: target, Range: protocol.Range{}}}, nil
}

func (server cliLSPServer) Implementation(_ context.Context, params *protocol.ImplementationParams) (protocol.DefinitionResult, error) {
	target := uri.File(params.TextDocument.URI.FsPath() + ".implementation" + server.provider)
	return protocol.LocationSlice{{URI: target, Range: protocol.Range{}}}, nil
}

func (server cliLSPServer) References(_ context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	suffix := ".references-false"
	if params.Context.IncludeDeclaration {
		suffix = ".references-true"
	}
	return []protocol.Location{{URI: uri.File(params.TextDocument.URI.FsPath() + suffix + server.provider), Range: protocol.Range{}}}, nil
}
