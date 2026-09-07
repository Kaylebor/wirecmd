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
	"sync"

	"github.com/Kaylebor/wirecmd/internal/buildinfo"
	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/discovery"
	"github.com/Kaylebor/wirecmd/internal/lspclient"
	"github.com/bmatcuk/doublestar/v4"
	"go.lsp.dev/jsonrpc2"
)

const (
	lspDefinition     = "definition"
	lspDeclaration    = "declaration"
	lspTypeDefinition = "type-definition"
	lspImplementation = "implementation"
	lspReferences     = "references"
	lspStatus         = "status"
)

type lspRequest struct {
	Operation          string
	File               string
	Line               int
	Column             int
	IncludeDeclaration bool
}

type lspPosition struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type lspRange struct {
	Start lspPosition `json:"start"`
	End   lspPosition `json:"end"`
}

type lspLocation struct {
	Provider string   `json:"provider"`
	Path     string   `json:"path"`
	Range    lspRange `json:"range"`
}

type lspProviderOutcome struct {
	Name      string     `json:"name"`
	Status    string     `json:"status"`
	Locations int        `json:"locations"`
	Error     *errorBody `json:"error,omitempty"`
}

type lspResult struct {
	Operation string               `json:"operation"`
	File      string               `json:"file"`
	Partial   bool                 `json:"partial"`
	Locations []lspLocation        `json:"locations"`
	Providers []lspProviderOutcome `json:"providers"`
}

type lspEnvelope struct {
	OK  bool      `json:"ok"`
	LSP lspResult `json:"lsp"`
}

type lspStatusSelector struct {
	LanguageID string `json:"language_id"`
	Pattern    string `json:"pattern"`
	Matched    *bool  `json:"matched,omitempty"`
}

type lspRuntimeStatus struct {
	Status        string                  `json:"status"`
	ServerName    string                  `json:"server_name,omitempty"`
	ServerVersion string                  `json:"server_version,omitempty"`
	Capabilities  *lspclient.Capabilities `json:"capabilities,omitempty"`
}

type lspDefinitionStatus struct {
	Name             string              `json:"name"`
	ImplementationID string              `json:"implementation_id,omitempty"`
	Executable       string              `json:"executable"`
	Selectors        []lspStatusSelector `json:"selectors"`
	Runtime          lspRuntimeStatus    `json:"runtime"`
}

type lspStatusResult struct {
	Operation string                `json:"operation"`
	Workspace string                `json:"workspace"`
	File      string                `json:"file,omitempty"`
	Providers []lspDefinitionStatus `json:"providers"`
}

type lspStatusEnvelope struct {
	OK  bool            `json:"ok"`
	LSP lspStatusResult `json:"lsp"`
}

type lspMatch struct {
	Definition config.LSP
	LanguageID string
}

type lspProviderRun struct {
	Locations   []lspclient.Location
	Err         *appError
	Unsupported bool
}

func isLSPNavigation(operation string) bool {
	switch operation {
	case lspDefinition, lspDeclaration, lspTypeDefinition, lspImplementation, lspReferences:
		return true
	default:
		return false
	}
}

