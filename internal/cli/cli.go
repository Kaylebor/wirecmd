// Package cli implements Wirecmd's first machine-composable command surface.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	exitOK             = 0
	exitInvocation     = 2
	exitConfiguration  = 3
	exitAuthentication = 4
	exitUpstreamTool   = 5
	exitProtocol       = 6
	exitTransport      = 7
	exitUserAction     = 8
	exitInternal       = 70
)

// Run executes Wirecmd with args and writes exactly one JSON envelope for
// ordinary operations. It returns the documented process exit status.
func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	result, appErr, help := run(ctx, args, in, errOut)
	if runner, ok := result.(foregroundDaemon); ok {
		return runner.run(ctx, out, errOut)
	}
	if help {
		writeJSON(out, map[string]any{"ok": true, "help": usage})
		return exitOK
	}
	if appErr != nil {
		writeJSON(out, failureEnvelope(appErr))
		return appErr.exitCode
	}
	writeJSON(out, result)
	return exitOK
}

const usage = "wirecmd [--config PATH] [--direct] [--json OBJECT|--stdin] [<server> [<tool>|<exact-call-object>]]"

type options struct {
	configs []string
	direct  bool
	json    string
	jsonSet bool
	stdin   bool
	help    bool
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	if value == "" {
		return errors.New("config path must not be empty")
	}
	*s = append(*s, value)
	return nil
}

type request struct {
	operation operation
	server    string
	tool      string
	arguments map[string]any
}

type operation uint8

const (
	listServers operation = iota
	listTools
	callTool
)

func run(ctx context.Context, args []string, in io.Reader, errOut io.Writer) (any, *appError, bool) {
	opts, positionals, parseErr := parseOptions(args)
	if parseErr != nil {
		return nil, invocationError("invalid_flags", parseErr.Error(), "place Wirecmd flags before the server and tool names"), false
	}
	if opts.help {
		return nil, nil, true
	}
	if admin, ok := parseDaemonAdmin(positionals, opts); ok {
		if admin.err != nil {
			return nil, admin.err, false
		}
		switch admin.command {
		case "run":
			return foregroundDaemon{}, nil, false
		case "status", "reload":
			result, appErr := runDaemonAdmin(ctx, admin.command, errOut)
			return result, appErr, false
		}
	}
	req, requestErr := parseRequest(positionals, opts, in)
	if requestErr != nil {
		return nil, requestErr, false
	}
	if len(opts.configs) == 0 {
		return nil, configurationError("config_required", "at least one --config PATH is required", "supply one or more KDL configuration files"), false
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, transportError("caller_cwd_unavailable", err.Error(), "run Wirecmd from an accessible working directory"), false
	}
	configPaths, err := absoluteConfigPaths(cwd, opts.configs)
	if err != nil {
		return nil, configurationError("config_path_invalid", err.Error(), "supply valid configuration paths"), false
	}
	// The daemon handshake precedes configuration parsing and secret lookup.
	// Besides failing clearly when it is unavailable, this makes normal-mode
	// configuration evaluation conditional on a compatible lifecycle broker.
	var daemonClient *daemonClient
	if !opts.direct {
		var appErr *appError
		daemonClient, appErr = openDaemonClient(ctx)
		if appErr != nil {
			return nil, appErr, false
		}
		defer daemonClient.Close()
	}
	cfg, err := config.LoadEffective(configPaths)
	if err != nil {
		return nil, configurationError("config_invalid", err.Error(), "correct the supplied KDL configuration"), false
	}
	if req.operation == listServers {
		if !opts.direct {
			return daemonRequestCallWithClient(daemonClient, daemonRequestFromConfig(req, cfg, cwd, configPaths, nil), errOut)
		}
		return serverList(cfg), nil, false
	}
	server, ok := findServer(cfg, req.server)
	if !ok {
		return nil, configurationError("server_not_found", fmt.Sprintf("configured server %q was not found", req.server), "list configured servers and choose one by name"), false
	}
	if !opts.direct {
		secrets := selectedSecretInputs(server, os.LookupEnv)
		return daemonRequestCallWithClient(daemonClient, daemonRequestFromConfig(req, cfg, cwd, configPaths, secrets), errOut)
	}

	command, secrets, commandErr := makeCommand(server, cfg.Root, cwd, os.LookupEnv)
	if commandErr != nil {
		return nil, commandErr, false
	}
	redactor := newRedactor(secrets, errOut)
	defer redactor.FlushTo(errOut)
	if req.operation == listTools {
		tools, runErr := directTools(ctx, command, redactor)
		if runErr != nil {
			return nil, runErr.redacted(redactor), false
		}
		return toolsEnvelope{OK: true, Server: server.Name, Tools: tools}, nil, false
	}
	call, runErr := directCall(ctx, command, req.tool, req.arguments, redactor)
	if runErr != nil {
		return nil, runErr.redacted(redactor), false
	}
	return callEnvelope{OK: true, Server: server.Name, Tool: req.tool, Result: call}, nil, false
}

