package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kaylebor/wirecmd/internal/buildinfo"
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
	if countFile := os.Getenv("WIRECMD_CHILD_COUNT_FILE"); countFile != "" {
		file, err := os.OpenFile(countFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		_, _ = fmt.Fprintln(file, os.Getpid())
		_ = file.Close()
	}
	if startedFile := os.Getenv("WIRECMD_HELPER_STARTED_FILE"); startedFile != "" {
		if err := os.WriteFile(startedFile, []byte("started"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
	}
	if delay := os.Getenv("WIRECMD_START_DELAY"); delay != "" {
		duration, err := time.ParseDuration(delay)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		time.Sleep(duration)
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

func TestMCPOperationErrorClassifiesCancellation(t *testing.T) {
	for _, test := range []struct {
		name         string
		err          error
		wantCanceled bool
		wantCategory string
		wantCode     string
		wantExit     int
	}{
		{name: "ordinary protocol error", err: errors.New("invalid response"), wantCategory: "upstream_protocol", wantCode: "tool_list_failed", wantExit: exitProtocol},
		{name: "canceled", err: context.Canceled, wantCanceled: true, wantCategory: "transport", wantCode: "operation_canceled", wantExit: exitTransport},
		{name: "deadline", err: context.DeadlineExceeded, wantCanceled: true, wantCategory: "transport", wantCode: "operation_canceled", wantExit: exitTransport},
		{name: "wrapped canceled", err: fmt.Errorf("request: %w", context.Canceled), wantCanceled: true, wantCategory: "transport", wantCode: "operation_canceled", wantExit: exitTransport},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := mcpOperationError(test.err, "tool_list_failed")
			if got.sdkCanceled != test.wantCanceled || got.category != test.wantCategory || got.code != test.wantCode || got.exitCode != test.wantExit {
				t.Fatalf("mcpOperationError() = {sdkCanceled:%v category:%q code:%q exit:%d}, want {%v %q %q %d}", got.sdkCanceled, got.category, got.code, got.exitCode, test.wantCanceled, test.wantCategory, test.wantCode, test.wantExit)
			}
		})
	}
}

func TestVersionCommandIsStandaloneAndDoesNotReserveServerName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-path-is-invalid-for-discovery")
	t.Setenv("XDG_RUNTIME_DIR", "")

	code, output, stderr := invoke(t, []string{"--version"})
	if code != exitOK || output != "wirecmd "+buildinfo.Version()+"\n" || stderr != "" {
		t.Fatalf("version: code=%d stdout=%q stderr=%q", code, output, stderr)
	}

	for _, args := range [][]string{
		{"--version", "--direct"},
		{"--version", "--config", "wirecmd.kdl"},
		{"--version", "--json", `{}`},
		{"--version", "version"},
	} {
		code, output, stderr = invoke(t, args)
		if code != exitInvocation || stderr != "" || decodeOutput(t, output)["error"].(map[string]any)["code"] != "version_usage" {
			t.Fatalf("version combination %v: code=%d stdout=%q stderr=%q", args, code, output, stderr)
		}
	}

	t.Setenv("GO_WIRECMD_HELPER", "1")
	config := writeConfig(t, "wirecmd { server \"version\" { scope \"workspace\"; stdio "+strconv.Quote(os.Args[0])+" { arg \"-test.run=TestHelperProcess\"; arg \"--\" } } }")
	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "version"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["server"] != "version" {
		t.Fatalf("version server alias: code=%d stdout=%q stderr=%q", code, output, stderr)
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
	if path := os.Getenv("WIRECMD_BLOCK_STARTED_FILE"); path != "" {
		_ = os.WriteFile(path, []byte("started"), 0o600)
	}
	<-ctx.Done()
	if path := os.Getenv("WIRECMD_BLOCK_CANCELED_FILE"); path != "" {
		_ = os.WriteFile(path, []byte("canceled"), 0o600)
	}
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

func TestDirectStreamableHTTPContracts(t *testing.T) {
	fixture := newHTTPFixture(t)
	config := httpConfig(t, fixture.URL)

	code, output, stderr := invoke(t, []string{"--direct", "--config", config})
	if code != exitOK || stderr != "" {
		t.Fatalf("list servers: code=%d stderr=%q output=%s", code, stderr, output)
	}
	servers := decodeOutput(t, output)["servers"].([]any)
	if len(servers) != 1 || servers[0].(map[string]any)["transport"] != "http" {
		t.Fatalf("HTTP server list = %#v", servers)
	}
	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "remote"})
	if code != exitOK || stderr != "" {
		t.Fatalf("HTTP list tools: code=%d stderr=%q output=%s", code, stderr, output)
	}
	tools := decodeOutput(t, output)["tools"].([]any)
	if tools[0].(map[string]any)["name"] != "a_tool" || tools[len(tools)-1].(map[string]any)["name"] != "z_tool" {
		t.Fatalf("HTTP tool order = %#v", tools)
	}
	if !fixture.sawMethod("server/discover") {
		t.Fatal("HTTP fixture did not observe modern server/discover")
	}

	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "--help", "remote", "projected"})
	if code != exitOK || stderr != "" || !strings.Contains(output, "--query") {
		t.Fatalf("HTTP focused help: code=%d stderr=%q output=%s", code, stderr, output)
	}

	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "remote", "projected", "--query", "Ada", "--enabled", "--", `{"tool_name":"one","toolName":"two"}`})
	if code != exitOK || stderr != "" {
		t.Fatalf("HTTP projected call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	data := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["query"] != "Ada" || data["enabled"] != true || data["tool_name"] != "one" || data["toolName"] != "two" {
		t.Fatalf("HTTP projected data = %#v", data)
	}

	code, output, stderr = invoke(t, []string{"--direct", "--config", config, "remote", `{"tool":"a_tool","arguments":{"name":"Ada"}}`})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["name"] != "Ada" {
		t.Fatalf("HTTP exact call: code=%d stderr=%q output=%s", code, stderr, output)
	}

	code, output, _ = invoke(t, []string{"--direct", "--config", config, "remote", "failure"})
	if code != exitUpstreamTool || decodeOutput(t, output)["error"].(map[string]any)["code"] != "tool_reported_error" {
		t.Fatalf("HTTP tool failure: code=%d output=%s", code, output)
	}
	if fixture.requests.Load() == 0 {
		t.Fatal("HTTP fixture did not receive MCP requests")
	}
}