// parseLSPCommand reserves only bare native forms. JSON input modes remain
// structural escapes for an MCP server named lsp.
func parseLSPCommand(positionals []string, opts options) (lspRequest, *appError, bool) {
	if len(positionals) < 2 || positionals[0] != "lsp" || opts.jsonSet || opts.stdin {
		return lspRequest{}, nil, false
	}
	operation := positionals[1]
	if !isLSPNavigation(operation) && operation != lspStatus {
		return lspRequest{}, nil, false
	}
	flags := flag.NewFlagSet("wirecmd lsp "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var file string
	var line, column int
	var lineSet, columnSet bool
	var includeDeclaration bool
	flags.StringVar(&file, "file", "", "source file")
	if operation != lspStatus {
		flags.Func("line", "one-based line", func(value string) error {
			parsed, err := parsePositiveLSPPosition("line", value)
			if err == nil {
				line, lineSet = parsed, true
			}
			return err
		})
		flags.Func("column", "one-based column", func(value string) error {
			parsed, err := parsePositiveLSPPosition("column", value)
			if err == nil {
				column, columnSet = parsed, true
			}
			return err
		})
	}
	if operation == lspReferences {
		flags.BoolVar(&includeDeclaration, "include-declaration", false, "include declarations")
	}
	usage := "use wirecmd lsp " + operation
	if operation != lspStatus {
		usage += " --file PATH --line N --column N"
	}
	if err := flags.Parse(positionals[2:]); err != nil {
		return lspRequest{}, invocationError("lsp_"+strings.ReplaceAll(operation, "-", "_")+"_flags", err.Error(), usage), true
	}
	if flags.NArg() != 0 {
		return lspRequest{}, invocationError("lsp_"+strings.ReplaceAll(operation, "-", "_")+"_arguments", operation+" does not accept positional arguments", usage), true
	}
	if operation != lspStatus {
		if file == "" {
			return lspRequest{}, invocationError("lsp_file_required", operation+" requires a non-empty --file", "supply --file PATH"), true
		}
		if !lineSet {
			return lspRequest{}, invocationError("lsp_line_required", operation+" requires --line", "supply a one-based --line N"), true
		}
		if !columnSet {
			return lspRequest{}, invocationError("lsp_column_required", operation+" requires --column", "supply a one-based --column N"), true
		}
	}
	return lspRequest{Operation: operation, File: file, Line: line, Column: column, IncludeDeclaration: includeDeclaration}, nil, true
}

func parsePositiveLSPPosition(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || uint64(parsed) > math.MaxUint32 {
		return 0, fmt.Errorf("--%s must be a positive integer", name)
	}
	return parsed, nil
}

func lspHelp(positionals []string, opts options) (helpText, *appError, bool) {
	if opts.helpServer || len(positionals) == 0 || positionals[0] != "lsp" {
		return "", nil, false
	}
	if opts.jsonSet || opts.stdin {
		return "", invocationError("input_with_help", "--json and --stdin cannot be used with --help", "request help without a tool input mode"), true
	}
	if len(positionals) == 1 {
		return helpText(lspHelpText()), nil, true
	}
	if len(positionals) == 2 && (isLSPNavigation(positionals[1]) || positionals[1] == lspStatus) {
		return helpText(lspOperationHelpText(positionals[1])), nil, true
	}
	return "", nil, false
}

func lspHelpText() string {
	return `Native LSP operations:
  wirecmd [client flags] lsp definition --file PATH --line N --column N
  wirecmd [client flags] lsp declaration --file PATH --line N --column N
  wirecmd [client flags] lsp type-definition --file PATH --line N --column N
  wirecmd [client flags] lsp implementation --file PATH --line N --column N
  wirecmd [client flags] lsp references [--include-declaration] --file PATH --line N --column N
  wirecmd [client flags] lsp status [--file PATH]

Wirecmd routes navigation to every configured LSP selector matching the file.
Normal calls retain sessions through the daemon; --direct uses one-shot
processes. The bare lsp command is native help. Use --help --, --json, --stdin,
or an exact call object to reach a configured MCP server named lsp.
`
}

func lspOperationHelpText(operation string) string {
	if operation == lspStatus {
		return "Inspect configured LSP providers without starting them.\n\nUsage:\n  wirecmd [client flags] lsp status [--file PATH]\n"
	}
	extra := ""
	if operation == lspReferences {
		extra = " [--include-declaration]"
	}
	return fmt.Sprintf("Run LSP %s through every matching capable provider.\n\nUsage:\n  wirecmd [client flags] lsp %s%s --file PATH --line N --column N\n\nPATH resolves from the caller CWD; line and column are one-based.\n", operation, operation, extra)
}

var executeLSPCommand = runLSPCommand

func runLSPCommand(ctx context.Context, opts options, request lspRequest, _ io.Reader, errOut io.Writer) (any, *appError) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, transportError("caller_cwd_unavailable", err.Error(), "run Wirecmd from an accessible working directory")
	}
	paths, discovered, appErr := lspConfigPaths(cwd, opts.configs)
	if appErr != nil {
		return nil, appErr
	}
	var client *daemonClient
	if !opts.direct {
		client, appErr = openDaemonClient(ctx)
		if appErr != nil {
			return nil, appErr
		}
		defer client.Close()
	}
	cfg, err := loadLSPConfig(paths, discovered)
	if err != nil {
		return nil, configurationError("config_invalid", err.Error(), "correct the supplied KDL configuration")
	}
	if len(cfg.LSPs) == 0 {
		return nil, configurationError("lsp_not_configured", "the effective workspace configuration has no LSP definition", "add a workspace lsp block with at least one selector")
	}
	workspace := effectiveLSPWorkspace(cfg.Root, cwd)
	file := request.File
	if file != "" {
		if !filepath.IsAbs(file) {
			file = filepath.Join(cwd, file)
		}
		file = filepath.Clean(file)
	}
	if request.Operation == lspStatus {
		if opts.direct {
			return makeLSPStatusEnvelope(cfg.LSPs, workspace, file, nil), nil
		}
		rpc := daemonRequest{Operation: statusLSP, CWD: cwd, Configs: paths, Discovered: discovered, Fingerprint: configFingerprint(cfg, cwd), LSPFile: file, LSPOperation: lspStatus}
		result, callErr, _ := daemonRequestCallWithClient(client, rpc, errOut)
		return result, callErr
	}
	matches, appErr := matchLSPDefinitions(cfg.LSPs, workspace, file)
	if appErr != nil {
		return nil, appErr
	}
	for _, match := range matches {
		if _, err := lspclient.PrepareTarget(file, uint32(request.Line), uint32(request.Column), match.LanguageID); err != nil {
			return nil, lspOperationError(err, request.Operation)
		}
	}
	if !opts.direct {
		secrets := selectedLSPMatchesSecretInputs(matches, os.LookupEnv)
		rpc := daemonRequest{Operation: navigateLSP, CWD: cwd, Configs: paths, Discovered: discovered, Fingerprint: configFingerprint(cfg, cwd), Execution: lspMatchesExecutionFingerprint(matches, cfg.Root, cwd), Secrets: secrets, LSPFile: file, LSPLine: request.Line, LSPColumn: request.Column, LSPOperation: request.Operation, LSPIncludeDeclaration: request.IncludeDeclaration}
		result, callErr, _ := daemonRequestCallWithClient(client, rpc, errOut)
		return result, callErr
	}
	return runDirectLSPMatches(ctx, cfg.Root, cwd, file, request, matches, errOut)
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

