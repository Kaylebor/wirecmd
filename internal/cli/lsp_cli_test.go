package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/lspclient"
)

func TestLSPNavigationGrammar(t *testing.T) {
	for _, operation := range []string{lspDefinition, lspDeclaration, lspTypeDefinition, lspImplementation, lspReferences} {
		t.Run(operation, func(t *testing.T) {
			args := []string{"lsp", operation, "--file", "main.go", "--line", "21", "--column", "13"}
			if operation == lspReferences {
				args = append(args, "--include-declaration")
			}
			got, appErr, handled := parseLSPCommand(args, options{})
			if !handled || appErr != nil || got.Operation != operation || got.File != "main.go" || got.Line != 21 || got.Column != 13 || got.IncludeDeclaration != (operation == lspReferences) {
				t.Fatalf("parse = %#v, %#v, %v", got, appErr, handled)
			}
		})
	}
	for _, test := range []struct {
		args []string
		code string
	}{
		{[]string{"lsp", "definition", "--line", "1", "--column", "1"}, "lsp_file_required"},
		{[]string{"lsp", "definition", "--file", "x", "--column", "1"}, "lsp_line_required"},
		{[]string{"lsp", "definition", "--file", "x", "--line", "1"}, "lsp_column_required"},
		{[]string{"lsp", "definition", "--file", "x", "--line", "0", "--column", "1"}, "lsp_definition_flags"},
		{[]string{"lsp", "status", "extra"}, "lsp_status_arguments"},
	} {
		_, appErr, handled := parseLSPCommand(test.args, options{})
		if !handled || appErr == nil || appErr.code != test.code {
			t.Fatalf("%v: handled=%v err=%#v", test.args, handled, appErr)
		}
	}
	status, appErr, handled := parseLSPCommand([]string{"lsp", "status", "--file", "main.go"}, options{})
	if !handled || appErr != nil || status.Operation != lspStatus || status.File != "main.go" {
		t.Fatalf("status parse = %#v %#v %v", status, appErr, handled)
	}
}

func TestLSPInspectionGrammar(t *testing.T) {
	hover, appErr, handled := parseLSPCommand([]string{"lsp", "hover", "--file", "main.go", "--line", "2", "--column", "3"}, options{})
	if !handled || appErr != nil || hover.Operation != lspHover || hover.File != "main.go" || hover.Line != 2 || hover.Column != 3 {
		t.Fatalf("hover parse = %#v %#v %v", hover, appErr, handled)
	}
	signature, appErr, handled := parseLSPCommand([]string{"lsp", "signature-help", "--file", "main.go", "--line", "2", "--column", "3"}, options{})
	if !handled || appErr != nil || signature.Operation != lspSignatureHelp || signature.File != "main.go" || signature.Line != 2 || signature.Column != 3 {
		t.Fatalf("signature-help parse = %#v %#v %v", signature, appErr, handled)
	}
	document, appErr, handled := parseLSPCommand([]string{"lsp", "document-symbols", "--file", "main.go"}, options{})
	if !handled || appErr != nil || document.Operation != lspDocumentSymbols || document.File != "main.go" {
		t.Fatalf("document symbols parse = %#v %#v %v", document, appErr, handled)
	}
	workspace, appErr, handled := parseLSPCommand([]string{"lsp", "workspace-symbols", "--query", ""}, options{})
	if !handled || appErr != nil || workspace.Operation != lspWorkspaceSymbols || !workspace.QuerySet || workspace.Query != "" {
		t.Fatalf("workspace symbols parse = %#v %#v %v", workspace, appErr, handled)
	}
	for _, test := range []struct {
		args []string
		code string
	}{
		{[]string{"lsp", "hover", "--file", "x", "--line", "1"}, "lsp_column_required"},
		{[]string{"lsp", "signature-help", "--file", "x", "--line", "1"}, "lsp_column_required"},
		{[]string{"lsp", "document-symbols"}, "lsp_file_required"},
		{[]string{"lsp", "document-symbols", "--file", "x", "--line", "1"}, "lsp_document_symbols_flags"},
		{[]string{"lsp", "workspace-symbols"}, "lsp_query_required"},
	} {
		_, got, handled := parseLSPCommand(test.args, options{})
		if !handled || got == nil || got.code != test.code {
			t.Fatalf("%v: handled=%v err=%#v", test.args, handled, got)
		}
	}
}

