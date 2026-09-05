package cli

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func TestLSPDefinitionGrammar(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		want lspDefinitionRequest
		code string
	}{
		{"valid", []string{"lsp", "definition", "--file", "main.go", "--line", "21", "--column", "13"}, lspDefinitionRequest{File: "main.go", Line: 21, Column: 13}, ""},
		{"equals", []string{"lsp", "definition", "--file=main.go", "--line=21", "--column=13"}, lspDefinitionRequest{File: "main.go", Line: 21, Column: 13}, ""},
		{"missing file", []string{"lsp", "definition", "--line", "21", "--column", "13"}, lspDefinitionRequest{}, "lsp_file_required"},
		{"missing line", []string{"lsp", "definition", "--file", "main.go", "--column", "13"}, lspDefinitionRequest{}, "lsp_line_required"},
		{"missing column", []string{"lsp", "definition", "--file", "main.go", "--line", "21"}, lspDefinitionRequest{}, "lsp_column_required"},
		{"zero", []string{"lsp", "definition", "--file", "main.go", "--line", "0", "--column", "13"}, lspDefinitionRequest{}, "lsp_definition_flags"},
		{"line overflow", []string{"lsp", "definition", "--file", "main.go", "--line", "4294967296", "--column", "13"}, lspDefinitionRequest{}, "lsp_definition_flags"},
		{"column overflow", []string{"lsp", "definition", "--file", "main.go", "--line", "21", "--column", "4294967296"}, lspDefinitionRequest{}, "lsp_definition_flags"},
		{"unknown", []string{"lsp", "definition", "--file", "main.go", "--line", "21", "--column", "13", "--other"}, lspDefinitionRequest{}, "lsp_definition_flags"},
		{"suffix", []string{"lsp", "definition", "--file", "main.go", "--line", "21", "--column", "13", "extra"}, lspDefinitionRequest{}, "lsp_definition_arguments"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err, handled := parseLSPDefinitionCommand(test.args, options{})
			if !handled {
				t.Fatal("command was not recognized")
			}
			if test.code == "" {
				if err != nil || got != test.want {
					t.Fatalf("parse = %#v, %v; want %#v, nil", got, err, test.want)
				}
				return
			}
			if err == nil || err.code != test.code {
				t.Fatalf("error = %#v; want code %q", err, test.code)
			}
		})
	}
}

func TestLSPReservesOnlyBareNativeForms(t *testing.T) {
	for _, test := range []struct {
		name        string
		positionals []string
		opts        options
	}{
		{"json escape", []string{"lsp", "definition"}, options{jsonSet: true}},
		{"stdin escape", []string{"lsp", "definition"}, options{stdin: true}},
		{"exact call", []string{"lsp", `{"tool":"definition"}`}, options{}},
		{"other MCP tool", []string{"lsp", "other"}, options{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, handled := parseLSPDefinitionCommand(test.positionals, test.opts)
			if handled {
				t.Fatalf("%s unexpectedly reserved", test.name)
			}
		})
	}

	if _, _, handled := lspHelp([]string{"lsp"}, options{help: true}); !handled {
		t.Fatal("static lsp help was not recognized")
	}
	if _, _, handled := lspHelp([]string{"lsp", "definition"}, options{help: true}); !handled {
		t.Fatal("static definition help was not recognized")
	}
	if _, _, handled := lspHelp([]string{"lsp"}, options{help: true, helpServer: true}); handled {
		t.Fatal("--help -- must preserve MCP server help")
	}
	if _, err, handled := lspHelp([]string{"lsp"}, options{help: true, jsonSet: true}); !handled || err == nil || err.code != "input_with_help" {
		t.Fatalf("help input conflict = handled:%v err:%#v", handled, err)
	}
}

func TestLSPStaticHelpDoesNotRequireConfiguration(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "relative-path-is-invalid-for-discovery")
	t.Setenv("XDG_RUNTIME_DIR", "/not-an-available-runtime")
	for _, args := range [][]string{{"lsp"}, {"--help", "lsp"}, {"--help", "lsp", "definition"}} {
		code, output, stderr := invoke(t, args)
		if code != exitOK || stderr != "" || json.Valid([]byte(output)) || !strings.Contains(output, "lsp definition") {
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
	config := writeConfig(t, strings.Replace(string(source), `server "helper"`, `server "lsp"`, 1))
	for _, test := range []struct {
		name  string
		args  []string
		input string
		want  string
	}{
		{"exact call", []string{"--direct", "--config", config, "lsp", `{"tool":"a_tool","arguments":{}}`}, "", "a_tool"},
		{"json", []string{"--direct", "--config", config, "--json", `{}`, "lsp", "a_tool"}, "", "a_tool"},
		{"stdin", []string{"--direct", "--config", config, "--stdin", "lsp", "a_tool"}, "{}", "a_tool"},
		{"help separator", []string{"--direct", "--config", config, "--help", "--", "lsp"}, "", "Tools for lsp"},
	} {
		t.Run(test.name, func(t *testing.T) {
			code, output, stderr := invokeWithInput(t, test.args, test.input)
			if code != exitOK || stderr != "" || !strings.Contains(output, test.want) {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, output, stderr)
			}
		})
	}
}

func TestLSPDefinitionDispatchesDistinctRequest(t *testing.T) {
	previous := executeLSPDefinition
	t.Cleanup(func() { executeLSPDefinition = previous })
	var got lspDefinitionRequest
	executeLSPDefinition = func(_ context.Context, opts options, request lspDefinitionRequest, _ io.Reader, _ io.Writer) (any, *appError) {
		if !opts.direct {
			t.Fatal("prefix --direct was not retained")
		}
		got = request
		return lspDefinitionEnvelope{OK: true, LSP: lspDefinitionResult{Operation: "definition", File: "/work/main.go", Locations: []lspLocation{}}}, nil
	}
	code, output, stderr := invoke(t, []string{"--direct", "lsp", "definition", "--file", "main.go", "--line", "21", "--column", "13"})
	if code != exitOK || stderr != "" || got != (lspDefinitionRequest{File: "main.go", Line: 21, Column: 13}) {
		t.Fatalf("code=%d stdout=%q stderr=%q request=%#v", code, output, stderr, got)
	}
	decoded := decodeOutput(t, output)
	lsp, ok := decoded["lsp"].(map[string]any)
	if !ok || lsp["operation"] != "definition" || lsp["file"] != "/work/main.go" {
		t.Fatalf("unexpected LSP envelope: %#v", decoded)
	}
}

func TestLSPEnvelopeUsesGenericPrettyJSON(t *testing.T) {
	value := lspDefinitionEnvelope{OK: true, LSP: lspDefinitionResult{
		Operation: "definition",
		File:      "/work/main.go",
		Locations: []lspLocation{{Path: "/work/target.go", Range: lspRange{Start: lspPosition{Line: 47, Column: 6}, End: lspPosition{Line: 47, Column: 9}}}},
	}}
	var output strings.Builder
	writeOutput(&output, value, presentation{pretty: true})
	if !strings.Contains(output.String(), "\n  \"lsp\":") || !strings.Contains(output.String(), "\"locations\": [") {
		t.Fatalf("LSP result did not use generic pretty JSON: %q", output.String())
	}
}