func TestDirectStreamableHTTPConfiguredQueryAndHeaders(t *testing.T) {
	fixture := newHTTPFixture(t)
	t.Setenv("WIRECMD_HTTP_TOKEN", "a/b c")
	t.Setenv("WIRECMD_HTTP_KEY", "header-secret")
	config := httpValuesConfig(t, fixture.URL+"?tenant=old&kept=yes")

	code, output, stderr := invoke(t, []string{"--direct", "--config", config, "remote"})
	if code != exitOK || stderr != "" {
		t.Fatalf("HTTP tool list: code=%d stderr=%q output=%s", code, stderr, output)
	}
	fixture.mu.Lock()
	query := fixture.lastQuery
	headers := fixture.lastHeaders
	fixture.mu.Unlock()
	if query.Get("tenant") != "acme" || query.Get("token") != "a/b c" || query.Get("kept") != "yes" {
		t.Fatalf("configured query = %#v", query)
	}
	if headers.Get("X-API-Key") != "header-secret" {
		t.Fatalf("configured header = %q", headers.Get("X-API-Key"))
	}
	if headers.Get("Accept") == "" || headers.Get("Content-Type") == "" || headers.Get("Mcp-Protocol-Version") == "" {
		t.Fatalf("SDK-owned headers were lost: %#v", headers)
	}
}

