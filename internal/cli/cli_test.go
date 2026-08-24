package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_WIRECMD_HELPER") != "1" {
		return
	}
	if pidFile := os.Getenv("WIRECMD_CHILD_PID_FILE"); pidFile != "" {
		if err := os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
	}
	secret := os.Getenv("SECRET")
	if os.Getenv("WIRECMD_EMIT_SECRET") == "1" && secret != "" {
		cut := len(secret) / 2
		_, _ = fmt.Fprint(os.Stderr, "helper secret: ")
		_, _ = fmt.Fprint(os.Stderr, secret[:cut])
		_, _ = fmt.Fprintln(os.Stderr, secret[cut:])
	}

	server := mcp.NewServer(&mcp.Implementation{Name: "wirecmd-test-server", Version: "dev"}, &mcp.ServerOptions{PageSize: 2})
	mcp.AddTool(server, &mcp.Tool{Name: "z_tool", Title: "Zed", Description: "last tool"}, echoTool)
	mcp.AddTool(server, &mcp.Tool{Name: "a_tool", Title: "Aye", Description: "first tool"}, echoTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:         "projected",
		Title:        "Projected fixture",
		Description:  "exercise focused help and projected arguments",
		InputSchema:  json.RawMessage(`{"type":"object","properties":{"query":{"type":"string","description":"search text"},"limit":{"type":"integer","default":10},"enabled":{"type":"boolean"},"filters":{"type":"object"},"tool_name":{"type":"string"},"toolName":{"type":"string"},"weird.name":{"type":"string"}},"required":["query"]}`),
		OutputSchema: json.RawMessage(`{"type":"object","properties":{"ok":{"type":"boolean"}}}`),
	}, echoTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "schema_secret",
		Description: "schema metadata " + secret,
		InputSchema: map[string]any{"type": "object", "description": "schema " + secret, "properties": map[string]any{"value": map[string]any{"type": "string", "examples": []any{secret}}}},
	}, echoTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "semantic_secret",
		Description: "private schema property " + secret,
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{secret: map[string]any{"type": "string"}}},
	}, semanticSecretTool)
	mcp.AddTool(server, &mcp.Tool{Name: "environment", Description: "read child environment"}, environmentTool)
	mcp.AddTool(server, &mcp.Tool{Name: "working_directory", Description: "read child working directory"}, workingDirectoryTool)
	mcp.AddTool(server, &mcp.Tool{Name: "secret", Description: "return configured secret"}, secretTool)
	mcp.AddTool(server, &mcp.Tool{Name: "secret_keys", Description: "return configured secret keys"}, secretKeysTool)
	mcp.AddTool(server, &mcp.Tool{Name: "failure", Description: "return a tool error"}, failureTool)
	mcp.AddTool(server, &mcp.Tool{Name: "binary", Description: "return unsupported content"}, binaryTool)
	mcp.AddTool(server, &mcp.Tool{Name: "input_required", Description: "request further input"}, inputRequiredTool)
	mcp.AddTool(server, &mcp.Tool{Name: "remember", Description: "retain process-local state"}, rememberTool)
	mcp.AddTool(server, &mcp.Tool{Name: "recall", Description: "read process-local state"}, recallTool)
	mcp.AddTool(server, &mcp.Tool{Name: "block", Description: "wait for cancellation"}, blockTool)
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
}

func echoTool(_ context.Context, request *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	var arguments any
	decoder := json.NewDecoder(bytes.NewReader(request.Params.Arguments))
	decoder.UseNumber()
	if err := decoder.Decode(&arguments); err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "echo"}}}, arguments, nil
}

func semanticSecretTool(_ context.Context, request *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	secret := os.Getenv("SECRET")
	var arguments map[string]any
	if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
		return nil, nil, err
	}
	_, matched := arguments[secret]
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "semantic secret"}}}, map[string]any{"matched": matched}, nil
}

func environmentTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "environment"}}}, map[string]any{
		"inherited": os.Getenv("INHERITED"),
		"empty":     os.Getenv("EMPTY"),
	}, nil
}

func workingDirectoryTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	directory, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "working directory"}}}, map[string]any{"cwd": directory}, nil
}

func secretTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	secret := os.Getenv("SECRET")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "secret: " + secret}}}, map[string]any{"secret": secret}, nil
}

func secretKeysTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	secret := os.Getenv("SECRET")
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "secret keys"}}}, map[string]any{
		secret:       "first",
		"[REDACTED]": "second",
	}, nil
}

func failureTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "tool failure"}}}, nil, nil
}

func binaryTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.ImageContent{MIMEType: "image/png", Data: []byte("not an image")}}}, nil, nil
}

func inputRequiredTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{"question": &mcp.ElicitParams{Message: "continue"}}}, nil, nil
}

var helperMemory struct {
	sync.Mutex
	value string
}

func rememberTool(_ context.Context, request *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	var arguments map[string]string
	if err := json.Unmarshal(request.Params.Arguments, &arguments); err != nil {
		return nil, nil, err
	}
	helperMemory.Lock()
	helperMemory.value = arguments["value"]
	helperMemory.Unlock()
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "remembered"}}}, map[string]any{"value": arguments["value"]}, nil
}

func recallTool(_ context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	helperMemory.Lock()
	value := helperMemory.value
	helperMemory.Unlock()
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "recalled"}}}, map[string]any{"value": value}, nil
}

func blockTool(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
	<-ctx.Done()
	return nil, nil, ctx.Err()
}

func TestConnectFailureProcess(t *testing.T) {
	if os.Getenv("GO_WIRECMD_CONNECT_FAILURE") != "1" {
		return
	}
	if err := os.WriteFile(os.Getenv("WIRECMD_CONNECT_FAILURE_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	reader := bufio.NewReader(os.Stdin)
	if _, err := reader.ReadBytes('\n'); err != nil {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":1,"error":{"code":-32601,"message":"server/discover unavailable"}}`)
	if _, err := reader.ReadBytes('\n'); err != nil {
		return
	}
	_, _ = fmt.Fprintln(os.Stdout, `{"jsonrpc":"2.0","id":2,"result":{"protocolVersion":"unsupported","capabilities":{},"serverInfo":{"name":"failure","version":"dev"}}}`)
	_, _ = reader.ReadBytes('\n') // waits for the connection close from Wirecmd.
}

func TestDirectListAndCallContracts(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	config := helperConfig(t, "", "")

	code, output, stderr := invoke(t, []string{"--direct", "--config", config})
	if code != exitOK || stderr != "" {
		t.Fatalf("list servers: code=%d stderr=%q output=%s", code, stderr, output)
	}
	servers := decodeOutput(t, output)
	if servers["ok"] != true || servers["servers"].([]any)[0].(map[string]any)["name"] != "helper" {
		t.Fatalf("servers output = %#v", servers)
	}

	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "helper"})
	if code != exitOK || stderr != "" {
		t.Fatalf("list tools: code=%d stderr=%q output=%s", code, stderr, output)
	}
	tools := decodeOutput(t, output)["tools"].([]any)
	if tools[0].(map[string]any)["name"] != "a_tool" || tools[len(tools)-1].(map[string]any)["name"] != "z_tool" {
		t.Fatalf("tool order = %#v", tools)
	}

	arguments, parseErr := decodeJSONObject([]byte(`{"count":12345678901234567890}`), "tool arguments")
	if parseErr != nil || arguments["count"].(json.Number).String() != "12345678901234567890" {
		t.Fatalf("UseNumber arguments = %#v error=%v", arguments, parseErr)
	}
	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "--json", `{"count":12345678901234567890}`, "helper", "a_tool"})
	if code != exitOK || stderr != "" {
		t.Fatalf("JSON call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	data := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["count"] == nil {
		t.Fatalf("JSON call data = %#v", data)
	}

	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "helper", ` {"tool":"a_tool","arguments":{"name":"Ada"}}`})
	if code != exitOK || stderr != "" {
		t.Fatalf("exact call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	data = decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["name"] != "Ada" {
		t.Fatalf("exact call data = %#v", data)
	}

	code, output, _ = invokeWithInput(t, []string{"--direct", "--config", config, "--stdin", "helper", "a_tool"}, `{"name":"Ada"}`)
	if code != exitOK || decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["name"] != "Ada" {
		t.Fatalf("stdin call: code=%d output=%s", code, output)
	}
}

