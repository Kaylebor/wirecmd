// Package cli implements Wirecmd's first machine-composable command surface.
package cli

import (
	"bytes"
	"context"
	"encoding/base64"
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
	uritemplate "github.com/yosida95/uritemplate/v3"
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

const usage = "wirecmd [--config PATH] [--direct] [--format auto|json|pretty] [--color auto|always|never] [--json OBJECT|--stdin] mcp [<server> [tool <tool>|resources|resource-templates|resource URI|<exact-call-object>]]"

var isInteractiveTerminal = terminalIO
var openAuthorizationURL = openBrowserURL

type options struct {
	configs             []string
	direct              bool
	json                string
	jsonSet             bool
	stdin               bool
	help                bool
	version             bool
	format              outputFormat
	color               colorMode
	formatSet           bool
	colorSet            bool
	legacyHelpSeparator bool
	mcpServerEscaped    bool
	completionServers   bool
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
	uri       string
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
	listResources
	listResourceTemplates
	readResource
	navigateLSP
	inspectLSP
	statusLSP
)

type versionText string

func run(ctx context.Context, opts options, positionals []string, parseErr error, in io.Reader, errOut io.Writer) (any, *appError) {
	if parseErr != nil {
		return nil, invocationError("invalid_flags", parseErr.Error(), "place Wirecmd flags before the server and tool names")
	}
	var escapeErr *appError
	positionals, opts, escapeErr = normalizeMCPServerEscape(positionals, opts)
	if escapeErr != nil {
		return nil, escapeErr
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
	if opts.help && (opts.jsonSet || opts.stdin) {
		return nil, invocationError("input_with_help", "--json and --stdin cannot be used with --help", "request help without a tool input mode")
	}
	if opts.help && opts.legacyHelpSeparator {
		if len(positionals) == 0 {
			return nil, namespaceRequired(positionals, opts)
		}
		return nil, namespaceHelpRequired(positionals)
	}
	// The empty form is deliberately static. It must not discover configuration,
	// connect to the daemon, or initialize any upstream capability.
	if len(positionals) == 0 && !opts.jsonSet && !opts.stdin && !opts.direct && len(opts.configs) == 0 {
		return helpText(globalHelpText()), nil
	}
	if opts.help {
		if len(positionals) == 1 && positionals[0] == "mcp" {
			return helpText(mcpHelpText()), nil
		}
		if text, ok := mcpPrimitiveHelp(positionals); ok {
			return helpText(text), nil
		}
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
	if !opts.help && len(positionals) == 1 && positionals[0] == "lsp" && !opts.jsonSet && !opts.stdin {
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
	if req.operation == listResources {
		resources, runErr := directResources(ctx, target, redactor)
		if runErr != nil {
			return nil, runErr.redacted(redactor)
		}
		return resourcesEnvelope{OK: true, Server: server.Name, Resources: resources}, nil
	}
	if req.operation == listResourceTemplates {
		templates, runErr := directResourceTemplates(ctx, target, redactor)
		if runErr != nil {
			return nil, runErr.redacted(redactor)
		}
		return resourceTemplatesEnvelope{OK: true, Server: server.Name, ResourceTemplates: templates}, nil
	}
	if req.operation == readResource {
		contents, runErr := directResource(ctx, target, req.uri, redactor)
		if runErr != nil {
			return nil, runErr.redacted(redactor)
		}
		return resourceEnvelope{OK: true, Server: server.Name, URI: redactedResourceIdentifier(req.uri, redactor, false), Contents: contents}, nil
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

// normalizeMCPServerEscape gives help-shaped aliases an unambiguous server
// position without changing tool-side -- ownership. It is deliberately
// namespace-local: the removed top-level `--help -- SERVER` grammar remains
// invalid.
func normalizeMCPServerEscape(positionals []string, opts options) ([]string, options, *appError) {
	if len(positionals) < 3 || positionals[0] != "mcp" || positionals[1] != "--" {
		return positionals, opts, nil
	}
	if positionals[2] != "--help" && positionals[2] != "-h" {
		return positionals, opts, nil
	}
	canonical := make([]string, 0, len(positionals)-1)
	canonical = append(canonical, positionals[0])
	canonical = append(canonical, positionals[2:]...)
	updated := opts
	updated.mcpServerEscaped = true
	return canonical, updated, nil
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
	// A -- consumed before positional parsing used to make --help select an
	// MCP collision escape. The v0.2 namespace removes that grammar. Tool-side
	// -- remains untouched because flag parsing stops at the first positional.
	for index := 0; index < len(args)-flags.NArg(); index++ {
		if args[index] == "--" {
			opts.legacyHelpSeparator = true
			break
		}
		name, _, assigned := strings.Cut(strings.TrimLeft(args[index], "-"), "=")
		if assigned {
			continue
		}
		flag := flags.Lookup(name)
		if flag == nil {
			continue
		}
		boolean, isBoolean := flag.Value.(interface{ IsBoolFlag() bool })
		if !isBoolean || !boolean.IsBoolFlag() {
			index++
		}
	}
	return opts, flags.Args(), nil
}

func parseRequest(positionals []string, opts options, in io.Reader) (request, *appError) {
	if len(positionals) != 0 && positionals[0] == "mcp" {
		return parseMCPRequest(positionals, opts, in)
	}
	if len(positionals) == 0 {
		if opts.jsonSet || opts.stdin {
			return request{}, invocationError("input_without_call", "JSON input is only valid when calling a tool", "supply mcp <server> tool <tool> before JSON input flags")
		}
		return request{}, namespaceRequired(nil, opts)
	}
	return request{}, namespaceRequired(positionals, opts)
}

// parseMCPRequest owns the public MCP namespace. Keeping the grammar here
// makes tool names unambiguous as MCP primitives are added beside `tool`.
func parseMCPRequest(positionals []string, opts options, in io.Reader) (request, *appError) {
	if opts.jsonSet && opts.stdin {
		return request{}, invocationError("input_mode_conflict", "--json and --stdin cannot be used together", "choose exactly one JSON input mode")
	}
	switch len(positionals) {
	case 1:
		if opts.jsonSet || opts.stdin {
			return request{}, invocationError("input_without_call", "JSON input is only valid when calling a tool", "supply mcp <server> tool <tool> before JSON input flags")
		}
		return request{operation: listServers}, nil
	case 2:
		if opts.jsonSet || opts.stdin {
			return request{}, invocationError("input_without_call", "JSON input is only valid when calling a tool", "supply tool <tool> after the MCP server")
		}
		return request{operation: listTools, server: positionals[1]}, nil
	case 3:
		if !opts.jsonSet && !opts.stdin && startsJSONObject(positionals[2]) {
			tool, arguments, err := parseExactCall(positionals[2])
			if err != nil {
				return request{}, err
			}
			return request{operation: callTool, server: positionals[1], tool: tool, arguments: arguments}, nil
		}
		switch positionals[2] {
		case "resources":
			if opts.jsonSet || opts.stdin {
				return request{}, invocationError("input_without_call", "JSON input is only valid when calling a tool", "supply tool <tool> after the MCP server")
			}
			return request{operation: listResources, server: positionals[1]}, nil
		case "resource-templates":
			if opts.jsonSet || opts.stdin {
				return request{}, invocationError("input_without_call", "JSON input is only valid when calling a tool", "supply tool <tool> after the MCP server")
			}
			return request{operation: listResourceTemplates, server: positionals[1]}, nil
		case "resource":
			return request{}, invocationError("resource_uri_required", "reading a resource requires one non-empty URI", "use wirecmd mcp "+mcpServerSpelling(positionals[1])+" resource URI")
		case "tool":
			return request{}, invocationError("tool_required", "MCP tool calls require a non-empty tool name", "use wirecmd mcp "+mcpServerSpelling(positionals[1])+" tool <tool>")
		default:
			return request{}, invocationError("mcp_operation_required", "MCP server operations must name tool, resources, or another MCP primitive", "use wirecmd mcp "+mcpServerSpelling(positionals[1])+" tool <tool>")
		}
	default:
		switch positionals[2] {
		case "resources":
			return request{}, invocationError("resource_list_arity", "resource listing does not accept additional arguments", "use wirecmd mcp "+mcpServerSpelling(positionals[1])+" resources")
		case "resource-templates":
			return request{}, invocationError("resource_template_list_arity", "resource template listing does not accept additional arguments", "use wirecmd mcp "+mcpServerSpelling(positionals[1])+" resource-templates")
		case "resource":
			if len(positionals) != 4 || positionals[3] == "" {
				return request{}, invocationError("resource_uri_required", "reading a resource requires one non-empty URI", "use wirecmd mcp "+mcpServerSpelling(positionals[1])+" resource URI")
			}
			if opts.jsonSet || opts.stdin {
				return request{}, invocationError("input_without_call", "JSON input is only valid when calling a tool", "supply tool <tool> after the MCP server")
			}
			if !validResourceURI(positionals[3]) {
				return request{}, invocationError("resource_uri_invalid", "resource reads require a valid absolute URI", "supply a URI with a non-empty scheme")
			}
			return request{operation: readResource, server: positionals[1], uri: positionals[3]}, nil
		case "tool":
		default:
			return request{}, invocationError("mcp_operation_required", "MCP server operations must name tool, resources, or another MCP primitive", "use wirecmd mcp "+mcpServerSpelling(positionals[1])+" tool <tool>")
		}
		if positionals[3] == "" {
			return request{}, invocationError("tool_required", "tool name must not be empty", "supply a non-empty tool name")
		}
		return parseMCPToolCall(positionals[1], positionals[3], positionals[4:], opts, in)
	}
}

func validResourceURI(raw string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && parsed.Scheme != "" && validResourceURICharacters(raw) && validResourceBracketPlacement(raw)
}

func validResourceURICharacters(raw string) bool {
	for index := 0; index < len(raw); index++ {
		character := raw[index]
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
		case strings.ContainsRune("-._~:/?#@!$&'()*+,;=", rune(character)):
		case character == '[' || character == ']':
		case character == '%' && index+2 < len(raw) && isHex(raw[index+1]) && isHex(raw[index+2]):
			index += 2
		default:
			return false
		}
	}
	return strings.Count(raw, "#") <= 1
}

func validResourceBracketPlacement(raw string) bool {
	if !strings.ContainsAny(raw, "[]") {
		return true
	}
	scheme := strings.IndexByte(raw, ':')
	if scheme < 0 || !strings.HasPrefix(raw[scheme+1:], "//") {
		return false
	}
	authorityStart := scheme + 3
	authorityEnd := len(raw)
	if end := strings.IndexAny(raw[authorityStart:], "/?#"); end >= 0 {
		authorityEnd = authorityStart + end
	}
	if strings.ContainsAny(raw[:authorityStart], "[]") || strings.ContainsAny(raw[authorityEnd:], "[]") {
		return false
	}
	authority := raw[authorityStart:authorityEnd]
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		if strings.ContainsAny(authority[:at], "[]") {
			return false
		}
		authority = authority[at+1:]
	}
	open, close := strings.IndexByte(authority, '['), strings.IndexByte(authority, ']')
	if open != 0 || close <= open || strings.Count(authority, "[") != 1 || strings.Count(authority, "]") != 1 {
		return false
	}
	return close == len(authority)-1 || authority[close+1] == ':'
}

func parseMCPToolCall(server, tool string, suffix []string, opts options, in io.Reader) (request, *appError) {
	if opts.jsonSet || opts.stdin {
		if len(suffix) != 0 {
			return request{}, invocationError("input_with_projected_arguments", "--json and --stdin cannot be combined with projected tool arguments", "choose either exact JSON input or projected arguments")
		}
		arguments, err := parseArguments(opts, in)
		if err != nil {
			return request{}, err
		}
		return request{operation: callTool, server: server, tool: tool, arguments: arguments}, nil
	}
	if len(suffix) == 0 {
		return request{operation: callTool, server: server, tool: tool, arguments: map[string]any{}}, nil
	}
	if suffix[len(suffix)-1] == "-h" {
		suffix = append([]string(nil), suffix...)
		suffix[len(suffix)-1] = "--help"
	}
	projected, overlay, err := parseProjectedSuffix(suffix)
	if err != nil {
		return request{}, err
	}
	return request{operation: callTool, server: server, tool: tool, arguments: map[string]any{}, projected: projected, overlay: overlay}, nil
}

func parseHelpRequest(positionals []string, opts options) (request, *appError) {
	if opts.jsonSet || opts.stdin {
		return request{}, invocationError("input_with_help", "--json and --stdin cannot be used with --help", "request help without a tool input mode")
	}
	if len(positionals) != 0 && positionals[0] == "mcp" {
		switch len(positionals) {
		case 1:
			return request{help: globalHelp}, nil
		case 2:
			return request{operation: listTools, server: positionals[1], help: serverHelp}, nil
		case 4:
			if positionals[2] != "tool" || positionals[3] == "" {
				return request{}, invocationError("mcp_help_arity", "focused MCP help accepts mcp <server> [tool <tool>]", "use wirecmd --help mcp <server> [tool <tool>]")
			}
			return request{operation: inspectTool, server: positionals[1], tool: positionals[3], help: toolHelp}, nil
		default:
			return request{}, invocationError("mcp_help_arity", "focused MCP help accepts mcp <server> [tool <tool>]", "use wirecmd --help mcp <server> [tool <tool>]")
		}
	}
	switch len(positionals) {
	case 0:
		return request{help: globalHelp}, nil
	default:
		return request{}, namespaceHelpRequired(positionals)
	}
}

func mcpPrimitiveHelp(positionals []string) (string, bool) {
	if len(positionals) < 3 || positionals[0] != "mcp" || positionals[1] == "" {
		return "", false
	}
	switch positionals[2] {
	case "resources":
		if len(positionals) == 3 {
			return "Usage:\n  wirecmd [client flags] mcp " + mcpServerSpelling(positionals[1]) + " resources\n\nLists resources advertised by the selected MCP server. The SDK follows pagination; Wirecmd sorts the resulting list by URI then name.\n", true
		}
	case "resource-templates":
		if len(positionals) == 3 {
			return "Usage:\n  wirecmd [client flags] mcp " + mcpServerSpelling(positionals[1]) + " resource-templates\n\nLists resource templates advertised by the selected MCP server. The SDK follows pagination; Wirecmd sorts the resulting list by URI template then name.\n", true
		}
	case "resource":
		if len(positionals) == 3 || len(positionals) == 4 {
			return "Usage:\n  wirecmd [client flags] mcp " + mcpServerSpelling(positionals[1]) + " resource URI\n\nReads one MCP resource. Text content is returned directly; binary content is returned as base64 in upstream order.\n", true
		}
	}
	return "", false
}

func namespaceHelpRequired(positionals []string) *appError {
	server := mcpServerSpelling(positionals[0])
	if len(positionals) == 1 || startsJSONObject(positionals[1]) {
		return invocationError("mcp_namespace_required", "top-level server help was removed", "use wirecmd --help mcp "+server)
	}
	return invocationError("mcp_namespace_required", "top-level server help was removed", "use wirecmd --help mcp "+server+" tool "+shellQuote(positionals[1]))
}

func namespaceRequired(positionals []string, opts options) *appError {
	if len(positionals) == 0 {
		return invocationError("mcp_namespace_required", "MCP operations are under the mcp namespace", "use wirecmd mcp to list configured MCP servers")
	}
	server := mcpServerSpelling(positionals[0])
	if len(positionals) == 1 {
		return invocationError("mcp_namespace_required", "top-level server dispatch was removed", "use wirecmd mcp "+server+" to list that server's tools")
	}
	tool := shellQuote(positionals[1])
	if startsJSONObject(positionals[1]) {
		// Explicit input modes make this a literal tool name, but echoing an
		// arbitrary JSON-looking name could disclose exact-call-shaped input.
		tool = "'<TOOL_NAME>'"
	}
	if opts.jsonSet {
		return invocationError("mcp_namespace_required", "top-level server dispatch was removed", "rerun with the original JSON object: wirecmd --json '<JSON_OBJECT>' mcp "+server+" tool "+tool)
	}
	if opts.stdin {
		return invocationError("mcp_namespace_required", "top-level server dispatch was removed", "rerun with the original JSON object: printf '%s\\n' '<JSON_OBJECT>' | wirecmd --stdin mcp "+server+" tool "+tool)
	}
	last := positionals[len(positionals)-1]
	if last == "--help" || last == "-h" {
		return invocationError("mcp_namespace_required", "top-level server dispatch was removed", "use wirecmd --help mcp "+server+" tool "+tool)
	}
	if startsJSONObject(positionals[1]) {
		return invocationError("mcp_namespace_required", "top-level server dispatch was removed", "use wirecmd mcp "+server+" '<exact-call-object>'")
	}
	if len(positionals) > 2 {
		return invocationError("mcp_namespace_required", "top-level server dispatch was removed", "migrate the original tool arguments manually: wirecmd mcp "+server+" tool "+tool+" <original-tool-arguments>")
	}
	return invocationError("mcp_namespace_required", "top-level server dispatch was removed", "use wirecmd mcp "+server+" tool "+tool)
}

func mcpServerSpelling(server string) string {
	quoted := shellQuote(singleLine(server))
	if server == "--help" || server == "-h" {
		return "-- " + quoted
	}
	return quoted
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

func resourcesAvailable(session *mcp.ClientSession) bool {
	result := session.InitializeResult()
	return result != nil && result.Capabilities != nil && result.Capabilities.Resources != nil
}

func resourceCapabilityError() *appError {
	return protocolError("resource_capability_unavailable", "the upstream MCP server did not advertise resource support", "select a server that supports MCP resources")
}

func resourceOperationError(err error, code string) *appError {
	var corrupt base64.CorruptInputError
	if errors.As(err, &corrupt) {
		return protocolError("resource_blob_invalid", err.Error(), "the upstream MCP server returned malformed base64 resource content")
	}
	return mcpOperationError(err, code)
}

func normalizedResourceURI(raw string, redactor *redactor) (string, *appError) {
	if !validResourceURI(raw) {
		return "", protocolError("resource_uri_unsupported", "the upstream MCP server returned an unsupported resource URI", "check the upstream MCP server diagnostics")
	}
	return redactedResourceIdentifier(raw, redactor, false), nil
}

func normalizedResourceTemplate(raw string, redactor *redactor) (string, *appError) {
	if !validResourceTemplate(raw) {
		return "", protocolError("resource_uri_unsupported", "the upstream MCP server returned an unsupported resource URI template", "check the upstream MCP server diagnostics")
	}
	return redactedResourceIdentifier(raw, redactor, true), nil
}

func validResourceTemplate(raw string) bool {
	if _, err := uritemplate.New(raw); err != nil {
		return false
	}
	plain := replaceResourceTemplateExpressions(raw)
	parsed, err := url.Parse(plain)
	return err == nil && parsed.Scheme != "" && validResourceScheme(parsed.Scheme) && validResourceTemplateURICharacters(plain) && validResourceBracketPlacement(plain)
}

func validResourceScheme(value string) bool {
	for index := 0; index < len(value); index++ {
		character := value[index]
		if index == 0 {
			if !resourceASCIILetter(character) {
				return false
			}
			continue
		}
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '+' || character == '-' || character == '.') {
			return false
		}
	}
	return true
}

func resourceASCIILetter(character byte) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

func validResourceTemplateURICharacters(raw string) bool {
	for index := 0; index < len(raw); index++ {
		character := raw[index]
		if character >= utf8.RuneSelf {
			_, size := utf8.DecodeRuneInString(raw[index:])
			if size == 1 {
				return false
			}
			index += size - 1
			continue
		}
		switch {
		case character >= 'a' && character <= 'z', character >= 'A' && character <= 'Z', character >= '0' && character <= '9':
		case strings.ContainsRune("-._~:/?#@!$&'()*+,;=", rune(character)):
		case character == '[' || character == ']':
		case character == '%' && index+2 < len(raw) && isHex(raw[index+1]) && isHex(raw[index+2]):
			index += 2
		default:
			return false
		}
	}
	return strings.Count(raw, "#") <= 1
}

func replaceResourceTemplateExpressions(raw string) string {
	var value strings.Builder
	for len(raw) != 0 {
		open := strings.IndexByte(raw, '{')
		if open < 0 {
			return value.String() + raw
		}
		value.WriteString(raw[:open])
		close := strings.IndexByte(raw[open:], '}')
		// uritemplate.New validated expression boundaries before this
		// absolute-capability substitution runs.
		close += open
		// A numeric replacement is valid in authority port positions as well as
		// ordinary path, query, and fragment positions. A scheme expression
		// needs a letter to leave an absolute URI for net/url to inspect.
		if value.Len() == 0 && strings.HasPrefix(raw[close+1:], ":") {
			value.WriteByte('x')
		} else {
			value.WriteByte('1')
		}
		raw = raw[close+1:]
	}
	return value.String()
}

func redactedResourceIdentifier(raw string, redactor *redactor, preserveTemplates bool) string {
	value := raw
	fragment := ""
	if index := resourceFragmentStart(value, preserveTemplates); index >= 0 {
		if value[index] == '{' {
			value = value[:index] + redactedResourceTemplateFragment(value[index:])
		} else if preserveTemplates {
			fragment = "#" + redactedResourceTemplateFragment(value[index+1:])
			value = value[:index]
		} else {
			value = value[:index]
			fragment = "#[REDACTED]"
		}
	}
	if query := resourceQueryStart(value, preserveTemplates); query >= 0 {
		if value[query] == '{' {
			value = value[:query] + redactedResourceQuery(value[query:], preserveTemplates)
		} else {
			value = value[:query+1] + redactedResourceQuery(value[query+1:], preserveTemplates)
		}
	}
	if scheme := resourceSchemeDelimiter(value, preserveTemplates); scheme >= 0 && strings.HasPrefix(value[scheme+1:], "//") {
		authorityStart := scheme + 3
		authorityEnd := len(value)
		for index, character := range value[authorityStart:] {
			if character == '/' || character == '?' || character == '#' {
				authorityEnd = authorityStart + index
				break
			}
		}
		if at := strings.LastIndexByte(value[authorityStart:authorityEnd], '@'); at >= 0 {
			userinfo := "[REDACTED]"
			if preserveTemplates {
				userinfo = redactedResourceTemplateUserinfo(value[authorityStart : authorityStart+at])
			}
			value = value[:authorityStart] + userinfo + "@" + value[authorityStart+at+1:]
		}
	}
	if preserveTemplates {
		return redactResourceTemplateLiterals(value+fragment, redactor)
	}
	return redactor.Redact(value + fragment)
}

func resourceSchemeDelimiter(value string, preserveTemplates bool) int {
	if !preserveTemplates {
		return strings.IndexByte(value, ':')
	}
	return templateDelimiterIndex(value, ':')
}

func redactResourceTemplateLiterals(value string, redactor *redactor) string {
	var result strings.Builder
	for len(value) != 0 {
		open := strings.IndexByte(value, '{')
		if open < 0 {
			result.WriteString(redactor.Redact(value))
			break
		}
		result.WriteString(redactor.Redact(value[:open]))
		close := strings.IndexByte(value[open:], '}')
		if close < 0 {
			result.WriteString(redactor.Redact(value[open:]))
			break
		}
		close += open
		expression := value[open : close+1]
		if validResourceTemplateExpression(expression) {
			result.WriteString(expression)
		} else {
			result.WriteString(redactor.Redact(expression))
		}
		value = value[close+1:]
	}
	return result.String()
}

func resourceFragmentStart(value string, preserveTemplates bool) int {
	if !preserveTemplates {
		return strings.IndexByte(value, '#')
	}
	for index := 0; index < len(value); index++ {
		if value[index] == '#' {
			return index
		}
		if value[index] != '{' {
			continue
		}
		close := strings.IndexByte(value[index:], '}')
		if close < 0 {
			continue
		}
		close += index
		expression := value[index : close+1]
		if validResourceTemplateExpression(expression) && expression[1] == '#' {
			return index
		}
		index = close
	}
	return -1
}

func resourceQueryStart(value string, preserveTemplates bool) int {
	if !preserveTemplates {
		return strings.IndexByte(value, '?')
	}
	for index := 0; index < len(value); index++ {
		if value[index] == '?' {
			return index
		}
		if value[index] != '{' {
			continue
		}
		close := strings.IndexByte(value[index:], '}')
		if close < 0 {
			continue
		}
		close += index
		expression := value[index : close+1]
		if validResourceTemplateExpression(expression) && expression[1] == '?' {
			return index
		}
		index = close
	}
	return -1
}

func protectResourceIdentifier(raw string, redactor *redactor) {
	protectResourceIdentifierParts(raw, redactor, true)
}

func protectResourceIdentifierParts(raw string, redactor *redactor, protectUserinfo bool) {
	parsed, err := url.Parse(raw)
	if err != nil {
		return
	}
	protected := []string{parsed.RawQuery, parsed.Fragment, parsed.EscapedFragment()}
	protected = append(protected, rawResourceQueryValues(parsed.RawQuery)...)
	if fragment := strings.IndexByte(raw, '#'); fragment >= 0 {
		protected = append(protected, raw[fragment+1:])
	}
	if protectUserinfo && parsed.User != nil {
		protected = append(protected, parsed.User.String(), parsed.User.Username())
		if password, ok := parsed.User.Password(); ok {
			protected = append(protected, password)
		}
	}
	if protectUserinfo {
		protected = append(protected, rawResourceUserinfo(raw)...)
	}
	if query, err := url.ParseQuery(parsed.RawQuery); err == nil {
		for _, values := range query {
			protected = append(protected, values...)
		}
	}
	redactor.ProtectSecrets(protected...)
}

func rawResourceQueryValues(query string) []string {
	var values []string
	for len(query) != 0 {
		end := resourceQuerySeparator(query)
		part := query[:end]
		if equals := strings.IndexByte(part, '='); equals >= 0 {
			values = append(values, part[equals+1:])
		} else {
			values = append(values, part)
		}
		if end == len(query) {
			break
		}
		query = query[end+1:]
	}
	return values
}

func rawResourceUserinfo(raw string) []string {
	scheme := strings.IndexByte(raw, ':')
	if scheme < 0 || !strings.HasPrefix(raw[scheme+1:], "//") {
		return nil
	}
	authority := raw[scheme+3:]
	if end := strings.IndexAny(authority, "/?#"); end >= 0 {
		authority = authority[:end]
	}
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		return nil
	}
	userinfo := authority[:at]
	values := []string{userinfo}
	if colon := strings.IndexByte(userinfo, ':'); colon >= 0 {
		values = append(values, userinfo[:colon], userinfo[colon+1:])
	} else {
		values = append(values, userinfo)
	}
	return values
}

func resourceDiagnosticRedactor(base *redactor, raw string) *redactor {
	copy := clonedResourceRedactor(base)
	protectResourceIdentifier(raw, copy)
	return copy
}

func protectResourceTemplateIdentifier(raw string, redactor *redactor) {
	for _, userinfo := range templateLiteralUserinfo(raw) {
		protectTemplateLiteralRuns(userinfo, redactor)
	}
	if query := resourceQueryStart(raw, true); query >= 0 {
		end := len(raw)
		if fragment := resourceFragmentStart(raw, true); fragment > query {
			end = fragment
		}
		protectTemplateQueryValues(raw[query:end], redactor)
	}
	if fragment := resourceFragmentStart(raw, true); fragment >= 0 {
		if raw[fragment] == '#' {
			fragment++
		}
		protectTemplateLiteralRuns(raw[fragment:], redactor)
	}
}

func templateLiteralUserinfo(raw string) []string {
	scheme := templateDelimiterIndex(raw, ':')
	if scheme < 0 || !strings.HasPrefix(raw[scheme+1:], "//") {
		return nil
	}
	authority := raw[scheme+3:]
	if end := templateAuthorityEnd(authority); end >= 0 {
		authority = authority[:end]
	}
	at := templateDelimiterIndex(authority, '@')
	if at < 0 {
		return nil
	}
	userinfo := authority[:at]
	if colon := templateDelimiterIndex(userinfo, ':'); colon >= 0 {
		return []string{userinfo[:colon], userinfo[colon+1:]}
	}
	return []string{userinfo}
}

func templateAuthorityEnd(authority string) int {
	for index := 0; index < len(authority); index++ {
		switch authority[index] {
		case '/', '?', '#':
			return index
		case '{':
			close := strings.IndexByte(authority[index:], '}')
			if close < 0 {
				continue
			}
			close += index
			expression := authority[index : close+1]
			if validResourceTemplateExpression(expression) {
				if expression[1] == '?' || expression[1] == '#' {
					return index
				}
				index = close
			}
		}
	}
	return -1
}

func clonedResourceRedactor(base *redactor) *redactor {
	return &redactor{
		secrets:       append([]string(nil), base.secrets...),
		endpoint:      base.endpoint,
		endpointQuery: base.endpointQuery,
		safeEndpoint:  base.safeEndpoint,
	}
}

func protectTemplateQueryValues(query string, redactor *redactor) {
	if strings.HasPrefix(query, "?") {
		query = query[1:]
	} else if strings.HasPrefix(query, "{") {
		if close := strings.IndexByte(query, '}'); close >= 0 {
			expression := query[:close+1]
			if validResourceTemplateExpression(expression) && expression[1] == '?' {
				query = query[close+1:]
				if strings.HasPrefix(query, "&") || strings.HasPrefix(query, ";") {
					query = query[1:]
				}
			}
		}
	}
	for len(query) != 0 {
		end := resourceQuerySeparator(query)
		part := query[:end]
		if equals := templateDelimiterIndex(part, '='); equals >= 0 {
			protectTemplateLiteralRuns(part[equals+1:], redactor)
		} else {
			protectTemplateLiteralRuns(part, redactor)
		}
		if end == len(query) {
			return
		}
		query = query[end+1:]
	}
}

func protectTemplateLiteralRuns(value string, redactor *redactor) {
	for len(value) != 0 {
		open := strings.IndexByte(value, '{')
		if open < 0 {
			protectTemplateLiteral(value, redactor)
			return
		}
		protectTemplateLiteral(value[:open], redactor)
		close := strings.IndexByte(value[open:], '}')
		if close < 0 {
			protectTemplateLiteral(value[open:], redactor)
			return
		}
		close += open
		if !validResourceTemplateExpression(value[open : close+1]) {
			protectTemplateLiteral(value[open:close+1], redactor)
		}
		value = value[close+1:]
	}
}

func protectTemplateLiteral(value string, redactor *redactor) {
	if value == "" {
		return
	}
	redactor.ProtectSecrets(value)
	if decoded, err := url.QueryUnescape(value); err == nil && decoded != value {
		redactor.ProtectSecrets(decoded)
	}
	if decoded, err := url.PathUnescape(value); err == nil && decoded != value {
		redactor.ProtectSecrets(decoded)
	}
}

func templateDelimiterIndex(value string, delimiter byte) int {
	for index := 0; index < len(value); index++ {
		if value[index] == delimiter {
			return index
		}
		if value[index] != '{' {
			continue
		}
		close := strings.IndexByte(value[index:], '}')
		if close < 0 {
			continue
		}
		close += index
		if validResourceTemplateExpression(value[index : close+1]) {
			index = close
		}
	}
	return -1
}

func redactedResourceTemplateUserinfo(value string) string {
	if colon := templateDelimiterIndex(value, ':'); colon >= 0 {
		return redactedResourceQueryValue(value[:colon], true) + ":" + redactedResourceQueryValue(value[colon+1:], true)
	}
	return redactedResourceQueryValue(value, true)
}

func redactedResourceQuery(query string, preserveTemplates bool) string {
	var value strings.Builder
	for len(query) != 0 {
		end := resourceQuerySeparator(query)
		part := query[:end]
		if equals := strings.IndexByte(part, '='); equals >= 0 {
			value.WriteString(part[:equals+1])
			value.WriteString(redactedResourceQueryValue(part[equals+1:], preserveTemplates))
		} else {
			value.WriteString(redactedResourceQueryValue(part, preserveTemplates))
		}
		if end == len(query) {
			break
		}
		value.WriteByte(query[end])
		query = query[end+1:]
	}
	return value.String()
}

func resourceQuerySeparator(query string) int {
	depth := 0
	for index := 0; index < len(query); index++ {
		switch query[index] {
		case '{':
			depth++
		case '}':
			if depth > 0 {
				depth--
			}
		case '&', ';':
			if depth == 0 {
				return index
			}
		}
	}
	return len(query)
}

func redactedResourceQueryValue(value string, preserveTemplates bool) string {
	if !preserveTemplates && value != "" {
		return "[REDACTED]"
	}
	var result strings.Builder
	for len(value) != 0 {
		open := strings.IndexByte(value, '{')
		if open < 0 {
			result.WriteString("[REDACTED]")
			break
		}
		if open > 0 {
			result.WriteString("[REDACTED]")
		}
		close := strings.IndexByte(value[open:], '}')
		if close < 0 {
			result.WriteString("[REDACTED]")
			break
		}
		close += open
		expression := value[open : close+1]
		if validResourceTemplateExpression(expression) {
			result.WriteString(expression)
		} else {
			result.WriteString("[REDACTED]")
		}
		value = value[close+1:]
	}
	return result.String()
}

func redactedResourceTemplateFragment(value string) string {
	var result strings.Builder
	for len(value) != 0 {
		open := strings.IndexByte(value, '{')
		if open < 0 {
			result.WriteString("[REDACTED]")
			break
		}
		if open > 0 {
			result.WriteString("[REDACTED]")
		}
		close := strings.IndexByte(value[open:], '}')
		if close < 0 {
			result.WriteString("[REDACTED]")
			break
		}
		close += open
		expression := value[open : close+1]
		if validResourceTemplateExpression(expression) {
			result.WriteString(expression)
		} else {
			result.WriteString("[REDACTED]")
		}
		value = value[close+1:]
	}
	return result.String()
}

func validResourceTemplateExpression(expression string) bool {
	_, err := uritemplate.New(expression)
	return err == nil
}

func isHex(value byte) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f' || value >= 'A' && value <= 'F'
}

func directResources(ctx context.Context, target connectionTarget, redactor *redactor) ([]resourceSummary, *appError) {
	session, appErr := connectTarget(ctx, target, redactor)
	if appErr != nil {
		return nil, appErr
	}
	defer session.Close()
	return sessionResources(ctx, session, redactor)
}

func sessionResources(ctx context.Context, session *mcp.ClientSession, redactor *redactor) ([]resourceSummary, *appError) {
	if !resourcesAvailable(session) {
		return nil, resourceCapabilityError()
	}
	type resourceEntry struct {
		rawURI   string
		rawName  string
		resource *mcp.Resource
		summary  resourceSummary
	}
	entries := make([]resourceEntry, 0)
	listingRedactor := clonedResourceRedactor(redactor)
	for resource, err := range session.Resources(ctx, nil) {
		if err != nil {
			return nil, resourceOperationError(err, "resource_list_failed").redacted(listingRedactor)
		}
		if resource == nil {
			return nil, protocolError("resource_list_invalid", "the upstream MCP server returned an invalid resource entry", "check the upstream MCP server diagnostics")
		}
		protectResourceIdentifier(resource.URI, listingRedactor)
		entries = append(entries, resourceEntry{rawURI: resource.URI, rawName: resource.Name, resource: resource})
	}
	for index := range entries {
		resource := entries[index].resource
		uri, uriErr := normalizedResourceURI(resource.URI, listingRedactor)
		if uriErr != nil {
			return nil, uriErr
		}
		entries[index].summary = resourceSummary{URI: uri, Name: listingRedactor.Redact(resource.Name), Title: listingRedactor.Redact(resource.Title), Description: listingRedactor.Redact(resource.Description), MIMEType: listingRedactor.Redact(resource.MIMEType), Size: resource.Size}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].rawURI != entries[j].rawURI {
			return entries[i].rawURI < entries[j].rawURI
		}
		return entries[i].rawName < entries[j].rawName
	})
	resources := make([]resourceSummary, len(entries))
	for index, entry := range entries {
		resources[index] = entry.summary
	}
	return resources, nil
}