func TestDirectStreamableHTTPColdCallPrimesSDKToolCache(t *testing.T) {
	fixture := newHTTPFixture(t)
	config := httpConfig(t, fixture.URL)

	code, output, stderr := invoke(t, []string{"--direct", "--config", config, "remote", `{"tool":"header_tool","arguments":{"region":"EU"}}`})
	if code != exitOK || stderr != "" {
		t.Fatalf("cold HTTP call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	data := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["region"] != "EU" {
		t.Fatalf("cold HTTP call data = %#v", data)
	}
	if got := fixture.methodCount("tools/list"); got == 0 {
		t.Fatal("cold HTTP call did not prime the SDK tool cache")
	}
}

func TestDirectStreamableHTTPConnectionFailures(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + listener.Addr().String() + "/mcp"
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	refused := httpConfig(t, endpoint)
	code, output, _ := invoke(t, []string{"--direct", "--config", refused, "remote"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "mcp_connect_failed" {
		t.Fatalf("refused HTTP connection: code=%d output=%s", code, output)
	}
	nonMCP := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/plain")
		_, _ = writer.Write([]byte("not MCP"))
	}))
	t.Cleanup(nonMCP.Close)
	config := httpConfig(t, nonMCP.URL)
	code, output, _ = invoke(t, []string{"--direct", "--config", config, "remote"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "mcp_connect_failed" {
		t.Fatalf("non-MCP HTTP response: code=%d output=%s", code, output)
	}
}

func TestHTTPQueryIsRedactedFromConnectionDiagnostics(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	endpoint := "http://" + listener.Addr().String() + "/mcp?access_token=http-query-secret"
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	config := httpConfig(t, endpoint)
	code, output, stderr := invoke(t, []string{"--direct", "--config", config, "remote"})
	if code != exitTransport || strings.Contains(output, "access_token") || strings.Contains(output, "http-query-secret") || strings.Contains(stderr, "access_token") || strings.Contains(stderr, "http-query-secret") {
		t.Fatalf("direct HTTP query disclosure: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	if !strings.Contains(output, "?[REDACTED]") {
		t.Fatalf("direct HTTP endpoint was not usefully sanitized: %s", output)
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
	for _, want := range []string{"--query", "JSON: query (string)", "--filters", `Value: one JSON value for "filters", not {"filters": ...}`, "Illustrative shape (consult Input schema): --filters '{}'", "tool_name  JSON-only", "Exact JSON fallback", "Output schema:"} {
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

func TestFocusedHelpComplexProjectedShapeUsesBarePropertyValue(t *testing.T) {
	description := toolDescription{
		Name: "create_entities",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"entities":{
					"type":"array",
					"items":{
						"type":"object",
						"properties":{
							"name":{"type":"string"},
							"entityType":{"type":"string"},
							"observations":{"type":"array","items":{"type":"string"}}
						},
						"required":["name","entityType","observations"]
					}
				}
			}
		}`),
	}
	help := renderToolHelp("memory", description)
	for _, want := range []string{
		`Value: one JSON value for "entities", not {"entities": ...}`,
		`Illustrative shape (consult Input schema): --entities '[{"entityType":"...","name":"...","observations":["..."]}]'`,
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("tool help missing %q: %s", want, help)
		}
	}
	if strings.Contains(help, `--entities '{"entities":`) {
		t.Fatalf("projected shape must not wrap the property value: %s", help)
	}
}

func TestFocusedHelpSkipsProjectedShapeForComposedSchema(t *testing.T) {
	description := toolDescription{
		Name:        "search",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":["string","null"]},"mode":{"oneOf":[{"type":"string"},{"type":"integer"}]}}}`),
	}
	help := renderToolHelp("memory", description)
	for _, want := range []string{
		`Value: one JSON value for "query", not {"query": ...}`,
		`Value: one JSON value for "mode", not {"mode": ...}`,
	} {
		if !strings.Contains(help, want) {
			t.Fatalf("tool help missing %q: %s", want, help)
		}
	}
	if strings.Contains(help, "Illustrative shape (consult Input schema):") {
		t.Fatalf("composed schemas must not receive a projected shape: %s", help)
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
	canonicalProject, err := filepath.EvalSymlinks(project)
	if err != nil {
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
	if data["cwd"] != canonicalProject {
		t.Fatalf("cwd = %q, want %q", data["cwd"], canonicalProject)
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

func TestHTTPConnectFailureClosesEstablishedSession(t *testing.T) {
	deleted := make(chan struct{}, 1)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			select {
			case deleted <- struct{}{}:
			default:
			}
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		var message struct {
			ID json.RawMessage `json:"id"`
		}
		if err := json.NewDecoder(request.Body).Decode(&message); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.Header().Set("Mcp-Session-Id", "failed-initialize-session")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      message.ID,
			"error":   map[string]any{"code": -32601, "message": "initialization unavailable"},
		})
	}))
	defer server.Close()
	session, appErr := connectTarget(context.Background(), connectionTarget{endpoint: server.URL}, newRedactor(nil, io.Discard))
	if session != nil || appErr == nil || appErr.code != "mcp_connect_failed" {
		t.Fatalf("HTTP connect = (%v, %#v), want initialization failure", session, appErr)
	}
	select {
	case <-deleted:
	case <-time.After(time.Second):
		t.Fatal("failed HTTP initialization did not close the established session")
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

type httpFixture struct {
	*httptest.Server
	requests            atomic.Int64
	blockToolList       bool
	toolListStarted     chan struct{}
	toolListRelease     chan struct{}
	toolListOnce        sync.Once
	toolListReleaseOnce sync.Once
	blockStarted        chan struct{}
	blockRelease        chan struct{}
	blockOnce           sync.Once
	holdStarted         chan struct{}
	holdRelease         chan struct{}
	holdOnce            sync.Once
	holdReleaseOnce     sync.Once
	mu                  sync.Mutex
	methods             []string
	lastQuery           url.Values
	lastHeaders         http.Header
}

func newHTTPFixture(t *testing.T) *httpFixture {
	return newHTTPFixtureWithBlockedToolList(t, false)
}

func newHTTPFixtureWithBlockedToolList(t *testing.T, blockToolList bool) *httpFixture {
	t.Helper()
	fixture := &httpFixture{blockToolList: blockToolList, toolListStarted: make(chan struct{}), toolListRelease: make(chan struct{}), blockStarted: make(chan struct{}), blockRelease: make(chan struct{}), holdStarted: make(chan struct{}), holdRelease: make(chan struct{})}
	server := mcp.NewServer(&mcp.Implementation{Name: "wirecmd-http-test-server", Version: "dev"}, &mcp.ServerOptions{PageSize: 2})
	mcp.AddTool(server, &mcp.Tool{Name: "z_tool", Title: "Zed", Description: "last HTTP tool"}, echoTool)
	mcp.AddTool(server, &mcp.Tool{Name: "a_tool", Title: "Aye", Description: "first HTTP tool"}, echoTool)
	mcp.AddTool(server, &mcp.Tool{
		Name: "projected", Title: "Projected HTTP fixture", Description: "exercise HTTP focused help and projected arguments",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"enabled":{"type":"boolean"},"tool_name":{"type":"string"},"toolName":{"type":"string"}},"required":["query"]}`),
	}, echoTool)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "header_tool",
		Description: "exercise SDK-owned x-mcp-header generation",
		InputSchema: json.RawMessage(`{"type":"object","properties":{"region":{"type":"string","x-mcp-header":"Region"}},"required":["region"]}`),
	}, echoTool)
	mcp.AddTool(server, &mcp.Tool{Name: "failure", Description: "return a tool error"}, failureTool)
	mcp.AddTool(server, &mcp.Tool{Name: "block", Description: "wait for cancellation"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ map[string]any) (*mcp.CallToolResult, any, error) {
		fixture.blockOnce.Do(func() { close(fixture.blockStarted) })
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-fixture.blockRelease:
			return nil, nil, errors.New("fixture released")
		}
	})
	mcp.AddTool(server, &mcp.Tool{Name: "hold", Description: "hold one request until released"}, func(context.Context, *mcp.CallToolRequest, map[string]any) (*mcp.CallToolResult, any, error) {
		fixture.holdOnce.Do(func() { close(fixture.holdStarted) })
		<-fixture.holdRelease
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "released"}}}, nil, nil
	})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		fixture.requests.Add(1)
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true})
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.mu.Lock()
		fixture.lastQuery = request.URL.Query()
		fixture.lastHeaders = request.Header.Clone()
		fixture.mu.Unlock()
		body, err := io.ReadAll(request.Body)
		if err == nil {
			var message struct {
				Method string `json:"method"`
			}
			if json.Unmarshal(body, &message) == nil && message.Method != "" {
				fixture.mu.Lock()
				fixture.methods = append(fixture.methods, message.Method)
				fixture.mu.Unlock()
				if fixture.blockToolList && message.Method == "tools/list" {
					fixture.toolListOnce.Do(func() { close(fixture.toolListStarted) })
					<-fixture.toolListRelease
				}
			}
			request.Body = io.NopCloser(bytes.NewReader(body))
		}
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(func() {
		fixture.releaseToolList()
		close(fixture.blockRelease)
		fixture.releaseHold()
		fixture.Close()
	})
	return fixture
}

func (f *httpFixture) releaseToolList() {
	f.toolListReleaseOnce.Do(func() { close(f.toolListRelease) })
}

func (f *httpFixture) releaseHold() {
	f.holdReleaseOnce.Do(func() { close(f.holdRelease) })
}

func (f *httpFixture) sawMethod(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, method := range f.methods {
		if method == want {
			return true
		}
	}
	return false
}

func (f *httpFixture) methodCount(want string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, method := range f.methods {
		if method == want {
			count++
		}
	}
	return count
}

func httpConfig(t *testing.T, endpoint string) string {
	t.Helper()
	return writeConfig(t, "wirecmd { server \"remote\" { scope \"workspace\"; http "+strconv.Quote(endpoint)+" } }")
}

func httpValuesConfig(t *testing.T, endpoint string) string {
	t.Helper()
	return writeConfig(t, "wirecmd { server \"remote\" { scope \"workspace\"; http "+strconv.Quote(endpoint)+" { query tenant=\"acme\"; query token=(secret)\"env://WIRECMD_HTTP_TOKEN\"; header X-API-Key=(secret)\"env://WIRECMD_HTTP_KEY\" } } }")
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