func TestInvocationValidationAndDaemonFailure(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	config := helperConfig(t, "", "")
	tests := []struct {
		name string
		args []string
		code int
		want string
	}{
		{name: "normal mode fails closed", args: []string{"--config", config}, code: exitTransport, want: "daemon_unavailable"},
		{name: "invalid projected suffix", args: []string{"--direct", "--config", config, "helper", "a_tool", "extra"}, code: exitInvocation, want: "projected_argument_invalid"},
		{name: "input conflict", args: []string{"--direct", "--config", config, "--json", `{}`, "--stdin", "helper", "a_tool"}, code: exitInvocation, want: "input_mode_conflict"},
		{name: "non object", args: []string{"--direct", "--config", config, "--json", `[]`, "helper", "a_tool"}, code: exitInvocation, want: "invalid_json"},
		{name: "empty JSON is still JSON input", args: []string{"--direct", "--config", config, "--json", ``, "helper", "a_tool"}, code: exitInvocation, want: "invalid_json"},
		{name: "trailing object", args: []string{"--direct", "--config", config, "--json", `{} {}`, "helper", "a_tool"}, code: exitInvocation, want: "invalid_json"},
		{name: "malformed envelope has no fallback", args: []string{"--direct", "--config", config, "helper", `{"tool":`}, code: exitInvocation, want: "invalid_exact_call"},
		{name: "unknown envelope field", args: []string{"--direct", "--config", config, "helper", `{"tool":"a_tool","unknown":true}`}, code: exitInvocation, want: "invalid_exact_call"},
		{name: "config after positional is suffix", args: []string{"--direct", "helper", "--config", config}, code: exitInvocation, want: "projected_argument_invalid"},
		{name: "JSON escapes unusual tool name", args: []string{"--direct", "--config", config, "--json", `{}`, "helper", "{unusual"}, code: exitProtocol, want: "tool_call_failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, output, _ := invoke(t, test.args)
			if code != test.code || decodeOutput(t, output)["error"].(map[string]any)["code"] != test.want {
				t.Fatalf("code=%d output=%s, want code=%d error=%q", code, output, test.code, test.want)
			}
		})
	}
}

func TestHelpDoesNotRequireConfiguration(t *testing.T) {
	code, output, stderr := invoke(t, []string{"--help"})
	if code != exitOK || stderr != "" || !strings.Contains(output, "--config PATH") || !strings.HasSuffix(output, "\n") {
		t.Fatalf("help: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestFocusedHelpAndProjectedArguments(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	config := helperConfig(t, "", "")

	code, output, stderr := invoke(t, []string{"--direct", "--config", config, "--help", "helper"})
	if code != exitOK || stderr != "" || !strings.Contains(output, "projected") || strings.HasPrefix(output, "{") {
		t.Fatalf("server help: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "--help", "helper", "projected"})
	for _, want := range []string{"--query", "JSON: query (string)", "--filters", "Value: one JSON value", "tool_name  JSON-only", "Exact JSON fallback", "Output schema:"} {
		if !strings.Contains(output, want) {
			t.Fatalf("tool help missing %q: %s", want, output)
		}
	}
	if code != exitOK || stderr != "" || !strings.HasSuffix(output, "\n") {
		t.Fatalf("tool help: code=%d stderr=%q output=%s", code, stderr, output)
	}

	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "helper", "projected", "--query", "Ada", "--limit=12", "--enabled", "--filters", `{"status":"open"}`, "--", `{"tool_name":"one","toolName":"two","weird.name":"three"}`})
	if code != exitOK || stderr != "" {
		t.Fatalf("projected call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	data := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["query"] != "Ada" || data["limit"].(json.Number).String() != "12" || data["enabled"] != true || data["filters"].(map[string]any)["status"] != "open" || data["tool_name"] != "one" || data["toolName"] != "two" {
		t.Fatalf("projected data = %#v", data)
	}

	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"unknown", []string{"--direct", "--config", config, "helper", "projected", "--missing", "x"}, "projected_argument_unknown"},
		{"duplicate flag", []string{"--direct", "--config", config, "helper", "projected", "--query", "one", "--query", "two"}, "projected_argument_duplicate"},
		{"duplicate overlay", []string{"--direct", "--config", config, "helper", "projected", "--query", "one", "--", `{"query":"two"}`}, "projected_argument_duplicate"},
		{"complex invalid", []string{"--direct", "--config", config, "helper", "projected", "--filters", "not-json"}, "projected_json_invalid"},
		{"boolean invalid", []string{"--direct", "--config", config, "helper", "projected", "--enabled", "yes"}, "projected_boolean_invalid"},
		{"overlay trailing", []string{"--direct", "--config", config, "helper", "projected", "--", `{}`, "tail"}, "raw_overlay_invalid"},
		{"input conflict", []string{"--direct", "--config", config, "--json", `{}`, "helper", "projected", "--query", "Ada"}, "input_with_projected_arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, output, _ := invoke(t, test.args)
			if code != exitInvocation || decodeOutput(t, output)["error"].(map[string]any)["code"] != test.want {
				t.Fatalf("code=%d output=%s want=%s", code, output, test.want)
			}
		})
	}
}