func effectiveLSPWorkspace(root *config.Root, cwd string) string {
	if root != nil {
		return resolveRoot(*root)
	}
	return cwd
}

func matchLSPDefinitions(definitions []config.LSP, workspace, file string) ([]lspMatch, *appError) {
	relative, err := filepath.Rel(workspace, file)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, configurationError("lsp_no_matching_provider", "the requested file is outside the effective workspace", "run Wirecmd in the intended workspace or configure its root")
	}
	relative = filepath.ToSlash(relative)
	matches := make([]lspMatch, 0)
	for _, definition := range definitions {
		languageID := ""
		for _, selector := range definition.Selectors {
			matched, _ := doublestar.Match(selector.Pattern, relative)
			if !matched {
				continue
			}
			if languageID != "" && languageID != selector.LanguageID {
				return nil, configurationError("lsp_selector_ambiguous", fmt.Sprintf("LSP %q matches %q with language IDs %q and %q", definition.Name, relative, languageID, selector.LanguageID), "make matching selectors for one provider use the same language-id")
			}
			languageID = selector.LanguageID
		}
		if languageID != "" {
			matches = append(matches, lspMatch{Definition: definition, LanguageID: languageID})
		}
	}
	if len(matches) == 0 {
		return nil, configurationError("lsp_no_matching_provider", fmt.Sprintf("no configured LSP selector matches %q", relative), "add or correct a selector pattern for this file")
	}
	return matches, nil
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

