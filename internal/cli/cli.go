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
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Kaylebor/wirecmd/internal/buildinfo"
	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/discovery"
	"github.com/Kaylebor/wirecmd/internal/oauthstore"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
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

// Run executes Wirecmd with args. Successful help and version output are
// deliberately conventional plain text; other output is rendered according to
// the selected presentation mode.
func Run(ctx context.Context, args []string, in io.Reader, out, errOut io.Writer) int {
	opts, positionals, parseErr := parseOptions(args)
	if opts.completionServers {
		return runServerCompletion(opts, positionals, parseErr, out)
	}
	presentation := presentationFor(opts, out)
	result, appErr := run(ctx, opts, positionals, parseErr, in, errOut)
	if runner, ok := result.(foregroundDaemon); ok {
		return runner.run(ctx, out, errOut, presentation)
	}
	if help, ok := result.(helpText); ok {
		_, _ = io.WriteString(out, string(help))
		return exitOK
	}
	if version, ok := result.(versionText); ok {
		_, _ = fmt.Fprintf(out, "wirecmd %s\n", version)
		return exitOK
	}
	if appErr != nil {
		writeOutput(out, failureEnvelope(appErr), presentation)
		return appErr.exitCode
	}
	writeOutput(out, result, presentation)
	return exitOK
}

const usage = "wirecmd [--config PATH] [--direct] [--format auto|json|pretty] [--color auto|always|never] [--json OBJECT|--stdin] [<server> [<tool>|<exact-call-object>]]"

var isInteractiveTerminal = terminalIO
var openAuthorizationURL = openBrowserURL

type options struct {
	configs           []string
	direct            bool
	json              string
	jsonSet           bool
	stdin             bool
	help              bool
	version           bool
	format            outputFormat
	color             colorMode
	formatSet         bool
	colorSet          bool
	helpServer        bool // a prefix -- explicitly selects server/tool help
	completionServers bool
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
	projected []projectedArgument
	overlay   map[string]any
	help      helpKind
}

type operation uint8

const (
	listServers operation = iota
	listTools
	callTool
	inspectTool
	navigateLSP
	statusLSP
)

type versionText string