func TestFocusedHelpRedactsSchemaMetadata(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	const secret = "schema-secret-value"
	t.Setenv("WIRECMD_SCHEMA_SECRET", secret)
	config := helperConfig(t, "", `env SECRET=(secret)"env://WIRECMD_SCHEMA_SECRET"`)
	code, output, stderr := invoke(t, []string{"--direct", "--config", config, "--help", "helper", "schema_secret"})
	if code != exitOK || stderr != "" || strings.Contains(output, secret) || !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("schema help redaction: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestProjectedSecretPropertyUsesPrivateSchema(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	const secret = "projected-schema-secret"
	t.Setenv("WIRECMD_SCHEMA_SECRET", secret)
	config := helperConfig(t, "", `env SECRET=(secret)"env://WIRECMD_SCHEMA_SECRET"`)
	flag, ok := projectedFlag(secret)
	if !ok {
		t.Fatal("fixture secret must form a projected flag")
	}
	code, output, stderr := invoke(t, []string{"--direct", "--config", config, "--help", "helper", "semantic_secret"})
	if code != exitOK || stderr != "" || strings.Contains(output, secret) || !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("secret schema help: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "helper", "semantic_secret", "--" + flag, "value"})
	if code != exitOK || stderr != "" || strings.Contains(output, secret) || decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["matched"] != true {
		t.Fatalf("secret projected call: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestProjectedNumbersUseNumber(t *testing.T) {
	description := toolDescription{InputSchema: json.RawMessage(`{"type":"object","properties":{"value":{"type":"number"}}}`)}
	arguments, appErr := resolveProjectedArguments(description, []projectedArgument{{Name: "value", Value: "12345678901234567890", ValueSet: true}}, nil)
	if appErr != nil || arguments["value"].(json.Number).String() != "12345678901234567890" {
		t.Fatalf("projected number = %#v error=%v", arguments, appErr)
	}
}

func TestProjectedNumberSyntaxAndOverlayOnlyFallback(t *testing.T) {
	description := toolDescription{InputSchema: json.RawMessage(`{"type":"object","properties":{"number":{"type":"number"},"integer":{"type":"integer"}}}`)}
	arguments, appErr := resolveProjectedArguments(description, []projectedArgument{{Name: "number", Value: "1e2", ValueSet: true}, {Name: "integer", Value: "0.0", ValueSet: true}}, nil)
	if appErr != nil || arguments["number"].(json.Number).String() != "1e2" || arguments["integer"].(json.Number).String() != "0.0" {
		t.Fatalf("number syntax = %#v error=%v", arguments, appErr)
	}
	arguments, appErr = resolveProjectedArguments(toolDescription{InputSchema: json.RawMessage(`{"type":"array"}`)}, nil, map[string]any{"unprojectable": true})
	if appErr != nil || arguments["unprojectable"] != true {
		t.Fatalf("overlay-only fallback = %#v error=%v", arguments, appErr)
	}
}

func TestProjectedFlagNormalizationAndCollisions(t *testing.T) {
	for _, test := range []struct {
		name string
		want string
		ok   bool
	}{
		{"tool_name", "tool-name", true},
		{"toolName", "tool-name", true},
		{"XMLParser", "xml-parser", true},
		{"many__parts", "many-parts", true},
		{"é", "", false},
	} {
		got, ok := projectedFlag(test.name)
		if got != test.want || ok != test.ok {
			t.Fatalf("projectedFlag(%q) = (%q, %v), want (%q, %v)", test.name, got, ok, test.want, test.ok)
		}
	}
	description := toolDescription{InputSchema: json.RawMessage(`{"type":"object","properties":{"tool_name":{"type":"string"},"toolName":{"type":"string"}}}`)}
	if _, appErr := resolveProjectedArguments(description, []projectedArgument{{Name: "tool-name", Value: "value", ValueSet: true}}, nil); appErr == nil || appErr.code != "projected_argument_unknown" {
		t.Fatalf("collision must be overlay-only: %#v", appErr)
	}
}

func TestDirectExecutionEnvironmentRootAndRedaction(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("INHERITED", "parent")
	const secret = "fixture-secret-value"
	t.Setenv("WIRECMD_TEST_SECRET", secret)
	t.Setenv("WIRECMD_EMIT_SECRET", "1")

	directory := t.TempDir()
	project := filepath.Join(directory, "project")
	if err := os.Mkdir(project, 0o700); err != nil {
		t.Fatal(err)
	}
	config := helperConfigAt(t, filepath.Join(directory, "config.kdl"), "project", `
                env INHERITED="configured"
                env EMPTY=""
                env SECRET=(secret)"env://WIRECMD_TEST_SECRET"`)

	code, output, stderr := invoke(t, []string{"--direct", "--config", config, "helper", "environment"})
	if code != exitOK {
		t.Fatalf("environment: code=%d stderr=%q output=%s", code, stderr, output)
	}
	data := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["inherited"] != "configured" || data["empty"] != "" {
		t.Fatalf("environment data = %#v", data)
	}

	code, output, _ = invoke(t, []string{"--direct", "--config", config, "helper", "working_directory"})
	if code != exitOK {
		t.Fatalf("working directory: code=%d output=%s", code, output)
	}
	data = decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["cwd"] != project {
		t.Fatalf("cwd = %q, want %q", data["cwd"], project)
	}

	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "helper", "secret"})
	if code != exitOK || strings.Contains(output, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("redaction: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	if !strings.Contains(output, "[REDACTED]") || !strings.Contains(stderr, "[REDACTED]") {
		t.Fatalf("redaction marker missing: stdout=%q stderr=%q", output, stderr)
	}

	code, output, _ = invoke(t, []string{"--direct", "--config", config, "helper", "secret_keys"})
	if code != exitOK || strings.Contains(output, secret) {
		t.Fatalf("key redaction: code=%d stdout=%q", code, output)
	}
	data = decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["[REDACTED]"] != "second" || data["[REDACTED]#2"] != "first" {
		t.Fatalf("key redaction collision = %#v", data)
	}
}

func TestDirectErrorResultsAndMissingSecret(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	config := helperConfig(t, "", "")
	code, output, _ := invoke(t, []string{"--direct", "--config", config, "helper", "failure"})
	if code != exitUpstreamTool {
		t.Fatalf("tool failure code = %d output=%s", code, output)
	}
	decoded := decodeOutput(t, output)
	if decoded["error"].(map[string]any)["category"] != "upstream_tool" || decoded["result"].(map[string]any)["messages"].([]any)[0] != "tool failure" {
		t.Fatalf("tool failure output = %#v", decoded)
	}

	code, output, _ = invoke(t, []string{"--direct", "--config", config, "helper", "binary"})
	if code != exitProtocol || decodeOutput(t, output)["error"].(map[string]any)["code"] != "unsupported_result" {
		t.Fatalf("binary output = %s", output)
	}

	code, output, _ = invoke(t, []string{"--direct", "--config", config, "helper", "input_required"})
	if code != exitUserAction || decodeOutput(t, output)["error"].(map[string]any)["code"] != "input_required" {
		t.Fatalf("input required output = %s", output)
	}

	missingConfig := helperConfig(t, "", `env SECRET=(secret)"env://WIRECMD_ABSENT_SECRET"`)
	previous, existed := os.LookupEnv("WIRECMD_ABSENT_SECRET")
	_ = os.Unsetenv("WIRECMD_ABSENT_SECRET")
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv("WIRECMD_ABSENT_SECRET", previous)
		} else {
			_ = os.Unsetenv("WIRECMD_ABSENT_SECRET")
		}
	})
	code, output, _ = invoke(t, []string{"--direct", "--config", missingConfig, "helper", "a_tool"})
	if code != exitConfiguration || decodeOutput(t, output)["error"].(map[string]any)["code"] != "secret_not_available" {
		t.Fatalf("missing secret: code=%d output=%s", code, output)
	}
}