func selectedLSPMatchesSecretInputs(matches []lspMatch, lookup func(string) (string, bool)) map[string]secretInput {
	result := map[string]secretInput{}
	for _, match := range matches {
		for name, value := range selectedLSPSecretInputs(match.Definition, lookup) {
			result[name] = value
		}
	}
	return result
}

func runDirectLSPMatches(ctx context.Context, root *config.Root, cwd, file string, request lspRequest, matches []lspMatch, errOut io.Writer) (any, *appError) {
	results := make([]lspProviderRun, len(matches))
	var wait sync.WaitGroup
	var stderrMu sync.Mutex
	lockedErr := writerFunc(func(p []byte) (int, error) {
		stderrMu.Lock()
		defer stderrMu.Unlock()
		return errOut.Write(p)
	})
	for index, match := range matches {
		wait.Add(1)
		go func(index int, match lspMatch) {
			defer wait.Done()
			command, secrets, appErr := makeStdioCommand(match.Definition.Stdio, root, cwd, os.LookupEnv)
			if appErr != nil {
				results[index].Err = appErr
				return
			}
			redactor := newRedactor(secrets, lockedErr)
			defer redactor.FlushTo(lockedErr)
			session, err := lspclient.Start(ctx, lspclient.Command{Path: command.Path, Args: command.Args[1:], Env: command.Env, Dir: command.Dir, Stderr: redactor}, effectiveLSPWorkspace(root, cwd), buildinfo.Version())
			if err != nil {
				results[index].Err = lspOperationError(err, "initialize").redacted(redactor)
				return
			}
			defer session.Close()
			target, err := lspclient.PrepareTarget(file, uint32(request.Line), uint32(request.Column), match.LanguageID)
			if err == nil {
				results[index].Locations, err = callLSPNavigation(ctx, session, target, request.Operation, request.IncludeDeclaration)
			}
			if errors.Is(err, lspclient.ErrCapabilityUnavailable) {
				results[index].Unsupported = true
				return
			}
			if err != nil {
				results[index].Err = lspOperationError(err, request.Operation).redacted(redactor)
			}
		}(index, match)
	}
	wait.Wait()
	return aggregateLSPResults(request.Operation, file, matches, results)
}

type writerFunc func([]byte) (int, error)

func (fn writerFunc) Write(p []byte) (int, error) { return fn(p) }

func callLSPNavigation(ctx context.Context, session *lspclient.Session, target *lspclient.DefinitionTarget, operation string, includeDeclaration bool) ([]lspclient.Location, error) {
	switch operation {
	case lspDefinition:
		return session.Definition(ctx, target)
	case lspDeclaration:
		return session.Declaration(ctx, target)
	case lspTypeDefinition:
		return session.TypeDefinition(ctx, target)
	case lspImplementation:
		return session.Implementation(ctx, target)
	case lspReferences:
		return session.References(ctx, target, includeDeclaration)
	default:
		return nil, fmt.Errorf("unsupported LSP operation %q", operation)
	}
}