func run(ctx context.Context, opts options, positionals []string, parseErr error, in io.Reader, errOut io.Writer) (any, *appError) {
	if parseErr != nil {
		return nil, invocationError("invalid_flags", parseErr.Error(), "place Wirecmd flags before the server and tool names")
	}
	var suffixErr *appError
	positionals, opts, suffixErr, _ = normalizeTrailingHelp(positionals, opts)
	if suffixErr != nil {
		return nil, suffixErr
	}
	if opts.version {
		if opts.direct || len(opts.configs) != 0 || opts.jsonSet || opts.stdin || opts.help || opts.formatSet || opts.colorSet || len(positionals) != 0 {
			return nil, invocationError("version_usage", "--version must be used by itself", "run wirecmd --version")
		}
		return versionText(buildinfo.Version()), nil
	}
	if opts.help {
		if result, err, handled := lspHelp(positionals, opts); handled {
			return result, err
		}
		if result, err, handled := administrativeHelp(positionals, opts); handled {
			if err != nil {
				return nil, err
			}
			return result, err
		}
	}
	if !opts.help && !opts.helpServer && len(positionals) == 1 && positionals[0] == "lsp" && !opts.jsonSet && !opts.stdin {
		return helpText(lspHelpText()), nil
	}
	if !opts.help {
		if lspRequest, err, handled := parseLSPCommand(positionals, opts); handled {
			if err != nil {
				return nil, err
			}
			return executeLSPCommand(ctx, opts, lspRequest, in, errOut)
		}
	}
	if admin, ok := parseConfigAdmin(positionals, opts); ok && !opts.help {
		return runConfigAdmin(admin)
	}
	authAdmin, isAuthAdmin := parseAuthAdmin(positionals, opts)
	if opts.help {
		isAuthAdmin = false
	}
	if isAuthAdmin && authAdmin.err != nil {
		return nil, authAdmin.err
	}
	var req request
	var requestErr *appError
	if isAuthAdmin {
		req = request{operation: listTools, server: authAdmin.server}
	} else if opts.help {
		req, requestErr = parseHelpRequest(positionals, opts)
	} else {
		req, requestErr = parseRequest(positionals, opts, in)
	}
	if requestErr != nil {
		return nil, requestErr
	}
	if req.help == globalHelp {
		return helpText(globalHelpText()), nil
	}
	if admin, ok := parseDaemonAdmin(positionals, opts); ok && !opts.help {
		if admin.err != nil {
			return nil, admin.err
		}
		switch admin.command {
		case "run":
			return foregroundDaemon{}, nil
		case "status", "reload":
			result, appErr := runDaemonAdmin(ctx, admin.command, errOut)
			return result, appErr
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, transportError("caller_cwd_unavailable", err.Error(), "run Wirecmd from an accessible working directory")
	}
	var configPaths []string
	discovered := len(opts.configs) == 0
	if len(opts.configs) != 0 {
		configPaths, err = absoluteConfigPaths(cwd, opts.configs)
		if err != nil {
			return nil, configurationError("config_path_invalid", err.Error(), "supply valid configuration paths")
		}
	} else {
		configPaths, err = discovery.Paths(cwd)
		if err != nil {
			var untrusted *discovery.UntrustedError
			var notFound *discovery.NotFoundError
			switch {
			case errors.As(err, &untrusted):
				return nil, userActionError("workspace_untrusted", err.Error(), "run wirecmd config trust "+shellQuote(untrusted.Workspace))
			case errors.As(err, &notFound):
				return nil, configurationError("config_not_found", err.Error(), "create a global or workspace wirecmd.kdl, or supply --config PATH")
			default:
				return nil, configurationError("config_discovery_failed", err.Error(), "check Wirecmd configuration and trust state")
			}
		}
	}
	// The daemon handshake precedes configuration parsing and secret lookup.
	// Besides failing clearly when it is unavailable, this makes normal-mode
	// configuration evaluation conditional on a compatible lifecycle broker.
	var daemonClient *daemonClient
	var daemonOpenErr *appError
	if !opts.direct {
		daemonClient, daemonOpenErr = openDaemonClient(ctx)
		if daemonOpenErr != nil {
			return nil, daemonOpenErr
		}
		if daemonClient != nil {
			defer daemonClient.Close()
		}
	}
	var cfg *config.Config
	if discovered {
		cfg, err = config.LoadEffectiveDiscovered(configPaths)
	} else {
		cfg, err = config.LoadEffective(configPaths)
	}
	if err != nil {
		return nil, configurationError("config_invalid", err.Error(), "correct the supplied KDL configuration")
	}
	if req.operation == listServers {
		if !opts.direct {
			result, appErr, _ := daemonRequestCallWithClient(daemonClient, daemonRequestFromConfig(req, cfg, cwd, configPaths, discovered, nil), errOut)
			return result, appErr
		}
		return serverList(cfg), nil
	}
	server, ok := findServer(cfg, req.server)
	if !ok {
		return nil, configurationError("server_not_found", fmt.Sprintf("configured server %q was not found", req.server), "list configured servers and choose one by name")
	}
	if isAuthAdmin {
		if server.HTTP == nil || hasAuthorizationHeader(*server.HTTP) {
			return nil, authServerError(server.Name)
		}
		secrets := selectedSecretInputs(server, os.LookupEnv)
		if !opts.direct {
			daemonRequest := daemonRequestFromConfig(req, cfg, cwd, configPaths, discovered, secrets)
			daemonRequest.Auth = authAdmin.command
			daemonRequest.Interactive = isInteractiveTerminal(in, errOut) && os.Getenv("WIRECMD_NONINTERACTIVE") != "1"
			daemonClient.event = browserEventHandler(errOut, daemonEventRedactor(server, secrets, errOut))
			result, appErr, _ := daemonRequestCallWithClient(daemonClient, daemonRequest, errOut)
			return result, appErr
		}
		return runDirectAuth(ctx, authAdmin, server, cfg.Root, cwd, in, errOut)
	}
	if !opts.direct {
		secrets := selectedSecretInputs(server, os.LookupEnv)
		daemonRequest := daemonRequestFromConfig(req, cfg, cwd, configPaths, discovered, secrets)
		daemonRequest.Interactive = isInteractiveTerminal(in, errOut) && os.Getenv("WIRECMD_NONINTERACTIVE") != "1"
		daemonClient.event = browserEventHandler(errOut, daemonEventRedactor(server, secrets, errOut))
		result, appErr, _ := daemonRequestCallWithClient(daemonClient, daemonRequest, errOut)
		if appErr != nil {
			return result, appErr
		}
		if req.help != noHelp || (wantsToolHelpFallback(req.projected, req.overlay) && isDaemonHelpResponse(result)) {
			return renderDaemonHelp(result)
		}
		return result, nil
	}

	target, secrets, targetErr := makeTarget(server, cfg.Root, cwd, os.LookupEnv)
	if targetErr != nil {
		return nil, targetErr
	}
	redactor := newRedactor(secrets, errOut)
	redactor.ProtectEndpoint(target.endpoint)
	if server.HTTP != nil {
		interactive := isInteractiveTerminal(in, errOut) && os.Getenv("WIRECMD_NONINTERACTIVE") != "1"
		var oauthSecrets []string
		target, oauthSecrets, targetErr = attachOAuth(target, server.Name, *server.HTTP, os.LookupEnv, interactive, false, browserEventHandler(errOut, redactor))
		if targetErr != nil {
			return nil, targetErr
		}
		secrets = append(secrets, oauthSecrets...)
		redactor.ProtectSecrets(oauthSecrets...)
		if target.oauthRun != nil {
			target.oauthRun.setRedactor(redactor)
			defer target.oauthRun.Close()
		}
	}
	defer redactor.FlushTo(errOut)
	if req.operation == listTools {
		catalog, runErr := directToolCatalog(ctx, target, redactor)
		if runErr != nil {
			return nil, runErr.redacted(redactor)
		}
		if req.help == serverHelp {
			return helpText(renderServerHelp(server.Name, catalog.summaries)), nil
		}
		return toolsEnvelope{OK: true, Server: server.Name, Tools: catalog.summaries}, nil
	}
	if req.operation == inspectTool {
		description, runErr := directToolDescription(ctx, target, req.tool, redactor)
		if runErr != nil {
			return nil, runErr.redacted(redactor)
		}
		return helpText(renderToolHelp(server.Name, description)), nil
	}
	arguments := req.arguments
	if len(req.projected) != 0 || req.overlay != nil {
		call, description, showHelp, runErr := directProjectedCall(ctx, target, req.tool, req.projected, req.overlay, redactor)
		if showHelp {
			return helpText(renderToolHelp(server.Name, description)), nil
		}
		if runErr != nil {
			return nil, runErr.redacted(redactor)
		}
		return callEnvelope{OK: true, Server: server.Name, Tool: req.tool, Result: call}, nil
	}
	call, runErr := directCall(ctx, target, req.tool, arguments, redactor)
	if runErr != nil {
		return nil, runErr.redacted(redactor)
	}
	return callEnvelope{OK: true, Server: server.Name, Tool: req.tool, Result: call}, nil
}

func hasAuthorizationHeader(transport config.HTTP) bool {
	for _, header := range transport.Headers {
		if strings.EqualFold(header.Name, "Authorization") {
			return true
		}
	}
	return false
}

func browserEventHandler(errOut io.Writer, redactor *redactor) func(string) {
	return func(raw string) {
		display := raw
		if redactor != nil {
			display = redactAuthorizationURL(raw, redactor)
		}
		fmt.Fprintln(errOut, "wirecmd: open this authorization URL:", display)
		if err := openAuthorizationURL(raw); err != nil {
			fmt.Fprintln(errOut, "wirecmd: could not open the browser; open the URL manually")
		}
	}
}

func redactAuthorizationURL(raw string, redactor *redactor) string {
	parsed, err := url.Parse(raw)
	if err != nil {
		return redactor.Redact(raw)
	}
	query := parsed.Query()
	for name, values := range query {
		for index, value := range values {
			values[index] = redactor.Redact(value)
		}
		query[name] = values
	}
	parsed.RawQuery = query.Encode()
	return redactor.Redact(parsed.String())
}

func daemonEventRedactor(server config.Server, inputs map[string]secretInput, errOut io.Writer) *redactor {
	values := make([]string, 0, len(inputs))
	for _, input := range inputs {
		if input.Present {
			values = append(values, input.Value)
		}
	}
	redactor := newRedactor(values, errOut)
	if server.HTTP != nil {
		lookup := func(name string) (string, bool) { value, ok := inputs[name]; return value.Value, ok && value.Present }
		if target, _, appErr := makeHTTPTarget(*server.HTTP, lookup); appErr == nil {
			redactor.ProtectEndpoint(target.endpoint)
		}
	}
	return redactor
}

func terminalIO(in io.Reader, errOut io.Writer) bool {
	input, inputOK := in.(*os.File)
	output, outputOK := errOut.(*os.File)
	if !inputOK || !outputOK {
		return false
	}
	inputInfo, inputErr := input.Stat()
	outputInfo, outputErr := output.Stat()
	return inputErr == nil && outputErr == nil && inputInfo.Mode()&os.ModeCharDevice != 0 && outputInfo.Mode()&os.ModeCharDevice != 0
}

func parseOptions(args []string) (options, []string, error) {
	var opts options
	flags := flag.NewFlagSet("wirecmd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.BoolVar(&opts.completionServers, "completion-servers", false, "internal completion helper")
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
	flags.BoolVar(&opts.version, "version", false, "show version")
	flags.Func("format", "output format: auto, json, or pretty", func(value string) error {
		format, err := parseOutputFormat(value)
		if err != nil {
			return err
		}
		opts.format, opts.formatSet = format, true
		return nil
	})
	setColor := func(value string) error {
		color, err := parseColorMode(value)
		if err != nil {
			return err
		}
		opts.color, opts.colorSet = color, true
		return nil
	}
	flags.Func("color", "color mode: auto, always, or never", setColor)
	flags.Func("colour", "alias for --color", setColor)
	if err := flags.Parse(args); err != nil {
		return opts, nil, err
	}
	// Walk only consumed prefix tokens. A -- used as a flag value is not the
	// separator, and tool-side tokens must never influence help ownership.
	for i := 0; i < len(args)-flags.NArg(); i++ {
		if args[i] == "--" {
			opts.helpServer = true
			break
		}
		name, _, assigned := strings.Cut(strings.TrimLeft(args[i], "-"), "=")
		if !assigned {
			f := flags.Lookup(name)
			if boolean, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !boolean.IsBoolFlag() {
				i++
			}
		}
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
		if opts.jsonSet || opts.stdin {
			return request{}, invocationError("input_with_projected_arguments", "--json and --stdin cannot be combined with projected tool arguments", "choose either exact JSON input or projected arguments")
		}
		suffix := positionals[2:]
		if len(suffix) != 0 && suffix[len(suffix)-1] == "-h" {
			suffix = append([]string(nil), suffix...)
			suffix[len(suffix)-1] = "--help"
		}
		projected, overlay, err := parseProjectedSuffix(suffix)
		if err != nil {
			return request{}, err
		}
		if positionals[1] == "" {
			return request{}, invocationError("tool_required", "tool name must not be empty", "supply a non-empty tool name")
		}
		return request{operation: callTool, server: positionals[0], tool: positionals[1], arguments: map[string]any{}, projected: projected, overlay: overlay}, nil
	}
}

func parseHelpRequest(positionals []string, opts options) (request, *appError) {
	if opts.jsonSet || opts.stdin {
		return request{}, invocationError("input_with_help", "--json and --stdin cannot be used with --help", "request help without a tool input mode")
	}
	switch len(positionals) {
	case 0:
		return request{help: globalHelp}, nil
	case 1:
		return request{operation: listTools, server: positionals[0], help: serverHelp}, nil
	case 2:
		if positionals[1] == "" {
			return request{}, invocationError("tool_required", "tool name must not be empty", "supply a non-empty tool name")
		}
		return request{operation: inspectTool, server: positionals[0], tool: positionals[1], help: toolHelp}, nil
	default:
		return request{}, invocationError("help_arity", "focused help accepts at most <server> <tool>", "use wirecmd --help [<server> [<tool>]]")
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
		transport := "stdio"
		if server.HTTP != nil {
			transport = "http"
		}
		servers = append(servers, serverSummary{Name: server.Name, Scope: string(server.Scope), Transport: transport})
	}
	return serversEnvelope{OK: true, Servers: servers}
}

// connectionTarget is the small transport-neutral execution seam. It keeps
// the CLI and daemon independent from MCP transport construction without
// introducing a general adapter layer.
type connectionTarget struct {
	command    *exec.Cmd
	endpoint   string
	httpClient *http.Client
	oauth      mcpauth.OAuthHandler
	oauthRun   *oauthRuntime
	authServer string
}

func (t connectionTarget) requiresToolPriming() bool { return t.endpoint != "" }

func makeTarget(server config.Server, root *config.Root, callerCWD string, lookup func(string) (string, bool)) (connectionTarget, []string, *appError) {
	if server.HTTP != nil {
		return makeHTTPTarget(*server.HTTP, lookup)
	}
	command, secrets, appErr := makeCommand(server, root, callerCWD, lookup)
	if appErr != nil {
		return connectionTarget{}, nil, appErr
	}
	return connectionTarget{command: command}, secrets, nil
}

func makeHTTPTarget(transport config.HTTP, lookup func(string) (string, bool)) (connectionTarget, []string, *appError) {
	endpoint, err := url.Parse(transport.Endpoint)
	if err != nil {
		return connectionTarget{}, nil, configurationError("invalid_http_endpoint", err.Error(), "correct the configured HTTP endpoint")
	}
	secrets := make([]string, 0, len(transport.Query)+len(transport.Headers))
	resolve := func(value config.Value) (config.ResolvedValue, *appError) {
		resolved, err := value.ResolveEnv(lookup)
		if err != nil {
			return config.ResolvedValue{}, configurationError("secret_not_available", err.Error(), "set the required environment variable before invoking Wirecmd")
		}
		if !utf8.ValidString(resolved.Text) {
			return config.ResolvedValue{}, configurationError("invalid_http_value", "HTTP query and header values must be valid UTF-8", "correct the configured or resolved HTTP value")
		}
		if resolved.Sensitive && resolved.Text != "" {
			secrets = append(secrets, resolved.Text)
		}
		return resolved, nil
	}
	query := endpoint.Query()
	for _, field := range transport.Query {
		value, appErr := resolve(field.Value)
		if appErr != nil {
			return connectionTarget{}, nil, appErr
		}
		query.Set(field.Name, value.Text)
	}
	endpoint.RawQuery = query.Encode()

	headers := make(http.Header, len(transport.Headers))
	for _, field := range transport.Headers {
		value, appErr := resolve(field.Value)
		if appErr != nil {
			return connectionTarget{}, nil, appErr
		}
		if strings.ContainsAny(value.Text, "\r\n") {
			return connectionTarget{}, nil, configurationError("invalid_http_value", fmt.Sprintf("header %q contains a line break", field.Name), "correct the configured or resolved HTTP header value")
		}
		headers.Set(field.Name, value.Text)
	}
	client := &http.Client{Transport: &configuredHeaderTransport{base: http.DefaultTransport, headers: headers, scheme: endpoint.Scheme, host: endpoint.Host}}
	return connectionTarget{endpoint: endpoint.String(), httpClient: client}, secrets, nil
}

func attachOAuth(target connectionTarget, serverName string, transport config.HTTP, lookup func(string) (string, bool), interactive, force bool, emitURL func(string)) (connectionTarget, []string, *appError) {
	for _, header := range transport.Headers {
		if strings.EqualFold(header.Name, "Authorization") {
			return target, nil, nil
		}
	}
	clientSecret, secrets, appErr := resolveOAuthClientSecret(transport, lookup)
	if appErr != nil {
		return connectionTarget{}, nil, appErr
	}
	runtime, appErr := newOAuthRuntime(transport, target.endpoint, clientSecret, interactive, force, emitURL)
	if appErr != nil {
		return connectionTarget{}, nil, appErr
	}
	handler, appErr := runtime.Handler(transport, clientSecret)
	if appErr != nil {
		runtime.Close()
		return connectionTarget{}, nil, appErr
	}
	target.oauth, target.oauthRun, target.authServer = handler, runtime, serverName
	return target, secrets, nil
}

type configuredHeaderTransport struct {
	base    http.RoundTripper
	headers http.Header
	scheme  string
	host    string
}

func (t *configuredHeaderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if !strings.EqualFold(request.URL.Scheme, t.scheme) || !strings.EqualFold(request.URL.Host, t.host) {
		return t.base.RoundTrip(request)
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	for name, values := range t.headers {
		clone.Header.Del(name)
		for _, value := range values {
			clone.Header.Add(name, value)
		}
	}
	return t.base.RoundTrip(clone)
}

func makeCommand(server config.Server, root *config.Root, callerCWD string, lookup func(string) (string, bool)) (*exec.Cmd, []string, *appError) {
	return makeStdioCommand(server.Stdio, root, callerCWD, lookup)
}

func makeStdioCommand(stdio config.Stdio, root *config.Root, callerCWD string, lookup func(string) (string, bool)) (*exec.Cmd, []string, *appError) {
	arguments := make([]string, 0, len(stdio.Args))
	secrets := make([]string, 0, len(stdio.Env)+len(stdio.Args))
	for _, arg := range stdio.Args {
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
	for _, assignment := range stdio.Env {
		value, err := assignment.Value.ResolveEnv(lookup)
		if err != nil {
			return nil, nil, configurationError("secret_not_available", err.Error(), "set the required environment variable before invoking Wirecmd")
		}
		env = replaceEnvironment(env, assignment.Name, value.Text)
		if value.Sensitive && value.Text != "" {
			secrets = append(secrets, value.Text)
		}
	}
	command := exec.Command(stdio.Command, arguments...)
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

func directTools(ctx context.Context, target connectionTarget, redactor *redactor) ([]toolSummary, *appError) {
	catalog, appErr := directToolCatalog(ctx, target, redactor)
	return catalog.summaries, appErr
}

type discoveredToolCatalog struct {
	summaries []toolSummary
}

func directToolCatalog(ctx context.Context, target connectionTarget, redactor *redactor) (discoveredToolCatalog, *appError) {
	session, err := connectTarget(ctx, target, redactor)
	if err != nil {
		return discoveredToolCatalog{}, err
	}
	defer session.Close()
	return sessionToolCatalog(ctx, session, redactor)
}

func sessionTools(ctx context.Context, session *mcp.ClientSession, redactor *redactor) ([]toolSummary, *appError) {
	catalog, appErr := sessionToolCatalog(ctx, session, redactor)
	return catalog.summaries, appErr
}

func sessionToolCatalog(ctx context.Context, session *mcp.ClientSession, redactor *redactor) (discoveredToolCatalog, *appError) {
	catalog := discoveredToolCatalog{}
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return discoveredToolCatalog{}, mcpOperationError(err, "tool_list_failed")
		}
		summary := toolSummary{Name: redactor.Redact(tool.Name), Title: redactor.Redact(tool.Title), Description: redactor.Redact(tool.Description)}
		catalog.summaries = append(catalog.summaries, summary)
	}
	sort.Slice(catalog.summaries, func(i, j int) bool { return catalog.summaries[i].Name < catalog.summaries[j].Name })
	return catalog, nil
}

func directCall(ctx context.Context, target connectionTarget, tool string, arguments map[string]any, redactor *redactor) (toolResult, *appError) {
	session, err := connectTarget(ctx, target, redactor)
	if err != nil {
		return toolResult{}, err
	}
	defer session.Close()
	if target.requiresToolPriming() {
		_, appErr := primeSessionTools(ctx, session, redactor)
		if appErr != nil {
			return toolResult{}, appErr
		}
	}
	result, appErr := sessionCall(ctx, session, tool, arguments, redactor)
	return result, appErr
}

// primeSessionTools lets the SDK populate its private schema cache before an
// HTTP call. The SDK uses that cache for transport behavior such as
// x-mcp-header; Wirecmd deliberately does not reproduce that logic.
func primeSessionTools(ctx context.Context, session *mcp.ClientSession, redactor *redactor) (discoveredToolCatalog, *appError) {
	return sessionToolCatalog(ctx, session, redactor)
}

func sessionCall(ctx context.Context, session *mcp.ClientSession, tool string, arguments map[string]any, redactor *redactor) (toolResult, *appError) {
	response, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: arguments})
	if callErr != nil {
		return toolResult{}, mcpOperationError(callErr, "tool_call_failed")
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

func isTransportFailure(err error) bool {
	if errors.Is(err, mcp.ErrConnectionClosed) {
		return true
	}
	var networkError net.Error
	return errors.As(err, &networkError)
}

// mcpOperationError retains the raw SDK cancellation signal privately. The
// daemon needs it to distinguish a canceled MCP request, whose HTTP session
// may be poisoned by the SDK's cancellation notification, from a local error
// produced after a successful SDK operation. It is never serialized.
func mcpOperationError(err error, protocolCode string) *appError {
	sdkCanceled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	var appErr *appError
	if sdkCanceled {
		appErr = transportError("operation_canceled", err.Error(), "retry if the cancellation was unintended")
	} else if isTransportFailure(err) {
		appErr = transportError("connection_closed", err.Error(), "check the upstream MCP server diagnostics")
	} else {
		appErr = protocolError(protocolCode, err.Error(), "check the upstream MCP server diagnostics")
	}
	appErr.sdkCanceled = sdkCanceled
	return appErr
}

func connectTarget(ctx context.Context, target connectionTarget, redactor *redactor) (*mcp.ClientSession, *appError) {
	if target.command != nil {
		return connectCommand(ctx, target.command, redactor)
	}
	if target.endpoint != "" {
		transport := &mcp.StreamableClientTransport{Endpoint: target.endpoint, HTTPClient: target.httpClient, OAuthHandler: target.oauth, DisableStandaloneSSE: true}
		session, appErr := connectSession(ctx, transport)
		if appErr != nil && appErr.code == "authorization_required" && target.authServer != "" {
			appErr.action = authAction(target.authServer)
		}
		if appErr != nil && target.oauth != nil && appErr.code == "mcp_connect_failed" {
			lower := strings.ToLower(appErr.message)
			switch {
			case strings.Contains(lower, "token exchange failed"), strings.Contains(lower, "authorization provider returned"), strings.Contains(lower, "invalid_grant"):
				appErr = authenticationError("authorization_failed", appErr.message, authAction(target.authServer))
			case strings.Contains(lower, "protected resource metadata"), strings.Contains(lower, "authorization server metadata"), strings.Contains(lower, "failed to register client"), strings.Contains(lower, "issuer"), strings.Contains(lower, "state mismatch"), strings.Contains(lower, "authorization response"):
				appErr = protocolError("oauth_protocol_failed", appErr.message, "check the upstream OAuth metadata and client registration")
			}
		}
		return session, appErr
	}
	return nil, transportError("mcp_connect_failed", "server has no configured transport", "correct the server transport configuration")
}

func connectCommand(ctx context.Context, command *exec.Cmd, redactor *redactor) (*mcp.ClientSession, *appError) {
	command.Stderr = redactor
	return connectSession(ctx, &mcp.CommandTransport{Command: command})
}

// connect remains a narrow compatibility helper for the stdio-specific
// connection cleanup test. Production callers use connectTarget.
func connect(ctx context.Context, command *exec.Cmd, redactor *redactor) (*mcp.ClientSession, *appError) {
	return connectCommand(ctx, command, redactor)
}

func connectSession(ctx context.Context, transport mcp.Transport) (*mcp.ClientSession, *appError) {
	tracked := &trackedTransport{transport: transport}
	client := mcp.NewClient(&mcp.Implementation{Name: "wirecmd", Version: buildinfo.Version()}, &mcp.ClientOptions{
		MultiRoundTrip: &mcp.MultiRoundTripOptions{Disabled: true},
	})
	session, err := client.Connect(ctx, tracked, nil)
	if err != nil {
		if tracked.connection != nil {
			_ = tracked.connection.Close()
		}
		if errors.Is(err, errAuthorizationRequired) {
			return nil, authenticationError("authorization_required", "the upstream MCP server requires authorization", "run wirecmd auth login for the selected server")
		}
		if errors.Is(err, errAuthorizationInProgress) {
			return nil, userActionError("authorization_in_progress", "another request is completing OAuth authorization for this credential", "retry after the current authorization finishes")
		}
		if errors.Is(err, oauthstore.ErrUnavailable) || errors.Is(err, oauthstore.ErrUnsafeState) || errors.Is(err, oauthstore.ErrCorrupt) || errors.Is(err, oauthstore.ErrStale) {
			return nil, oauthStoreError(err)
		}
		return nil, transportError("mcp_connect_failed", err.Error(), "check the configured transport and upstream diagnostics")
	}
	return session, nil
}

// trackedTransport retains an SDK-created connection until Client.Connect
// returns a session. In v1.7.0 an initialization failure may return without a
// session for the caller to close, regardless of transport. Closing the saved
// connection preserves the transport's normal child-process or HTTP-session
// shutdown behavior.
type trackedTransport struct {
	transport  mcp.Transport
	connection mcp.Connection
}

func (t *trackedTransport) Connect(ctx context.Context) (mcp.Connection, error) {
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
	category    string
	code        string
	message     string
	action      string
	exitCode    int
	result      *toolResult
	details     any
	sdkCanceled bool
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

func userActionError(code, message, action string) *appError {
	return &appError{category: "user_action", code: code, message: message, action: action, exitCode: exitUserAction}
}

func daemonUnavailable() *appError {
	return &appError{category: "transport", code: "daemon_unavailable", message: "the Wirecmd daemon is not running", action: "start it with wirecmd daemon run or use --direct deliberately", exitCode: exitTransport}
}

type errorBody struct {
	Category string `json:"category"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Action   string `json:"action"`
	Details  any    `json:"details,omitempty"`
}

type failure struct {
	OK     bool        `json:"ok"`
	Error  errorBody   `json:"error"`
	Result *toolResult `json:"result,omitempty"`
}

func failureEnvelope(appErr *appError) failure {
	return failure{OK: false, Error: errorBody{Category: appErr.category, Code: appErr.code, Message: appErr.message, Action: appErr.action, Details: appErr.details}, Result: appErr.result}
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
	secrets       []string
	endpoint      string
	endpointQuery string
	safeEndpoint  string
	pending       string
	writer        io.Writer
}

func newRedactor(values []string, writer io.Writer) *redactor {
	redactor := &redactor{writer: writer}
	redactor.ProtectSecrets(values...)
	return redactor
}

func (r *redactor) Redact(value string) string {
	if r.endpoint != "" {
		value = strings.ReplaceAll(value, r.endpoint, r.safeEndpoint)
		value = strings.ReplaceAll(value, r.endpointQuery, "[REDACTED]")
	}
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	return value
}

func (r *redactor) ProtectSecrets(values ...string) {
	seen := make(map[string]struct{}, len(r.secrets)+len(values))
	for _, value := range r.secrets {
		seen[value] = struct{}{}
	}
	for _, value := range values {
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		r.secrets = append(r.secrets, value)
		variants := []string{value}
		for depth := 0; depth < 2; depth++ {
			current := append([]string(nil), variants...)
			for _, item := range current {
				variants = append(variants, url.QueryEscape(item), url.PathEscape(item))
			}
		}
		for _, encoded := range variants[1:] {
			if encoded == value || encoded == "" {
				continue
			}
			if _, ok := seen[encoded]; ok {
				continue
			}
			seen[encoded] = struct{}{}
			r.secrets = append(r.secrets, encoded)
		}
	}
	sort.Slice(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

// ProtectEndpoint makes literal query strings diagnostic-only: the endpoint
// path stays useful, but no raw query or query value can escape in SDK errors.
// Query entries are not configuration secrets in this slice; this is solely a
// boundary redaction rule until typed header/query values are introduced.
func (r *redactor) ProtectEndpoint(endpoint string) {
	if endpoint == "" {
		return
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.RawQuery == "" {
		return
	}
	bare := *parsed
	bare.RawQuery = ""
	bare.ForceQuery = false
	r.endpoint = endpoint
	r.endpointQuery = parsed.RawQuery
	r.safeEndpoint = bare.String() + "?[REDACTED]"
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