func TestLSPReservesOnlyBareNativeForms(t *testing.T) {
	for _, test := range []struct {
		positionals []string
		opts        options
	}{
		{[]string{"lsp", "definition"}, options{jsonSet: true}},
		{[]string{"lsp", "definition"}, options{stdin: true}},
		{[]string{"lsp", `{"tool":"definition"}`}, options{}},
		{[]string{"lsp", "other"}, options{}},
	} {
		if _, _, handled := parseLSPCommand(test.positionals, test.opts); handled {
			t.Fatalf("%v unexpectedly reserved", test.positionals)
		}
	}
	if _, _, handled := lspHelp([]string{"lsp"}, options{help: true}); !handled {
		t.Fatal("static lsp help was not recognized")
	}
	if _, _, handled := lspHelp([]string{"lsp", "references"}, options{help: true}); !handled {
		t.Fatal("focused references help was not recognized")
	}
	if _, _, handled := lspHelp([]string{"lsp", "workspace-symbols"}, options{help: true}); !handled {
		t.Fatal("focused workspace symbols help was not recognized")
	}
	if _, _, handled := lspHelp([]string{"lsp", "signature-help"}, options{help: true}); !handled {
		t.Fatal("focused signature-help help was not recognized")
	}
}

func TestLSPStaticHelpDoesNotRequireConfiguration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-path-is-invalid-for-discovery")
	t.Setenv("XDG_RUNTIME_DIR", "/not-an-available-runtime")
	for _, args := range [][]string{{"lsp"}, {"--help", "lsp"}, {"--help", "lsp", "definition"}, {"--help", "lsp", "signature-help"}, {"--help", "lsp", "status"}} {
		code, output, stderr := invoke(t, args)
		if code != exitOK || stderr != "" || json.Valid([]byte(output)) || !strings.Contains(output, "lsp") {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, output, stderr)
		}
	}
}

func TestLSPMCPServerEscapesRemainReachable(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	source, err := os.ReadFile(helperConfig(t, "", ""))
	if err != nil {
		t.Fatal(err)
	}
	configPath := writeConfig(t, strings.Replace(string(source), `mcp "helper"`, `mcp "lsp"`, 1))
	for _, args := range [][]string{
		{"--direct", "--config", configPath, "mcp", "lsp", `{"tool":"a_tool","arguments":{}}`},
		{"--direct", "--config", configPath, "--json", `{}`, "mcp", "lsp", "tool", "a_tool"},
		{"--direct", "--config", configPath, "--help", "mcp", "lsp"},
	} {
		code, output, stderr := invoke(t, args)
		if code != exitOK || stderr != "" || !strings.Contains(output, "lsp") {
			t.Fatalf("%v: code=%d stdout=%q stderr=%q", args, code, output, stderr)
		}
	}
}

func TestLSPDispatchesDistinctRequest(t *testing.T) {
	previous := executeLSPCommand
	t.Cleanup(func() { executeLSPCommand = previous })
	var got lspRequest
	executeLSPCommand = func(_ context.Context, opts options, request lspRequest, _ io.Reader, _ io.Writer) (any, *appError) {
		if !opts.direct {
			t.Fatal("prefix --direct was not retained")
		}
		got = request
		return lspEnvelope{OK: true, LSP: lspResult{Operation: request.Operation, File: "/work/main.go", Locations: []lspLocation{}, Providers: []lspProviderOutcome{}}}, nil
	}
	code, _, stderr := invoke(t, []string{"--direct", "lsp", "implementation", "--file", "main.go", "--line", "21", "--column", "13"})
	if code != exitOK || stderr != "" || got.Operation != lspImplementation || got.File != "main.go" || got.Line != 21 || got.Column != 13 {
		t.Fatalf("code=%d stderr=%q request=%#v", code, stderr, got)
	}
}