func aggregateLSPResults(operation, file string, matches []lspMatch, results []lspProviderRun) (any, *appError) {
	outcomes := make([]lspProviderOutcome, len(matches))
	locations := make([]lspLocation, 0)
	successes, failures := 0, 0
	var firstErr *appError
	for index, match := range matches {
		outcome := lspProviderOutcome{Name: match.Definition.Name}
		switch {
		case results[index].Unsupported:
			outcome.Status = "unsupported"
		case results[index].Err != nil:
			outcome.Status = "failed"
			outcome.Error = bodyFromAppError(results[index].Err)
			failures++
			if firstErr == nil {
				firstErr = results[index].Err
			}
		default:
			outcome.Status = "ok"
			outcome.Locations = len(results[index].Locations)
			successes++
			for _, location := range results[index].Locations {
				locations = append(locations, lspLocation{Provider: match.Definition.Name, Path: location.Path, Range: lspRange{Start: lspPosition{Line: int(location.Range.Start.Line), Column: int(location.Range.Start.Column)}, End: lspPosition{Line: int(location.Range.End.Line), Column: int(location.Range.End.Column)}}})
			}
		}
		outcomes[index] = outcome
	}
	if successes > 0 {
		return lspEnvelope{OK: true, LSP: lspResult{Operation: operation, File: file, Partial: failures > 0, Locations: locations, Providers: outcomes}}, nil
	}
	if firstErr == nil {
		firstErr = protocolError("lsp_capability_unavailable", "no matching LSP provider advertises the requested operation", "configure a matching LSP that supports textDocument/"+operation)
	}
	firstErr.details = map[string]any{"providers": outcomes}
	return nil, firstErr
}

func bodyFromAppError(value *appError) *errorBody {
	if value == nil {
		return nil
	}
	return &errorBody{Category: value.category, Code: value.code, Message: value.message, Action: value.action}
}

func makeLSPStatusEnvelope(definitions []config.LSP, workspace, file string, runtime map[string]lspRuntimeStatus) lspStatusEnvelope {
	providers := make([]lspDefinitionStatus, 0, len(definitions))
	for _, definition := range definitions {
		selectors := make([]lspStatusSelector, 0, len(definition.Selectors))
		for _, selector := range definition.Selectors {
			item := lspStatusSelector{LanguageID: selector.LanguageID, Pattern: selector.Pattern}
			if file != "" {
				relative, err := filepath.Rel(workspace, file)
				matched := false
				if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					matched, _ = doublestar.Match(selector.Pattern, filepath.ToSlash(relative))
				}
				item.Matched = &matched
			}
			selectors = append(selectors, item)
		}
		state := lspRuntimeStatus{Status: "not_checked"}
		if value, ok := runtime[definition.Name]; ok {
			state = value
		}
		providers = append(providers, lspDefinitionStatus{Name: definition.Name, ImplementationID: definition.ImplementationID, Executable: definition.Stdio.Command, Selectors: selectors, Runtime: state})
	}
	return lspStatusEnvelope{OK: true, LSP: lspStatusResult{Operation: lspStatus, Workspace: workspace, File: file, Providers: providers}}
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
		return protocolError("lsp_capability_unavailable", err.Error(), "configure a matching LSP that supports textDocument/"+operation)
	case errors.Is(err, lspclient.ErrEncodingUnsupported):
		return protocolError("lsp_encoding_unsupported", err.Error(), "configure an LSP server that supports UTF-16 positions")
	case errors.Is(err, lspclient.ErrResultUnsupported):
		return protocolError("lsp_result_unsupported", err.Error(), "use an LSP server that returns file locations")
	case errors.Is(err, lspclient.ErrServerRequestUnsupported):
		return protocolError("lsp_server_request_unsupported", err.Error(), "use an LSP server that does not require unsupported client capabilities for this operation")
	case errors.Is(err, jsonrpc2.ErrMethodNotFound):
		return protocolError("lsp_capability_mismatch", "the LSP server advertised support but rejected textDocument/"+operation, "check the selected LSP server and workspace configuration")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return transportError("lsp_request_canceled", "the LSP "+operation+" operation was canceled", "retry the request")
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return transportError("lsp_instance_unavailable", err.Error(), "run wirecmd daemon reload or restart the daemon")
	default:
		return protocolError("lsp_"+strings.ReplaceAll(operation, "-", "_")+"_failed", err.Error(), "check the LSP server diagnostics and workspace configuration")
	}
}
