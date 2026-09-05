package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/Kaylebor/wirecmd/internal/buildinfo"
	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/discovery"
	"github.com/Kaylebor/wirecmd/internal/lspclient"
	"go.lsp.dev/jsonrpc2"
)

// lspDefinitionRequest is the shell-facing input for the first native LSP
// operation. File resolution and LSP position conversion belong to the LSP
// runtime, which has the caller workspace and the selected configuration.
type lspDefinitionRequest struct {
	File   string
	Line   int
	Column int
}

// lspPosition, lspRange, and lspLocation deliberately describe the public
// shell result rather than protocol package types. Positions are one-based.
type lspPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	Path  string   `json:"path"`
	Range lspRange `json:"range"`
}

type lspDefinitionResult struct {
	Operation string        `json:"operation"`
	File      string        `json:"file"`
	Locations []lspLocation `json:"locations"`
}

type lspDefinitionEnvelope struct {
	OK  bool                `json:"ok"`
	LSP lspDefinitionResult `json:"lsp"`
}

// parseLSPDefinitionCommand reserves the bare native form only. JSON input
// modes and exact call objects deliberately remain structural escapes for an
// MCP server named "lsp".
func parseLSPDefinitionCommand(positionals []string, opts options) (lspDefinitionRequest, *appError, bool) {
	if len(positionals) < 2 || positionals[0] != "lsp" || positionals[1] != "definition" || opts.jsonSet || opts.stdin {
		return lspDefinitionRequest{}, nil, false
	}

	flags := flag.NewFlagSet("wirecmd lsp definition", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var file string
	var line, column int
	var lineSet, columnSet bool
	flags.StringVar(&file, "file", "", "source file")
	flags.Func("line", "one-based line", func(value string) error {
		parsed, err := parsePositiveLSPPosition("line", value)
		if err != nil {
			return err
		}
		line, lineSet = parsed, true
		return nil
	})
	flags.Func("column", "one-based column", func(value string) error {
		parsed, err := parsePositiveLSPPosition("column", value)
		if err != nil {
			return err
		}
		column, columnSet = parsed, true
		return nil
	})
	if err := flags.Parse(positionals[2:]); err != nil {
		return lspDefinitionRequest{}, invocationError("lsp_definition_flags", err.Error(), "use wirecmd lsp definition --file PATH --line N --column N"), true
	}
	if flags.NArg() != 0 {
		return lspDefinitionRequest{}, invocationError("lsp_definition_arguments", "definition accepts only --file, --line, and --column", "use wirecmd lsp definition --file PATH --line N --column N"), true
	}
	if file == "" {
		return lspDefinitionRequest{}, invocationError("lsp_file_required", "definition requires a non-empty --file", "supply --file PATH"), true
	}
	if !lineSet {
		return lspDefinitionRequest{}, invocationError("lsp_line_required", "definition requires --line", "supply a one-based --line N"), true
	}
	if !columnSet {
		return lspDefinitionRequest{}, invocationError("lsp_column_required", "definition requires --column", "supply a one-based --column N"), true
	}
	return lspDefinitionRequest{File: file, Line: line, Column: column}, nil, true
}

func parsePositiveLSPPosition(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || uint64(parsed) > math.MaxUint32 {
		return 0, fmt.Errorf("--%s must be a positive integer", name)
	}
	return parsed, nil
}

// lspHelp resolves only the native, statically known help forms. The prefix
// -- escape intentionally leaves a configured MCP server named lsp reachable.
func lspHelp(positionals []string, opts options) (helpText, *appError, bool) {
	if opts.helpServer || len(positionals) == 0 || positionals[0] != "lsp" {
		return "", nil, false
	}
	switch len(positionals) {
	case 1:
		if opts.jsonSet || opts.stdin {
			return "", invocationError("input_with_help", "--json and --stdin cannot be used with --help", "request help without a tool input mode"), true
		}
		return helpText(lspHelpText()), nil, true
	case 2:
		if positionals[1] == "definition" {
			if opts.jsonSet || opts.stdin {
				return "", invocationError("input_with_help", "--json and --stdin cannot be used with --help", "request help without a tool input mode"), true
			}
			return helpText(lspDefinitionHelpText()), nil, true
		}
	}
	return "", nil, false
}

func lspHelpText() string {
	return `Native LSP operations:
  wirecmd [client flags] lsp definition --file PATH --line N --column N

LSP operations use the single workspace-scoped LSP configured for the caller's
workspace. Normal calls use the daemon and retain the language-server session;
--direct deliberately starts a one-shot process. Ask for focused help with:
  wirecmd --help lsp definition

The bare lsp command is native help. For a configured MCP server named lsp,
use --help -- for focused help, or --json, --stdin, or an exact call object to
invoke tools.
`
}

func lspDefinitionHelpText() string {
	return `Find a definition through the configured workspace LSP.

Usage:
  wirecmd [client flags] lsp definition --file PATH --line N --column N

PATH is resolved from the caller's current directory. Line and column are
one-based. Results contain zero or more file locations with one-based ranges.
The operation lazily opens the on-disk file in the retained LSP session.

Client flags, including --config and --direct, must appear before lsp.
`
}

var executeLSPDefinition = runLSPDefinition

func runLSPDefinition(ctx context.Context, opts options, request lspDefinitionRequest, _ io.Reader, errOut io.Writer) (any, *appError) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, transportError("caller_cwd_unavailable", err.Error(), "run Wirecmd from an accessible working directory")
	}
	paths, discovered, appErr := lspConfigPaths(cwd, opts.configs)
	if appErr != nil {
		return nil, appErr
	}

	// Match ordinary calls: prove a compatible daemon is available before
	// parsing configuration, and never fall back to direct execution.
	var daemonClient *daemonClient
	if !opts.direct {
		daemonClient, appErr = openDaemonClient(ctx)
		if appErr != nil {
			return nil, appErr
		}
		defer daemonClient.Close()
	}
	cfg, err := loadLSPConfig(paths, discovered)
	if err != nil {
		return nil, configurationError("config_invalid", err.Error(), "correct the supplied KDL configuration")
	}
	definition, appErr := selectLSP(cfg)
	if appErr != nil {
		return nil, appErr
	}
	file := request.File
	if !filepath.IsAbs(file) {
		file = filepath.Join(cwd, file)
	}
	file = filepath.Clean(file)
	target, err := lspclient.PrepareDefinition(file, uint32(request.Line), uint32(request.Column))
	if err != nil {
		return nil, lspOperationError(err, "definition")
	}

	if !opts.direct {
		secrets := selectedLSPSecretInputs(definition, os.LookupEnv)
		rpc := daemonRequest{
			Operation: defineLSP, CWD: cwd, Configs: paths, Discovered: discovered,
			Fingerprint: configFingerprint(cfg, cwd), Execution: lspExecutionFingerprint(definition, cfg.Root, cwd),
			Secrets: secrets, LSPFile: file, LSPLine: request.Line, LSPColumn: request.Column,
		}
		result, callErr, _ := daemonRequestCallWithClient(daemonClient, rpc, errOut)
		return result, callErr
	}

	lookup := os.LookupEnv
	command, secrets, appErr := makeStdioCommand(definition.Stdio, cfg.Root, cwd, lookup)
	if appErr != nil {
		return nil, appErr
	}
	redactor := newRedactor(secrets, errOut)
	defer redactor.FlushTo(errOut)
	workspace := cwd
	if cfg.Root != nil {
		workspace = resolveRoot(*cfg.Root)
	}
	session, err := lspclient.Start(ctx, lspclient.Command{Path: command.Path, Args: command.Args[1:], Env: command.Env, Dir: command.Dir, Stderr: redactor}, definition.LanguageID, workspace, buildinfo.Version())
	if err != nil {
		return nil, lspOperationError(err, "initialize").redacted(redactor)
	}
	defer session.Close()
	locations, err := session.Definition(ctx, target)
	if err != nil {
		return nil, lspOperationError(err, "definition").redacted(redactor)
	}
	return makeLSPDefinitionEnvelope(file, locations), nil
}