func parseOptions(args []string) (options, []string, error) {
	var opts options
	flags := flag.NewFlagSet("wirecmd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Var((*stringList)(&opts.configs), "config", "KDL configuration file")
	flags.BoolVar(&opts.direct, "direct", false, "run without the local daemon")
	flags.Func("json", "exact JSON tool arguments", func(value string) error {
		opts.json = value
		opts.jsonSet = true
		return nil
	})
	flags.BoolVar(&opts.stdin, "stdin", false, "read exact JSON tool arguments from stdin")
	flags.BoolVar(&opts.help, "help", false, "show usage")
	flags.BoolVar(&opts.help, "h", false, "show usage")
	if err := flags.Parse(args); err != nil {
		return options{}, nil, err
	}
	return opts, flags.Args(), nil
}

func parseRequest(positionals []string, opts options, in io.Reader) (request, *appError) {
	if opts.jsonSet && opts.stdin {
		return request{}, invocationError("input_mode_conflict", "--json and --stdin cannot be used together", "choose exactly one JSON input mode")
	}
	switch len(positionals) {
	case 0:
		if opts.jsonSet || opts.stdin {
			return request{}, invocationError("input_without_call", "JSON input is only valid when calling a tool", "supply <server> <tool> before JSON input flags")
		}
		return request{operation: listServers}, nil
	case 1:
		if opts.jsonSet || opts.stdin {
			return request{}, invocationError("input_without_call", "JSON input is only valid when calling a tool", "supply a tool name after the server")
		}
		return request{operation: listTools, server: positionals[0]}, nil
	case 2:
		if !opts.jsonSet && !opts.stdin && startsJSONObject(positionals[1]) {
			tool, arguments, err := parseExactCall(positionals[1])
			if err != nil {
				return request{}, err
			}
			return request{operation: callTool, server: positionals[0], tool: tool, arguments: arguments}, nil
		}
		arguments, err := parseArguments(opts, in)
		if err != nil {
			return request{}, err
		}
		if positionals[1] == "" {
			return request{}, invocationError("tool_required", "tool name must not be empty", "supply a non-empty tool name")
		}
		return request{operation: callTool, server: positionals[0], tool: positionals[1], arguments: arguments}, nil
	default:
		return request{}, invocationError("suffix_arguments_unsupported", "arguments after <server> <tool> are not supported yet", "use --json, --stdin, or an exact call object; projected tool arguments are deferred")
	}
}

func startsJSONObject(value string) bool {
	return strings.HasPrefix(strings.TrimLeftFunc(value, unicode.IsSpace), "{")
}

func parseArguments(opts options, in io.Reader) (map[string]any, *appError) {
	if opts.stdin {
		input, err := io.ReadAll(in)
		if err != nil {
			return nil, invocationError("stdin_read_failed", err.Error(), "provide one JSON object on standard input")
		}
		return decodeJSONObject(input, "tool arguments")
	}
	if opts.jsonSet {
		return decodeJSONObject([]byte(opts.json), "tool arguments")
	}
	return map[string]any{}, nil
}

func parseExactCall(input string) (string, map[string]any, *appError) {
	var envelope map[string]json.RawMessage
	if err := decodeJSON([]byte(input), &envelope); err != nil {
		return "", nil, invocationError("invalid_exact_call", err.Error(), "provide one exact JSON call object")
	}
	for key := range envelope {
		if key != "tool" && key != "arguments" {
			return "", nil, invocationError("invalid_exact_call", fmt.Sprintf("unknown exact-call field %q", key), "use only tool and optional arguments fields")
		}
	}
	rawTool, ok := envelope["tool"]
	if !ok {
		return "", nil, invocationError("invalid_exact_call", "exact call requires a tool field", "supply a non-empty tool name")
	}
	var tool string
	if err := decodeJSON(rawTool, &tool); err != nil || tool == "" {
		return "", nil, invocationError("invalid_exact_call", "exact call tool must be a non-empty string", "supply a non-empty tool name")
	}
	arguments := map[string]any{}
	if rawArguments, ok := envelope["arguments"]; ok {
		decoded, err := decodeJSONObject(rawArguments, "exact call arguments")
		if err != nil {
			return "", nil, err
		}
		arguments = decoded
	}
	return tool, arguments, nil
}

func decodeJSONObject(input []byte, label string) (map[string]any, *appError) {
	var value any
	if err := decodeJSON(input, &value); err != nil {
		return nil, invocationError("invalid_json", fmt.Sprintf("%s must be one valid JSON object: %v", label, err), "provide one JSON object without trailing values")
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, invocationError("invalid_json", fmt.Sprintf("%s must be a JSON object", label), "provide an object such as {}")
	}
	return object, nil
}

func decodeJSON(input []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return errors.New("trailing JSON value")
		}
		return err
	}
	return nil
}

