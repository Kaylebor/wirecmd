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
	if _, _, handled := lspHelp([]string{"lsp"}, options{help: true, helpServer: true}); handled {
		t.Fatal("--help -- must preserve MCP server help")
	}
}

func TestLSPStaticHelpDoesNotRequireConfiguration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-path-is-invalid-for-discovery")
	t.Setenv("XDG_RUNTIME_DIR", "/not-an-available-runtime")
	for _, args := range [][]string{{"lsp"}, {"--help", "lsp"}, {"--help", "lsp", "definition"}, {"--help", "lsp", "status"}} {
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
		{"--direct", "--config", configPath, "lsp", `{"tool":"a_tool","arguments":{}}`},
		{"--direct", "--config", configPath, "--json", `{}`, "lsp", "a_tool"},
		{"--direct", "--config", configPath, "--help", "--", "lsp"},
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
