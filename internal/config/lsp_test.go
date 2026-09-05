package config

import (
	"strings"
	"testing"
)

func TestParseAndComposeLSPPartialSources(t *testing.T) {
	base, err := ParseString("base.kdl", `wirecmd {
        lsp "primary" {
            scope "workspace"
            language-id "go"
            stdio "gopls" {
                arg "serve"
                env BASE="base"
                env SHARED=(secret)"env://BASE_TOKEN"
            }
        }
        lsp "retained" {
            scope "workspace"
            language-id "text"
            stdio "retained-lsp"
        }
    }`)
	if err != nil {
		t.Fatal(err)
	}
	local, err := ParseString("local.kdl", `wirecmd {
        lsp "primary" {
            language-id "go.local"
            stdio "local-lsp" {
                arg "--stdio"
                env SHARED="local"
                env EMPTY=""
            }
        }
        lsp "added" {
            scope "workspace"
            language-id "markdown"
            stdio "added-lsp"
        }
    }`)
	if err != nil {
		t.Fatal(err)
	}

	config, err := Compose(base, local)
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	if len(config.Servers) != 0 {
		t.Fatalf("server count = %d, want 0", len(config.Servers))
	}
	if got, want := lspNames(config.LSPs), []string{"primary", "retained", "added"}; !sameStrings(got, want) {
		t.Fatalf("LSP order = %#v, want %#v", got, want)
	}

	primary := config.LSPs[0]
	if primary.Scope != ScopeWorkspace || primary.ScopeProvenance != (Provenance{File: "base.kdl", Path: `wirecmd.lsp["primary"].scope`}) {
		t.Fatalf("scope = %#v", primary)
	}
	if primary.LanguageID != "go.local" || primary.LanguageIDProvenance != (Provenance{File: "local.kdl", Path: `wirecmd.lsp["primary"].language-id`}) {
		t.Fatalf("language ID = %#v", primary)
	}
	if primary.Stdio.Command != "local-lsp" || primary.Stdio.CommandProvenance != (Provenance{File: "local.kdl", Path: `wirecmd.lsp["primary"].stdio`}) {
		t.Fatalf("stdio command = %#v", primary.Stdio)
	}
	if got, want := valueTexts(primary.Stdio.Args), []string{"--stdio"}; !sameStrings(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
	if primary.Stdio.Args[0].Provenance != (Provenance{File: "local.kdl", Path: `wirecmd.lsp["primary"].stdio.arg[0]`}) {
		t.Fatalf("argument provenance = %#v", primary.Stdio.Args[0].Provenance)
	}
	if got, want := envNames(primary.Stdio.Env), []string{"BASE", "SHARED", "EMPTY"}; !sameStrings(got, want) {
		t.Fatalf("environment order = %#v, want %#v", got, want)
	}
	if primary.Stdio.Env[0].Value.Text != "base" || primary.Stdio.Env[0].Value.File != "base.kdl" {
		t.Fatalf("inherited environment = %#v", primary.Stdio.Env[0])
	}
	if primary.Stdio.Env[1].Value.Kind != ValueLiteral || primary.Stdio.Env[1].Value.Text != "local" || primary.Stdio.Env[1].Value.File != "local.kdl" {
		t.Fatalf("overridden environment = %#v", primary.Stdio.Env[1])
	}
	if primary.Stdio.Env[2].Value.Text != "" || primary.Stdio.Env[2].Value.File != "local.kdl" {
		t.Fatalf("present-empty environment = %#v", primary.Stdio.Env[2])
	}
}

func TestParseAllowsPartialLSPSources(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "definition only", source: `wirecmd { lsp "primary" }`},
		{name: "scope only", source: `wirecmd { lsp "primary" { scope "workspace" } }`},
		{name: "language only", source: `wirecmd { lsp "primary" { language-id "go" } }`},
		{name: "stdio only", source: `wirecmd { lsp "primary" { stdio { env FLAG="" } } }`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseString("partial.kdl", test.source); err != nil {
				t.Fatalf("ParseString() error = %v", err)
			}
		})
	}
}

func TestLSPValidationUsesEffectiveConfiguration(t *testing.T) {
	base, err := ParseString("base.kdl", `wirecmd { lsp "primary" { scope "workspace"; language-id "go" } }`)
	if err != nil {
		t.Fatal(err)
	}
	local, err := ParseString("local.kdl", `wirecmd { lsp "primary" { stdio "gopls" } }`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compose(base, local); err != nil {
		t.Fatalf("Compose() error = %v", err)
	}

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "missing scope", source: `wirecmd { lsp "primary" { language-id "go"; stdio "gopls" } }`, want: `lsp["primary"].scope: scope is required`},
		{name: "unsupported scope", source: `wirecmd { lsp "primary" { scope "global"; language-id "go"; stdio "gopls" } }`, want: `lsp["primary"].scope: unsupported scope "global"`},
		{name: "missing language", source: `wirecmd { lsp "primary" { scope "workspace"; stdio "gopls" } }`, want: `lsp["primary"].language-id: language-id is required`},
		{name: "missing stdio", source: `wirecmd { lsp "primary" { scope "workspace"; language-id "go" } }`, want: `lsp["primary"].stdio: stdio is required`},
		{name: "missing executable", source: `wirecmd { lsp "primary" { scope "workspace"; language-id "go"; stdio { env FLAG="one" } } }`, want: `lsp["primary"].stdio: executable is required`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := ParseString("invalid.kdl", test.source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Compose(source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Compose() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestParseRejectsInvalidLSPStructure(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "duplicate definition", source: `wirecmd { lsp "primary"; lsp "primary" }`, want: "duplicate lsp"},
		{name: "duplicate language ID", source: `wirecmd { lsp "primary" { language-id "go"; language-id "go" } }`, want: "duplicate language-id"},
		{name: "duplicate stdio", source: `wirecmd { lsp "primary" { stdio "one"; stdio "two" } }`, want: "duplicate stdio"},
		{name: "unknown child", source: `wirecmd { lsp "primary" { http "https://example.test" } }`, want: `unknown child node "http"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseString("invalid.kdl", test.source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseString() error = %v, want %q", err, test.want)
			}
		})
	}
}

func lspNames(values []LSP) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.Name
	}
	return result
}