func directResourceTemplates(ctx context.Context, target connectionTarget, redactor *redactor) ([]resourceTemplateSummary, *appError) {
	session, appErr := connectTarget(ctx, target, redactor)
	if appErr != nil {
		return nil, appErr
	}
	defer session.Close()
	return sessionResourceTemplates(ctx, session, redactor)
}

func sessionResourceTemplates(ctx context.Context, session *mcp.ClientSession, redactor *redactor) ([]resourceTemplateSummary, *appError) {
	if !resourcesAvailable(session) {
		return nil, resourceCapabilityError()
	}
	type resourceTemplateEntry struct {
		rawTemplate string
		rawName     string
		template    *mcp.ResourceTemplate
		summary     resourceTemplateSummary
	}
	entries := make([]resourceTemplateEntry, 0)
	listingRedactor := clonedResourceRedactor(redactor)
	for template, err := range session.ResourceTemplates(ctx, nil) {
		if err != nil {
			return nil, resourceOperationError(err, "resource_template_list_failed").redacted(listingRedactor)
		}
		if template == nil {
			return nil, protocolError("resource_template_list_invalid", "the upstream MCP server returned an invalid resource template", "check the upstream MCP server diagnostics")
		}
		protectResourceTemplateIdentifier(template.URITemplate, listingRedactor)
		entries = append(entries, resourceTemplateEntry{rawTemplate: template.URITemplate, rawName: template.Name, template: template})
	}
	for index := range entries {
		template := entries[index].template
		uriTemplate, uriErr := normalizedResourceTemplate(template.URITemplate, listingRedactor)
		if uriErr != nil {
			return nil, uriErr
		}
		entries[index].summary = resourceTemplateSummary{URITemplate: uriTemplate, Name: listingRedactor.Redact(template.Name), Title: listingRedactor.Redact(template.Title), Description: listingRedactor.Redact(template.Description), MIMEType: listingRedactor.Redact(template.MIMEType)}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].rawTemplate != entries[j].rawTemplate {
			return entries[i].rawTemplate < entries[j].rawTemplate
		}
		return entries[i].rawName < entries[j].rawName
	})
	templates := make([]resourceTemplateSummary, len(entries))
	for index, entry := range entries {
		templates[index] = entry.summary
	}
	return templates, nil
}