func lspConfigPaths(cwd string, configured []string) ([]string, bool, *appError) {
	if len(configured) != 0 {
		paths, err := absoluteConfigPaths(cwd, configured)
		if err != nil {
			return nil, false, configurationError("config_path_invalid", err.Error(), "supply valid configuration paths")
		}
		return paths, false, nil
	}
	paths, err := discovery.Paths(cwd)
	if err == nil {
		return paths, true, nil
	}
	var untrusted *discovery.UntrustedError
	var notFound *discovery.NotFoundError
	switch {
	case errors.As(err, &untrusted):
		return nil, true, userActionError("workspace_untrusted", err.Error(), "run wirecmd config trust "+shellQuote(untrusted.Workspace))
	case errors.As(err, &notFound):
		return nil, true, configurationError("config_not_found", err.Error(), "create a global or workspace wirecmd.kdl, or supply --config PATH")
	default:
		return nil, true, configurationError("config_discovery_failed", err.Error(), "check Wirecmd configuration and trust state")
	}
}

func loadLSPConfig(paths []string, discovered bool) (*config.Config, error) {
	if discovered {
		return config.LoadEffectiveDiscovered(paths)
	}
	return config.LoadEffective(paths)
}

func selectLSP(cfg *config.Config) (config.LSP, *appError) {
	switch len(cfg.LSPs) {
	case 0:
		return config.LSP{}, configurationError("lsp_not_configured", "the effective workspace configuration has no LSP definition", "add one workspace-scoped lsp block")
	case 1:
		return cfg.LSPs[0], nil
	default:
		names := make([]string, 0, len(cfg.LSPs))
		for _, definition := range cfg.LSPs {
			names = append(names, definition.Name)
		}
		return config.LSP{}, configurationError("lsp_ambiguous_configuration", "the effective workspace config contains multiple LSP definitions: "+strings.Join(names, ", "), "configure exactly one LSP definition for this workspace")
	}
}

