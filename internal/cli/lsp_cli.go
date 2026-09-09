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
	lspDefinition       = "definition"
	lspDeclaration      = "declaration"
	lspTypeDefinition   = "type-definition"
	lspImplementation   = "implementation"
	lspReferences       = "references"
	lspHover            = "hover"
	lspSignatureHelp    = "signature-help"
	lspDocumentSymbols  = "document-symbols"
	lspWorkspaceSymbols = "workspace-symbols"
	lspStatus           = "status"
)

type lspRequest struct {
	Operation          string
	File               string
	Line               int
	Column             int
	Query              string
	QuerySet           bool
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

type lspHoverContent struct {
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Language string `json:"language,omitempty"`
}

type lspHoverEntry struct {
	Provider string            `json:"provider"`
	Range    *lspRange         `json:"range,omitempty"`
	Contents []lspHoverContent `json:"contents"`
}

type lspHoverProviderOutcome struct {
	Name   string     `json:"name"`
	Status string     `json:"status"`
	Hovers int        `json:"hovers"`
	Error  *errorBody `json:"error,omitempty"`
}

type lspHoverResult struct {
	Operation string                    `json:"operation"`
	File      string                    `json:"file"`
	Line      int                       `json:"line"`
	Column    int                       `json:"column"`
	Partial   bool                      `json:"partial"`
	Hovers    []lspHoverEntry           `json:"hovers"`
	Providers []lspHoverProviderOutcome `json:"providers"`
}

type lspHoverEnvelope struct {
	OK  bool           `json:"ok"`
	LSP lspHoverResult `json:"lsp"`
}

type lspSignatureDocumentation struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type lspSignatureParameter struct {
	Label         string                     `json:"label"`
	Active        bool                       `json:"active"`
	Documentation *lspSignatureDocumentation `json:"documentation,omitempty"`
}

type lspSignature struct {
	Provider      string                     `json:"provider"`
	Label         string                     `json:"label"`
	Active        bool                       `json:"active"`
	Documentation *lspSignatureDocumentation `json:"documentation,omitempty"`
	Parameters    []lspSignatureParameter    `json:"parameters"`
}

type lspSignatureProviderOutcome struct {
	Name       string     `json:"name"`
	Status     string     `json:"status"`
	Signatures int        `json:"signatures"`
	Error      *errorBody `json:"error,omitempty"`
}

type lspSignatureResult struct {
	Operation  string                        `json:"operation"`
	File       string                        `json:"file"`
	Line       int                           `json:"line"`
	Column     int                           `json:"column"`
	Partial    bool                          `json:"partial"`
	Signatures []lspSignature                `json:"signatures"`
	Providers  []lspSignatureProviderOutcome `json:"providers"`
}

type lspSignatureEnvelope struct {
	OK  bool               `json:"ok"`
	LSP lspSignatureResult `json:"lsp"`
}

type lspSymbol struct {
	Provider       string      `json:"provider"`
	Name           string      `json:"name"`
	Kind           int         `json:"kind"`
	KindName       string      `json:"kind_name"`
	Path           string      `json:"path"`
	Range          lspRange    `json:"range"`
	SelectionRange *lspRange   `json:"selection_range,omitempty"`
	Detail         string      `json:"detail,omitempty"`
	ContainerName  string      `json:"container_name,omitempty"`
	Deprecated     bool        `json:"deprecated,omitempty"`
	Children       []lspSymbol `json:"children,omitempty"`
}

type lspSymbolProviderOutcome struct {
	Name    string     `json:"name"`
	Status  string     `json:"status"`
	Symbols int        `json:"symbols"`
	Error   *errorBody `json:"error,omitempty"`
}

type lspSymbolResult struct {
	Operation string                     `json:"operation"`
	File      string                     `json:"file,omitempty"`
	Workspace string                     `json:"workspace,omitempty"`
	Query     *string                    `json:"query,omitempty"`
	Partial   bool                       `json:"partial"`
	Symbols   []lspSymbol                `json:"symbols"`
	Providers []lspSymbolProviderOutcome `json:"providers"`
}

type lspSymbolEnvelope struct {
	OK  bool            `json:"ok"`
	LSP lspSymbolResult `json:"lsp"`
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
	Hovers      []lspclient.Hover
	Signatures  []lspclient.Signature
	Symbols     []lspclient.Symbol
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

func isLSPInspection(operation string) bool {
	switch operation {
	case lspHover, lspSignatureHelp, lspDocumentSymbols, lspWorkspaceSymbols:
		return true
	default:
		return false
	}
}

func isLSPFileOperation(operation string) bool {
	return isLSPNavigation(operation) || operation == lspHover || operation == lspSignatureHelp || operation == lspDocumentSymbols
}

func isLSPPositionOperation(operation string) bool {
	return isLSPNavigation(operation) || operation == lspHover || operation == lspSignatureHelp
}

// parseLSPCommand reserves only bare native forms. JSON input modes remain
// structural escapes for an MCP server named lsp.
func parseLSPCommand(positionals []string, opts options) (lspRequest, *appError, bool) {
	if len(positionals) < 2 || positionals[0] != "lsp" || opts.jsonSet || opts.stdin {
		return lspRequest{}, nil, false
	}
	operation := positionals[1]
	if !isLSPNavigation(operation) && !isLSPInspection(operation) && operation != lspStatus {
		return lspRequest{}, nil, false
	}
	flags := flag.NewFlagSet("wirecmd lsp "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var file string
	var line, column int
	var lineSet, columnSet bool
	var query string
	var querySet bool
	var includeDeclaration bool
	flags.StringVar(&file, "file", "", "source file")
	if isLSPPositionOperation(operation) {
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
	if operation == lspWorkspaceSymbols {
		flags.Func("query", "workspace symbol query", func(value string) error {
			query, querySet = value, true
			return nil
		})
	}
	if operation == lspReferences {
		flags.BoolVar(&includeDeclaration, "include-declaration", false, "include declarations")
	}
	usage := "use wirecmd lsp " + operation
	if isLSPPositionOperation(operation) {
		usage += " --file PATH --line N --column N"
	} else if operation == lspDocumentSymbols {
		usage += " --file PATH"
	} else if operation == lspWorkspaceSymbols {
		usage += " --query TEXT"
	}
	if err := flags.Parse(positionals[2:]); err != nil {
		return lspRequest{}, invocationError("lsp_"+strings.ReplaceAll(operation, "-", "_")+"_flags", err.Error(), usage), true
	}
	if flags.NArg() != 0 {
		return lspRequest{}, invocationError("lsp_"+strings.ReplaceAll(operation, "-", "_")+"_arguments", operation+" does not accept positional arguments", usage), true
	}
	if isLSPFileOperation(operation) {
		if file == "" {
			return lspRequest{}, invocationError("lsp_file_required", operation+" requires a non-empty --file", "supply --file PATH"), true
		}
	}
	if isLSPPositionOperation(operation) {
		if !lineSet {
			return lspRequest{}, invocationError("lsp_line_required", operation+" requires --line", "supply a one-based --line N"), true
		}
		if !columnSet {
			return lspRequest{}, invocationError("lsp_column_required", operation+" requires --column", "supply a one-based --column N"), true
		}
	}
	if operation == lspWorkspaceSymbols && !querySet {
		return lspRequest{}, invocationError("lsp_query_required", operation+" requires --query", "supply --query TEXT; an explicit empty query is allowed"), true
	}
	return lspRequest{Operation: operation, File: file, Line: line, Column: column, Query: query, QuerySet: querySet, IncludeDeclaration: includeDeclaration}, nil, true
}

func parsePositiveLSPPosition(name, value string) (int, error) {
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || uint64(parsed) > math.MaxUint32 {
		return 0, fmt.Errorf("--%s must be a positive integer", name)
	}
	return parsed, nil
}

func lspHelp(positionals []string, opts options) (helpText, *appError, bool) {
	if len(positionals) == 0 || positionals[0] != "lsp" {
		return "", nil, false
	}
	if opts.jsonSet || opts.stdin {
		return "", invocationError("input_with_help", "--json and --stdin cannot be used with --help", "request help without a tool input mode"), true
	}
	if len(positionals) == 1 {
		return helpText(lspHelpText()), nil, true
	}
	if len(positionals) == 2 && (isLSPNavigation(positionals[1]) || isLSPInspection(positionals[1]) || positionals[1] == lspStatus) {
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
  wirecmd [client flags] lsp hover --file PATH --line N --column N
  wirecmd [client flags] lsp signature-help --file PATH --line N --column N
  wirecmd [client flags] lsp document-symbols --file PATH
  wirecmd [client flags] lsp workspace-symbols --query TEXT
  wirecmd [client flags] lsp status [--file PATH]

Wirecmd routes file operations to every configured LSP selector matching the
file; workspace-symbols queries every configured provider.
Normal calls retain sessions through the daemon; --direct uses one-shot
processes. The bare lsp command is native help. Use wirecmd mcp lsp to reach a
configured MCP server named lsp.
`
}

func lspOperationHelpText(operation string) string {
	if operation == lspStatus {
		return "Inspect configured LSP providers without starting them.\n\nUsage:\n  wirecmd [client flags] lsp status [--file PATH]\n"
	}
	if operation == lspDocumentSymbols {
		return "Inspect document symbols through every matching capable provider.\n\nUsage:\n  wirecmd [client flags] lsp document-symbols --file PATH\n\nPATH resolves from the caller CWD.\n"
	}
	if operation == lspWorkspaceSymbols {
		return "Search workspace symbols through every configured capable provider.\n\nUsage:\n  wirecmd [client flags] lsp workspace-symbols --query TEXT\n\nThe query is required, may be empty, and is passed unchanged.\n"
	}
	if operation == lspSignatureHelp {
		return "Inspect callable signatures through every matching capable provider.\n\nUsage:\n  wirecmd [client flags] lsp signature-help --file PATH --line N --column N\n\nPATH resolves from the caller CWD; line and column are one-based.\n"
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
	// Discovery resolves trusted workspaces through symlinks. Keep the caller
	// path in that same identity space so macOS aliases such as /var and
	// /private/var cannot make an in-workspace file appear to be outside root.
	if discovered {
		cwd, err = filepath.EvalSymlinks(cwd)
		if err != nil {
			return nil, transportError("caller_cwd_unavailable", err.Error(), "run Wirecmd from an accessible working directory")
		}
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
	var matches []lspMatch
	if request.Operation == lspWorkspaceSymbols {
		matches = allLSPDefinitions(cfg.LSPs)
	} else {
		matches, appErr = matchLSPDefinitions(cfg.LSPs, workspace, file)
		if appErr != nil {
			return nil, appErr
		}
	}
	if appErr := validateLSPRequestInput(file, request, matches); appErr != nil {
		return nil, appErr
	}
	if !opts.direct {
		secrets := selectedLSPMatchesSecretInputs(matches, os.LookupEnv)
		daemonOperation := inspectLSP
		if isLSPNavigation(request.Operation) {
			daemonOperation = navigateLSP
		}
		rpc := daemonRequest{Operation: daemonOperation, CWD: cwd, Configs: paths, Discovered: discovered, Fingerprint: configFingerprint(cfg, cwd), Execution: lspMatchesExecutionFingerprint(matches, cfg.Root, cwd), Secrets: secrets, LSPFile: file, LSPLine: request.Line, LSPColumn: request.Column, LSPQuery: request.Query, LSPQuerySet: request.QuerySet, LSPOperation: request.Operation, LSPIncludeDeclaration: request.IncludeDeclaration}
		result, callErr, _ := daemonRequestCallWithClient(client, rpc, errOut)
		return result, callErr
	}
	return runDirectLSPMatches(ctx, cfg.Root, cwd, workspace, file, request, matches, errOut)
}

func allLSPDefinitions(definitions []config.LSP) []lspMatch {
	matches := make([]lspMatch, len(definitions))
	for index, definition := range definitions {
		matches[index] = lspMatch{Definition: definition}
	}
	return matches
}

func validateLSPRequestInput(file string, request lspRequest, matches []lspMatch) *appError {
	for _, match := range matches {
		var err error
		switch {
		case isLSPPositionOperation(request.Operation):
			_, err = lspclient.PrepareTarget(file, uint32(request.Line), uint32(request.Column), match.LanguageID)
		case request.Operation == lspDocumentSymbols:
			_, err = lspclient.PrepareDocument(file, match.LanguageID)
		}
		if err != nil {
			return lspOperationError(err, request.Operation)
		}
	}
	return nil
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

func runDirectLSPMatches(ctx context.Context, root *config.Root, cwd, workspace, file string, request lspRequest, matches []lspMatch, errOut io.Writer) (any, *appError) {
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
			err = callLSPRequest(ctx, session, file, request, match.LanguageID, &results[index])
			if errors.Is(err, lspclient.ErrCapabilityUnavailable) {
				results[index].Unsupported = true
				return
			}
			if err != nil {
				results[index].Err = lspOperationError(err, request.Operation).redacted(redactor)
				return
			}
			redactLSPProviderRun(&results[index], redactor)
		}(index, match)
	}
	wait.Wait()
	return aggregateLSPRequestResults(request, workspace, file, matches, results)
}

func redactLSPProviderRun(result *lspProviderRun, redactor *redactor) {
	for hoverIndex := range result.Hovers {
		for contentIndex := range result.Hovers[hoverIndex].Content {
			content := &result.Hovers[hoverIndex].Content[contentIndex]
			content.Kind = redactor.Redact(content.Kind)
			content.Text = redactor.Redact(content.Text)
			content.Language = redactor.Redact(content.Language)
		}
	}
	for signatureIndex := range result.Signatures {
		signature := &result.Signatures[signatureIndex]
		signature.Label = redactor.Redact(signature.Label)
		if signature.Documentation != nil {
			signature.Documentation.Kind = redactor.Redact(signature.Documentation.Kind)
			signature.Documentation.Text = redactor.Redact(signature.Documentation.Text)
		}
		for parameterIndex := range signature.Parameters {
			parameter := &signature.Parameters[parameterIndex]
			parameter.Label = redactor.Redact(parameter.Label)
			if parameter.Documentation != nil {
				parameter.Documentation.Kind = redactor.Redact(parameter.Documentation.Kind)
				parameter.Documentation.Text = redactor.Redact(parameter.Documentation.Text)
			}
		}
	}
	for symbolIndex := range result.Symbols {
		redactLSPSymbol(&result.Symbols[symbolIndex], redactor)
	}
}

func redactLSPSymbol(symbol *lspclient.Symbol, redactor *redactor) {
	symbol.Name = redactor.Redact(symbol.Name)
	symbol.KindName = redactor.Redact(symbol.KindName)
	symbol.Path = redactor.Redact(symbol.Path)
	if symbol.Detail != nil {
		value := redactor.Redact(*symbol.Detail)
		symbol.Detail = &value
	}
	if symbol.ContainerName != nil {
		value := redactor.Redact(*symbol.ContainerName)
		symbol.ContainerName = &value
	}
	for index := range symbol.Children {
		redactLSPSymbol(&symbol.Children[index], redactor)
	}
}

func callLSPRequest(ctx context.Context, session *lspclient.Session, file string, request lspRequest, languageID string, result *lspProviderRun) error {
	switch request.Operation {
	case lspHover:
		target, err := lspclient.PrepareTarget(file, uint32(request.Line), uint32(request.Column), languageID)
		if err != nil {
			return err
		}
		result.Hovers, err = session.Hover(ctx, target)
		return err
	case lspSignatureHelp:
		target, err := lspclient.PrepareTarget(file, uint32(request.Line), uint32(request.Column), languageID)
		if err != nil {
			return err
		}
		result.Signatures, err = session.SignatureHelp(ctx, target)
		return err
	case lspDocumentSymbols:
		target, err := lspclient.PrepareDocument(file, languageID)
		if err != nil {
			return err
		}
		result.Symbols, err = session.DocumentSymbols(ctx, target)
		return err
	case lspWorkspaceSymbols:
		var err error
		result.Symbols, err = session.WorkspaceSymbols(ctx, request.Query)
		return err
	default:
		target, err := lspclient.PrepareTarget(file, uint32(request.Line), uint32(request.Column), languageID)
		if err != nil {
			return err
		}
		result.Locations, err = callLSPNavigation(ctx, session, target, request.Operation, request.IncludeDeclaration)
		return err
	}
}

func aggregateLSPRequestResults(request lspRequest, workspace, file string, matches []lspMatch, results []lspProviderRun) (any, *appError) {
	switch request.Operation {
	case lspHover:
		return aggregateLSPHoverResults(request, file, matches, results)
	case lspSignatureHelp:
		return aggregateLSPSignatureResults(request, file, matches, results)
	case lspDocumentSymbols, lspWorkspaceSymbols:
		return aggregateLSPSymbolResults(request, workspace, file, matches, results)
	default:
		return aggregateLSPResults(request.Operation, file, matches, results)
	}
}

func aggregateLSPSignatureResults(request lspRequest, file string, matches []lspMatch, results []lspProviderRun) (any, *appError) {
	outcomes := make([]lspSignatureProviderOutcome, len(matches))
	signatures := make([]lspSignature, 0)
	successes, failures := 0, 0
	var firstErr *appError
	for index, match := range matches {
		outcome := lspSignatureProviderOutcome{Name: match.Definition.Name}
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
			outcome.Signatures = len(results[index].Signatures)
			successes++
			for _, signature := range results[index].Signatures {
				signatures = append(signatures, cliLSPSignature(match.Definition.Name, signature))
			}
		}
		outcomes[index] = outcome
	}
	if successes > 0 {
		return lspSignatureEnvelope{OK: true, LSP: lspSignatureResult{Operation: request.Operation, File: file, Line: request.Line, Column: request.Column, Partial: failures > 0, Signatures: signatures, Providers: outcomes}}, nil
	}
	if firstErr == nil {
		firstErr = protocolError("lsp_capability_unavailable", "no matching LSP provider advertises the requested operation", "configure a matching LSP that supports textDocument/signatureHelp")
	}
	firstErr.details = map[string]any{"providers": outcomes}
	return nil, firstErr
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

func aggregateLSPHoverResults(request lspRequest, file string, matches []lspMatch, results []lspProviderRun) (any, *appError) {
	outcomes := make([]lspHoverProviderOutcome, len(matches))
	hovers := make([]lspHoverEntry, 0)
	successes, failures := 0, 0
	var firstErr *appError
	for index, match := range matches {
		outcome := lspHoverProviderOutcome{Name: match.Definition.Name}
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
			outcome.Hovers = len(results[index].Hovers)
			successes++
			for _, hover := range results[index].Hovers {
				entry := lspHoverEntry{Provider: match.Definition.Name, Contents: make([]lspHoverContent, len(hover.Content))}
				if hover.Range != nil {
					rng := cliLSPRange(*hover.Range)
					entry.Range = &rng
				}
				for contentIndex, content := range hover.Content {
					entry.Contents[contentIndex] = lspHoverContent{Kind: content.Kind, Text: content.Text, Language: content.Language}
				}
				hovers = append(hovers, entry)
			}
		}
		outcomes[index] = outcome
	}
	if successes > 0 {
		return lspHoverEnvelope{OK: true, LSP: lspHoverResult{Operation: request.Operation, File: file, Line: request.Line, Column: request.Column, Partial: failures > 0, Hovers: hovers, Providers: outcomes}}, nil
	}
	if firstErr == nil {
		firstErr = protocolError("lsp_capability_unavailable", "no matching LSP provider advertises the requested operation", "configure a matching LSP that supports textDocument/hover")
	}
	firstErr.details = map[string]any{"providers": outcomes}
	return nil, firstErr
}

func aggregateLSPSymbolResults(request lspRequest, workspace, file string, matches []lspMatch, results []lspProviderRun) (any, *appError) {
	outcomes := make([]lspSymbolProviderOutcome, len(matches))
	symbols := make([]lspSymbol, 0)
	successes, failures := 0, 0
	var firstErr *appError
	for index, match := range matches {
		outcome := lspSymbolProviderOutcome{Name: match.Definition.Name}
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
			outcome.Symbols = len(results[index].Symbols)
			successes++
			for _, symbol := range results[index].Symbols {
				symbols = append(symbols, cliLSPSymbol(match.Definition.Name, symbol))
			}
		}
		outcomes[index] = outcome
	}
	if successes > 0 {
		result := lspSymbolResult{Operation: request.Operation, File: file, Partial: failures > 0, Symbols: symbols, Providers: outcomes}
		if request.Operation == lspWorkspaceSymbols {
			result.File = ""
			result.Workspace = workspace
			result.Query = &request.Query
		}
		return lspSymbolEnvelope{OK: true, LSP: result}, nil
	}
	if firstErr == nil {
		method := "textDocument/documentSymbol"
		if request.Operation == lspWorkspaceSymbols {
			method = "workspace/symbol"
		}
		firstErr = protocolError("lsp_capability_unavailable", "no matching LSP provider advertises the requested operation", "configure an LSP that supports "+method)
	}
	firstErr.details = map[string]any{"providers": outcomes}
	return nil, firstErr
}

func cliLSPRange(value lspclient.Range) lspRange {
	return lspRange{Start: lspPosition{Line: int(value.Start.Line), Column: int(value.Start.Column)}, End: lspPosition{Line: int(value.End.Line), Column: int(value.End.Column)}}
}

func cliLSPSymbol(provider string, value lspclient.Symbol) lspSymbol {
	result := lspSymbol{Provider: provider, Name: value.Name, Kind: int(value.Kind), KindName: value.KindName, Path: value.Path, Range: cliLSPRange(value.Range), Deprecated: value.Deprecated}
	if value.SelectionRange != nil {
		rng := cliLSPRange(*value.SelectionRange)
		result.SelectionRange = &rng
	}
	if value.Detail != nil {
		result.Detail = *value.Detail
	}
	if value.ContainerName != nil {
		result.ContainerName = *value.ContainerName
	}
	if len(value.Children) != 0 {
		result.Children = make([]lspSymbol, len(value.Children))
		for index, child := range value.Children {
			result.Children[index] = cliLSPSymbol(provider, child)
		}
	}
	return result
}

func cliLSPSignature(provider string, value lspclient.Signature) lspSignature {
	result := lspSignature{Provider: provider, Label: value.Label, Active: value.Active, Parameters: make([]lspSignatureParameter, len(value.Parameters))}
	if value.Documentation != nil {
		result.Documentation = &lspSignatureDocumentation{Kind: value.Documentation.Kind, Text: value.Documentation.Text}
	}
	for index, parameter := range value.Parameters {
		result.Parameters[index] = lspSignatureParameter{Label: parameter.Label, Active: parameter.Active}
		if parameter.Documentation != nil {
			result.Parameters[index].Documentation = &lspSignatureDocumentation{Kind: parameter.Documentation.Kind, Text: parameter.Documentation.Text}
		}
	}
	return result
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
		return protocolError("lsp_result_unsupported", err.Error(), "use an LSP server that returns a supported result")
	case errors.Is(err, lspclient.ErrServerRequestUnsupported):
		return protocolError("lsp_server_request_unsupported", err.Error(), "use an LSP server that does not require unsupported client capabilities for this operation")
	case errors.Is(err, jsonrpc2.ErrMethodNotFound):
		return protocolError("lsp_capability_mismatch", "the LSP server advertised support but rejected "+lspProtocolMethod(operation), "check the selected LSP server and workspace configuration")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return transportError("lsp_request_canceled", "the LSP "+operation+" operation was canceled", "retry the request")
	case errors.Is(err, io.EOF), errors.Is(err, net.ErrClosed):
		return transportError("lsp_instance_unavailable", err.Error(), "run wirecmd daemon reload or restart the daemon")
	default:
		return protocolError("lsp_"+strings.ReplaceAll(operation, "-", "_")+"_failed", err.Error(), "check the LSP server diagnostics and workspace configuration")
	}
}

func lspProtocolMethod(operation string) string {
	switch operation {
	case lspWorkspaceSymbols:
		return "workspace/symbol"
	case lspDocumentSymbols:
		return "textDocument/documentSymbol"
	case lspTypeDefinition:
		return "textDocument/typeDefinition"
	case lspSignatureHelp:
		return "textDocument/signatureHelp"
	default:
		return "textDocument/" + operation
	}
}