func directResource(ctx context.Context, target connectionTarget, uri string, redactor *redactor) ([]resourceContent, *appError) {
	session, appErr := connectTarget(ctx, target, redactor)
	if appErr != nil {
		return nil, appErr
	}
	defer session.Close()
	return sessionResource(ctx, session, uri, redactor)
}

func sessionResource(ctx context.Context, session *mcp.ClientSession, uri string, redactor *redactor) ([]resourceContent, *appError) {
	if !resourcesAvailable(session) {
		return nil, resourceCapabilityError()
	}
	diagnosticRedactor := resourceDiagnosticRedactor(redactor, uri)
	response, err := session.ReadResource(ctx, &mcp.ReadResourceParams{URI: uri})
	if err != nil {
		return nil, resourceOperationError(err, "resource_read_failed").redacted(diagnosticRedactor)
	}
	if response == nil {
		return nil, protocolError("resource_read_invalid", "the upstream MCP server returned no resource result", "check the upstream MCP server diagnostics")
	}
	if response.NeedsInput() {
		return nil, userActionError("input_required", "the upstream resource requires additional user input", "use an explicit interactive flow when one is available")
	}
	responseRedactor := clonedResourceRedactor(diagnosticRedactor)
	for _, content := range response.Contents {
		if content == nil {
			return nil, protocolError("resource_content_invalid", "the upstream MCP server returned an invalid resource content entry", "check the upstream MCP server diagnostics")
		}
		protectResourceIdentifier(content.URI, responseRedactor)
	}
	contents := make([]resourceContent, 0, len(response.Contents))
	for _, content := range response.Contents {
		contentURI, uriErr := normalizedResourceURI(content.URI, responseRedactor)
		if uriErr != nil {
			return nil, uriErr
		}
		entry := resourceContent{URI: contentURI, MIMEType: responseRedactor.Redact(content.MIMEType)}
		switch {
		case content.Text != "" && content.Blob != nil:
			return nil, protocolError("resource_content_unsupported", "the upstream MCP server returned both text and blob content", "check the upstream MCP server diagnostics")
		case content.Blob != nil:
			// The SDK decoded the upstream base64 into Blob. Re-encode only after
			// redacting known resolved secrets from those decoded bytes.
			blob := base64.StdEncoding.EncodeToString([]byte(responseRedactor.Redact(string(content.Blob))))
			entry.Blob = &blob
		default:
			text := responseRedactor.Redact(content.Text)
			entry.Text = &text
		}
		contents = append(contents, entry)
	}
	return contents, nil
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

type resourceSummary struct {
	URI         string `json:"uri"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
	Size        int64  `json:"size,omitempty"`
}

type resourcesEnvelope struct {
	OK        bool              `json:"ok"`
	Server    string            `json:"server"`
	Resources []resourceSummary `json:"resources"`
}

type resourceTemplateSummary struct {
	URITemplate string `json:"uri_template"`
	Name        string `json:"name"`
	Title       string `json:"title,omitempty"`
	Description string `json:"description,omitempty"`
	MIMEType    string `json:"mime_type,omitempty"`
}

type resourceTemplatesEnvelope struct {
	OK                bool                      `json:"ok"`
	Server            string                    `json:"server"`
	ResourceTemplates []resourceTemplateSummary `json:"resource_templates"`
}

type resourceContent struct {
	URI      string  `json:"uri"`
	MIMEType string  `json:"mime_type,omitempty"`
	Text     *string `json:"text,omitempty"`
	Blob     *string `json:"blob,omitempty"`
}

type resourceEnvelope struct {
	OK       bool              `json:"ok"`
	Server   string            `json:"server"`
	URI      string            `json:"uri"`
	Contents []resourceContent `json:"contents"`
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