func findServer(cfg *config.Config, name string) (config.Server, bool) {
	for _, server := range cfg.Servers {
		if server.Name == name {
			return server, true
		}
	}
	return config.Server{}, false
}

func serverList(cfg *config.Config) serversEnvelope {
	servers := make([]serverSummary, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		servers = append(servers, serverSummary{Name: server.Name, Scope: string(server.Scope), Transport: "stdio"})
	}
	return serversEnvelope{OK: true, Servers: servers}
}

func makeCommand(server config.Server, root *config.Root, callerCWD string, lookup func(string) (string, bool)) (*exec.Cmd, []string, *appError) {
	arguments := make([]string, 0, len(server.Stdio.Args))
	secrets := make([]string, 0, len(server.Stdio.Env)+len(server.Stdio.Args))
	for _, arg := range server.Stdio.Args {
		value, err := arg.ResolveEnv(lookup)
		if err != nil {
			return nil, nil, configurationError("secret_not_available", err.Error(), "set the required environment variable before invoking Wirecmd")
		}
		arguments = append(arguments, value.Text)
		if value.Sensitive && value.Text != "" {
			secrets = append(secrets, value.Text)
		}
	}
	env := os.Environ()
	for _, assignment := range server.Stdio.Env {
		value, err := assignment.Value.ResolveEnv(lookup)
		if err != nil {
			return nil, nil, configurationError("secret_not_available", err.Error(), "set the required environment variable before invoking Wirecmd")
		}
		env = replaceEnvironment(env, assignment.Name, value.Text)
		if value.Sensitive && value.Text != "" {
			secrets = append(secrets, value.Text)
		}
	}
	command := exec.Command(server.Stdio.Command, arguments...)
	command.Env = env
	if root != nil {
		command.Dir = resolveRoot(*root)
	} else {
		command.Dir = callerCWD
	}
	return command, secrets, nil
}