func selectedLSPSecretInputs(definition config.LSP, lookup func(string) (string, bool)) map[string]secretInput {
	return selectedStdioSecretInputs(definition.Stdio, lookup)
}

func selectedStdioSecretInputs(stdio config.Stdio, lookup func(string) (string, bool)) map[string]secretInput {
	result := map[string]secretInput{}
	add := func(value config.Value) {
		if !value.IsSecret() {
			return
		}
		name := strings.TrimPrefix(value.Text, "env://")
		if _, exists := result[name]; exists {
			return
		}
		resolved, present := lookup(name)
		result[name] = secretInput{Present: present, Value: resolved}
	}
	for _, value := range stdio.Args {
		add(value)
	}
	for _, assignment := range stdio.Env {
		add(assignment.Value)
	}
	return result
}

func makeLSPDefinitionEnvelope(file string, locations []lspclient.Location) lspDefinitionEnvelope {
	result := make([]lspLocation, 0, len(locations))
	for _, location := range locations {
		result = append(result, lspLocation{Path: location.Path, Range: lspRange{Start: lspPosition{Line: int(location.Range.Start.Line), Column: int(location.Range.Start.Column)}, End: lspPosition{Line: int(location.Range.End.Line), Column: int(location.Range.End.Column)}}})
	}
	return lspDefinitionEnvelope{OK: true, LSP: lspDefinitionResult{Operation: "definition", File: file, Locations: result}}
}

func lspOperationError(err error, operation string) *appError {
	var position *lspclient.PositionError
	var pathErr *os.PathError
	switch {
	case errors.Is(err, lspclient.ErrStart):
		return transportError("lsp_instance_unavailable", err.Error(), "correct the configured LSP executable or environment")
	case errors.As(err, &pathErr), errors.As(err, &position):
		return invocationError("lsp_position_invalid", err.Error(), "supply a readable file and a valid one-based line and column")
	case errors.Is(err, lspclient.ErrCapabilityUnavailable):
		return protocolError("lsp_capability_unavailable", err.Error(), "choose an LSP server that supports textDocument/definition")
	case errors.Is(err, lspclient.ErrEncodingUnsupported):
		return protocolError("lsp_encoding_unsupported", err.Error(), "configure an LSP server that supports UTF-16 positions")
	case errors.Is(err, lspclient.ErrResultUnsupported):
		return protocolError("lsp_result_unsupported", err.Error(), "use an LSP server that returns file definition locations")
	case errors.Is(err, lspclient.ErrServerRequestUnsupported):
		return protocolError("lsp_server_request_unsupported", err.Error(), "use an LSP server that does not require unsupported client capabilities for this operation")
	case errors.Is(err, jsonrpc2.ErrMethodNotFound):
		return protocolError("lsp_capability_mismatch", "the LSP server advertised definition support but rejected textDocument/definition", "check the selected LSP server and workspace configuration")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return transportError("lsp_request_canceled", "the LSP "+operation+" operation was canceled", "retry the request")
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return transportError("lsp_instance_unavailable", err.Error(), "run wirecmd daemon reload or restart the daemon")
	default:
		return protocolError("lsp_"+operation+"_failed", err.Error(), "check the LSP server diagnostics and workspace configuration")
	}
}