func TestMatchLSPDefinitions(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, "src", "main.ts")
	definitions := []config.LSP{
		{Name: "angular", Selectors: []config.LSPSelector{{LanguageID: "typescript", Pattern: "**/*.ts"}}},
		{Name: "typescript", Selectors: []config.LSPSelector{{LanguageID: "typescript", Pattern: "src/**"}}},
		{Name: "html", Selectors: []config.LSPSelector{{LanguageID: "html", Pattern: "**/*.html"}}},
	}
	matches, appErr := matchLSPDefinitions(definitions, workspace, file)
	if appErr != nil || len(matches) != 2 || matches[0].Definition.Name != "angular" || matches[1].Definition.Name != "typescript" {
		t.Fatalf("matches=%#v err=%#v", matches, appErr)
	}
	ambiguous := []config.LSP{{Name: "mixed", Selectors: []config.LSPSelector{{LanguageID: "typescript", Pattern: "**/*"}, {LanguageID: "html", Pattern: "**/*"}}}}
	if _, appErr := matchLSPDefinitions(ambiguous, workspace, file); appErr == nil || appErr.code != "lsp_selector_ambiguous" {
		t.Fatalf("ambiguous error=%#v", appErr)
	}
	if _, appErr := matchLSPDefinitions(definitions, workspace, filepath.Dir(workspace)+"/outside.ts"); appErr == nil || appErr.code != "lsp_no_matching_provider" {
		t.Fatalf("outside error=%#v", appErr)
	}
}