func replaceEnvironment(env []string, name, value string) []string {
	prefix := name + "="
	result := env[:0]
	for _, entry := range env {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func resolveRoot(root config.Root) string {
	if filepath.IsAbs(root.Path) {
		return root.Path
	}
	return filepath.Join(filepath.Dir(root.File), root.Path)
}

func directTools(ctx context.Context, command *exec.Cmd, redactor *redactor) ([]toolSummary, *appError) {
	session, err := connect(ctx, command, redactor)
	if err != nil {
		return nil, err
	}
	defer session.Close()
	return sessionTools(ctx, session, redactor)
}

func sessionTools(ctx context.Context, session *mcp.ClientSession, redactor *redactor) ([]toolSummary, *appError) {
	var tools []toolSummary
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			if errors.Is(err, mcp.ErrConnectionClosed) {
				return nil, transportError("connection_closed", err.Error(), "check the upstream MCP server diagnostics")
			}
			return nil, protocolError("tool_list_failed", err.Error(), "check the upstream MCP server diagnostics")
		}
		tools = append(tools, toolSummary{Name: redactor.Redact(tool.Name), Title: redactor.Redact(tool.Title), Description: redactor.Redact(tool.Description)})
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools, nil
}

func directCall(ctx context.Context, command *exec.Cmd, tool string, arguments map[string]any, redactor *redactor) (toolResult, *appError) {
	session, err := connect(ctx, command, redactor)
	if err != nil {
		return toolResult{}, err
	}
	defer session.Close()
	return sessionCall(ctx, session, tool, arguments, redactor)
}

func sessionCall(ctx context.Context, session *mcp.ClientSession, tool string, arguments map[string]any, redactor *redactor) (toolResult, *appError) {
	response, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if callErr != nil {
		if errors.Is(callErr, mcp.ErrConnectionClosed) {
			return toolResult{}, transportError("connection_closed", callErr.Error(), "check the upstream MCP server diagnostics")
		}
		return toolResult{}, protocolError("tool_call_failed", callErr.Error(), "check the upstream MCP server diagnostics")
	}
	result, normalizeErr := normalizeResult(response, redactor)
	if normalizeErr != nil {
		return toolResult{}, normalizeErr
	}
	if response.NeedsInput() {
		return result, &appError{category: "user_action", code: "input_required", message: "the upstream tool requires additional user input", action: "use an explicit interactive flow when one is available", exitCode: exitUserAction, result: &result}
	}
	if response.IsError {
		return result, &appError{category: "upstream_tool", code: "tool_reported_error", message: "the upstream tool reported an error", action: "inspect the returned result and correct the tool invocation", exitCode: exitUpstreamTool, result: &result}
	}
	return result, nil
}

func connect(ctx context.Context, command *exec.Cmd, redactor *redactor) (*mcp.ClientSession, *appError) {
	command.Stderr = redactor
	transport := &trackedCommandTransport{transport: &mcp.CommandTransport{Command: command}}
	client := mcp.NewClient(&mcp.Implementation{Name: "wirecmd", Version: "dev"}, &mcp.ClientOptions{
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		if transport.connection != nil {
			_ = transport.connection.Close()
		}
		return nil, transportError("mcp_connect_failed", err.Error(), "check the server command, working directory, and upstream diagnostics")
	}
	return session, nil
}

// trackedCommandTransport retains the SDK-owned connection until
// Client.Connect returns a session. In v1.7.0 some initialization failures
// return without a session for the caller to close after CommandTransport has
// started its child. Closing this saved connection uses the SDK's ioConn
// sync.Once cleanup and its normal CommandTransport shutdown path.
type trackedCommandTransport struct {
	transport  *mcp.CommandTransport
	connection mcp.Connection
}

func (t *trackedCommandTransport) Connect(ctx context.Context) (mcp.Connection, error) {
	connection, err := t.transport.Connect(ctx)
	if err == nil {
		t.connection = connection
	}
	return connection, err
}

func normalizeResult(response *mcp.CallToolResult, redactor *redactor) (toolResult, *appError) {
	result := toolResult{Data: nil, Messages: make([]string, 0)}
	if response.StructuredContent != nil {
		value, err := normalizedJSON(response.StructuredContent)
		if err != nil {
			return toolResult{}, protocolError("unsupported_result", err.Error(), "the upstream tool returned unsupported structured content")
		}
		result.Data = redactJSON(value, redactor)
	}
	for _, content := range response.Content {
		text, ok := content.(*mcp.TextContent)
		if !ok {
			return toolResult{}, protocolError("unsupported_result", "the upstream tool returned non-text content", "use a tool that returns text or structured JSON in this slice")
		}
		result.Messages = append(result.Messages, redactor.Redact(text.Text))
	}
	return result, nil
}

func normalizedJSON(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var result any
	if err := decodeJSON(encoded, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func redactJSON(value any, redactor *redactor) any {
	switch value := value.(type) {
	case string:
		return redactor.Redact(value)
	case []any:
		for i := range value {
			value[i] = redactJSON(value[i], redactor)
		}
		return value
	case map[string]any:
		keys := make([]string, 0, len(value))
		for key := range value {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		result := make(map[string]any, len(value))
		for _, key := range keys {
			redactedKey := uniqueRedactedKey(result, redactor.Redact(key))
			result[redactedKey] = redactJSON(value[key], redactor)
		}
		return result
	default:
		return value
	}
}

func uniqueRedactedKey(object map[string]any, key string) string {
	if _, exists := object[key]; !exists {
		return key
	}
	for suffix := 2; ; suffix++ {
		candidate := key + "#" + strconv.Itoa(suffix)
		if _, exists := object[candidate]; !exists {
			return candidate
		}
	}
}

type appError struct {
	category string
	code     string
	message  string
	action   string
	exitCode int
	result   *toolResult
}

func (e *appError) redacted(redactor *redactor) *appError {
	if e == nil {
		return nil
	}
	copy := *e
	copy.message = redactor.Redact(copy.message)
	copy.action = redactor.Redact(copy.action)
	return &copy
}

func invocationError(code, message, action string) *appError {
	return &appError{category: "invocation", code: code, message: message, action: action, exitCode: exitInvocation}
}

func configurationError(code, message, action string) *appError {
	return &appError{category: "configuration", code: code, message: message, action: action, exitCode: exitConfiguration}
}

func protocolError(code, message, action string) *appError {
	return &appError{category: "upstream_protocol", code: code, message: message, action: action, exitCode: exitProtocol}
}

func transportError(code, message, action string) *appError {
	return &appError{category: "transport", code: code, message: message, action: action, exitCode: exitTransport}
}

func daemonUnavailable() *appError {
	return &appError{category: "transport", code: "daemon_unavailable", message: "the Wirecmd daemon is not running", action: "start it with wirecmd daemon run or use --direct deliberately", exitCode: exitTransport}
}

type errorBody struct {
	Category string `json:"category"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Action   string `json:"action"`
}

type failure struct {
	OK     bool        `json:"ok"`
	Error  errorBody   `json:"error"`
	Result *toolResult `json:"result,omitempty"`
}

func failureEnvelope(appErr *appError) failure {
	return failure{OK: false, Error: errorBody{Category: appErr.category, Code: appErr.code, Message: appErr.message, Action: appErr.action}, Result: appErr.result}
}

type serverSummary struct {
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Transport string `json:"transport"`
}

type serversEnvelope struct {
	OK      bool            `json:"ok"`
	Servers []serverSummary `json:"servers"`
}

type toolSummary struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type toolsEnvelope struct {
	OK     bool          `json:"ok"`
	Server string        `json:"server"`
	Tools  []toolSummary `json:"tools"`
}

type toolResult struct {
	Data     any      `json:"data"`
	Messages []string `json:"messages"`
}

type callEnvelope struct {
	OK     bool       `json:"ok"`
	Server string     `json:"server"`
	Tool   string     `json:"tool"`
	Result toolResult `json:"result"`
}

func writeJSON(writer io.Writer, value any) {
	encoded, err := json.Marshal(value)
	if err != nil {
		encoded = []byte(`{"ok":false,"error":{"category":"internal","code":"output_encode_failed","message":"failed to encode Wirecmd output","action":"report this internal error"}}`)
	}
	_, _ = writer.Write(append(encoded, '\n'))
}

// redactor keeps at most the suffix that could start a secret spanning the
// next write. It is intentionally only used for known resolved secret values.
type redactor struct {
	secrets []string
	pending string
	writer  io.Writer
}

func newRedactor(values []string, writer io.Writer) *redactor {
	seen := make(map[string]struct{}, len(values))
	secrets := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		secrets = append(secrets, value)
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	return &redactor{secrets: secrets, writer: writer}
}

func (r *redactor) Redact(value string) string {
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}

func (r *redactor) Write(input []byte) (int, error) {
	r.pending += string(input)
	output, pending := r.consume(false)
	_, _ = io.WriteString(r.writer, output)
	r.pending = pending
	return len(input), nil
}

func (r *redactor) FlushTo(writer io.Writer) {
	if r.pending == "" {
		return
	}
	output, _ := r.consume(true)
	_, _ = io.WriteString(writer, output)
	r.pending = ""
}

func (r *redactor) consume(final bool) (string, string) {
	var output strings.Builder
	for index := 0; index < len(r.pending); {
		if secret := r.matchAt(index); secret != "" {
			output.WriteString("[REDACTED]")
			index += len(secret)
			continue
		}
		if !final && r.startsIncompleteSecret(index) {
			return output.String(), r.pending[index:]
		}
		output.WriteString(r.pending[index : index+1])
		index++
	}
	return output.String(), ""
}

func (r *redactor) matchAt(index int) string {
	for _, secret := range r.secrets {
		if strings.HasPrefix(r.pending[index:], secret) {
			return secret
		}
	}
	return ""
}

func (r *redactor) startsIncompleteSecret(index int) bool {
	candidate := r.pending[index:]
	for _, secret := range r.secrets {
		if len(candidate) < len(secret) && strings.HasPrefix(secret, candidate) {
			return true
		}
	}
	return false
}