func TestConnectFailureReapsStartedChild(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "child.pid")
	command := exec.Command(os.Args[0], "-test.run=TestConnectFailureProcess", "--")
	command.Env = append(os.Environ(),
		"GO_WIRECMD_CONNECT_FAILURE=1",
		"WIRECMD_CONNECT_FAILURE_PID="+pidPath,
	)
	session, appErr := connect(context.Background(), command, newRedactor(nil, io.Discard))
	if session != nil || appErr == nil || appErr.code != "mcp_connect_failed" {
		t.Fatalf("connect = (%v, %#v), want connection failure", session, appErr)
	}
	if _, err := os.ReadFile(pidPath); err != nil {
		t.Fatalf("failure fixture did not start: %v", err)
	}
	if command.ProcessState == nil {
		t.Fatalf("started child was not reaped: process state=%#v", command.ProcessState)
	}
}

func TestRepeatedConfigsUseStrongestLayer(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	base := writeConfig(t, `wirecmd { server "helper" { scope "workspace"; stdio "definitely-not-a-command" } }`)
	local := helperConfig(t, "", "")
	code, output, stderr := invoke(t, []string{"--direct", "--config", base, "--config", local, "helper"})
	if code != exitOK || stderr != "" || len(decodeOutput(t, output)["tools"].([]any)) == 0 {
		t.Fatalf("ordered configs: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestDirectServerListDoesNotStartOrResolveUnselectedServers(t *testing.T) {
	config := writeConfig(t, `wirecmd {
        server "not-started" { scope "workspace"; stdio "definitely-not-a-command" }
        server "needs-secret" {
            scope "workspace"
            stdio "also-not-started" { env SECRET=(secret)"env://WIRECMD_UNSET_LIST_SECRET" }
        }
    }`)
	code, output, stderr := invoke(t, []string{"--direct", "--config", config})
	if code != exitOK || stderr != "" || len(decodeOutput(t, output)["servers"].([]any)) != 2 {
		t.Fatalf("server listing: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestDirectProcessStartFailureIsTransportError(t *testing.T) {
	config := writeConfig(t, `wirecmd { server "broken" { scope "workspace"; stdio "definitely-not-a-command" } }`)
	code, output, _ := invoke(t, []string{"--direct", "--config", config, "broken"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "mcp_connect_failed" {
		t.Fatalf("process failure: code=%d output=%s", code, output)
	}
}

func TestRedactorProtectsSplitSecrets(t *testing.T) {
	tests := []struct {
		name      string
		secret    string
		fragments []string
		want      string
	}{
		{name: "ordinary split", secret: "abcdef", fragments: []string{"prefix ab", "cdef suffix"}, want: "prefix [REDACTED] suffix"},
		{name: "overlapping prefix", secret: "aba", fragments: []string{"aaba"}, want: "a[REDACTED]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			redactor := newRedactor([]string{test.secret}, &output)
			for _, fragment := range test.fragments {
				if _, err := redactor.Write([]byte(fragment)); err != nil {
					t.Fatal(err)
				}
			}
			redactor.FlushTo(&output)
			if got := output.String(); got != test.want {
				t.Fatalf("redacted output = %q, want %q", got, test.want)
			}
		})
	}
}

func helperConfig(t *testing.T, root, env string) string {
	return helperConfigAt(t, filepath.Join(t.TempDir(), "wirecmd.kdl"), root, env)
}

func helperConfigAt(t *testing.T, path, root, env string) string {
	t.Helper()
	rootNode := ""
	if root != "" {
		rootNode = "root " + strconv.Quote(root)
	}
	source := "wirecmd {\n" + rootNode + "\nserver \"helper\" {\nscope \"workspace\"\nstdio " + strconv.Quote(os.Args[0]) + " {\narg \"-test.run=TestHelperProcess\"\narg \"--\"\n" + env + "\n}\n}\n}"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeConfig(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wirecmd.kdl")
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func invoke(t *testing.T, args []string) (int, string, string) {
	t.Helper()
	return invokeWithInput(t, args, "")
}

func invokeWithInput(t *testing.T, args []string, input string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, strings.NewReader(input), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func decodeOutput(t *testing.T, output string) map[string]any {
	t.Helper()
	var decoded map[string]any
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode output %q: %v", output, err)
	}
	return decoded
}