func TestAggregateLSPResults(t *testing.T) {
	matches := []lspMatch{{Definition: config.LSP{Name: "first"}}, {Definition: config.LSP{Name: "second"}}}
	value, appErr := aggregateLSPResults(lspDefinition, "/work/main.go", matches, []lspProviderRun{
		{Locations: []lspclient.Location{{Path: "/work/target.go", Range: lspclient.Range{Start: lspclient.Point{Line: 1, Column: 2}, End: lspclient.Point{Line: 1, Column: 3}}}}},
		{Err: transportError("connection_closed", "closed", "reload")},
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	envelope := value.(lspEnvelope)
	if !envelope.LSP.Partial || len(envelope.LSP.Locations) != 1 || envelope.LSP.Locations[0].Provider != "first" || envelope.LSP.Providers[1].Status != "failed" {
		t.Fatalf("envelope=%#v", envelope)
	}

	_, appErr = aggregateLSPResults(lspDefinition, "/work/main.go", matches, []lspProviderRun{{Unsupported: true}, {Unsupported: true}})
	if appErr == nil || appErr.code != "lsp_capability_unavailable" || len(appErr.details.(map[string]any)["providers"].([]lspProviderOutcome)) != 2 {
		t.Fatalf("unsupported error=%#v", appErr)
	}

	firstFailure := protocolError("first_failure", "first failed", "fix first")
	secondFailure := transportError("second_failure", "second failed", "fix second")
	_, appErr = aggregateLSPResults(lspDefinition, "/work/main.go", matches, []lspProviderRun{{Err: firstFailure}, {Err: secondFailure}})
	if appErr == nil || appErr.code != "first_failure" {
		t.Fatalf("all-failed error=%#v", appErr)
	}
	outcomes := appErr.details.(map[string]any)["providers"].([]lspProviderOutcome)
	if len(outcomes) != 2 || outcomes[0].Status != "failed" || outcomes[1].Error.Code != "second_failure" {
		t.Fatalf("all-failed outcomes=%#v", outcomes)
	}
}

func TestAggregateLSPInspectionResults(t *testing.T) {
	matches := []lspMatch{{Definition: config.LSP{Name: "first"}}, {Definition: config.LSP{Name: "second"}}}
	hoverValue, appErr := aggregateLSPHoverResults(lspRequest{Operation: lspHover, Line: 2, Column: 3}, "/work/main.go", matches, []lspProviderRun{
		{Hovers: []lspclient.Hover{{Content: []lspclient.HoverBlock{{Kind: "markdown", Text: "**value**"}}}}},
		{Err: protocolError("hover_failed", "failed", "retry")},
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	hover := hoverValue.(lspHoverEnvelope)
	if !hover.LSP.Partial || len(hover.LSP.Hovers) != 1 || hover.LSP.Hovers[0].Provider != "first" || hover.LSP.Providers[0].Hovers != 1 || hover.LSP.Providers[1].Status != "failed" {
		t.Fatalf("hover=%#v", hover)
	}

	detail, container := "detail", "container"
	symbolValue, appErr := aggregateLSPSymbolResults(lspRequest{Operation: lspWorkspaceSymbols, Query: "", QuerySet: true}, "/work", "", matches[:1], []lspProviderRun{{Symbols: []lspclient.Symbol{{Name: "Top", Kind: 12, KindName: "function", Path: "/work/main.go", Detail: &detail, ContainerName: &container, Children: []lspclient.Symbol{{Name: "Child", Kind: 13, KindName: "variable", Path: "/work/main.go"}}}}}})
	if appErr != nil {
		t.Fatal(appErr)
	}
	symbols := symbolValue.(lspSymbolEnvelope)
	if symbols.LSP.Query == nil || *symbols.LSP.Query != "" || symbols.LSP.Workspace != "/work" || len(symbols.LSP.Symbols) != 1 || symbols.LSP.Symbols[0].Provider != "first" || symbols.LSP.Symbols[0].Children[0].Provider != "first" {
		t.Fatalf("symbols=%#v", symbols)
	}

	_, appErr = aggregateLSPSymbolResults(lspRequest{Operation: lspDocumentSymbols}, "/work", "/work/main.go", matches, []lspProviderRun{{Unsupported: true}, {Unsupported: true}})
	if appErr == nil || appErr.code != "lsp_capability_unavailable" {
		t.Fatalf("unsupported symbols=%#v", appErr)
	}
}

func TestAggregateLSPSignatureResults(t *testing.T) {
	matches := []lspMatch{{Definition: config.LSP{Name: "first"}}, {Definition: config.LSP{Name: "second"}}}
	value, appErr := aggregateLSPSignatureResults(lspRequest{Operation: lspSignatureHelp, Line: 2, Column: 3}, "/work/main.go", matches, []lspProviderRun{
		{Signatures: []lspclient.Signature{{Label: "first(value)", Active: true, Parameters: []lspclient.SignatureParameter{{Label: "value", Active: true}}}}},
		{Err: protocolError("signature_help_failed", "failed", "retry")},
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	envelope := value.(lspSignatureEnvelope)
	if !envelope.LSP.Partial || len(envelope.LSP.Signatures) != 1 || envelope.LSP.Signatures[0].Provider != "first" || !envelope.LSP.Signatures[0].Parameters[0].Active || envelope.LSP.Providers[0].Signatures != 1 || envelope.LSP.Providers[1].Status != "failed" {
		t.Fatalf("signatures=%#v", envelope)
	}
	_, appErr = aggregateLSPSignatureResults(lspRequest{Operation: lspSignatureHelp}, "/work/main.go", matches, []lspProviderRun{{Unsupported: true}, {Unsupported: true}})
	if appErr == nil || appErr.code != "lsp_capability_unavailable" {
		t.Fatalf("unsupported signatures=%#v", appErr)
	}
}

func TestAllLSPDefinitionsIgnoresSelectors(t *testing.T) {
	definitions := []config.LSP{{Name: "go"}, {Name: "typescript"}}
	matches := allLSPDefinitions(definitions)
	if len(matches) != 2 || matches[0].Definition.Name != "go" || matches[1].Definition.Name != "typescript" || matches[0].LanguageID != "" {
		t.Fatalf("matches=%#v", matches)
	}
}

func TestDaemonRejectsMalformedLSPInspectionFrames(t *testing.T) {
	d := &daemon{}
	cached := &daemonConfig{config: &config.Config{LSPs: []config.LSP{{Name: "fixture"}}}}
	for _, request := range []daemonRequest{
		{Operation: inspectLSP, LSPOperation: lspWorkspaceSymbols},
		{Operation: navigateLSP, LSPOperation: lspHover, LSPFile: "/work/main.go", LSPLine: 1, LSPColumn: 1},
	} {
		reply := d.executeLSP(context.Background(), request, cached, 0, nil)
		if reply.Error == nil || reply.Error.Code != "lsp_request_invalid" || reply.ExitCode != exitInvocation {
			t.Fatalf("request=%#v reply=%#v", request, reply)
		}
	}
}
