package config

import (
	"strings"
	"testing"
)

func TestParseAndComposeLSPSelectors(t *testing.T) {
	base, err := ParseString("base.kdl", `wirecmd {
        lsp "primary" {
            implementation-id "base-implementation"
            selector language-id="go" pattern="**/*.go"
            selector language-id="text"
            stdio "gopls" {
                arg "serve"
                env BASE="base"
                env SHARED=(secret)"env://BASE_TOKEN"
            }
        }
        lsp "retained" {
            scope "workspace"
            selector language-id="text"
            stdio "retained-lsp"
        }
    }`)
	if err != nil {
		t.Fatal(err)
	}
	local, err := ParseString("local.kdl", `wirecmd {
        lsp "primary" {
            implementation-id "local-implementation"
            selector language-id="go.local" pattern="cmd/**/*.go"
            stdio "local-lsp" {
                arg "--stdio"
                env SHARED="local"
                env EMPTY=""
            }
        }
        lsp "added" {
            selector language-id="markdown"
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
	if got, want := lspNames(config.LSPs), []string{"primary", "retained", "added"}; !sameStrings(got, want) {
		t.Fatalf("LSP order = %#v, want %#v", got, want)
	}

	primary := config.LSPs[0]
	if primary.Scope != ScopeWorkspace || primary.ScopeProvenance != (Provenance{File: "local.kdl", Path: `wirecmd.lsp["primary"]`}) {
		t.Fatalf("default scope = %#v", primary)
	}
	if primary.ImplementationID != "local-implementation" || primary.ImplementationIDProvenance != (Provenance{File: "local.kdl", Path: `wirecmd.lsp["primary"].implementation-id`}) {
		t.Fatalf("implementation ID = %#v", primary)
	}
	if len(primary.Selectors) != 1 {
		t.Fatalf("selectors = %#v, want one replacement selector", primary.Selectors)
	}
	selector := primary.Selectors[0]
	if selector.LanguageID != "go.local" || selector.Pattern != "cmd/**/*.go" {
		t.Fatalf("selector = %#v", selector)
	}
	if selector.LanguageIDProvenance != (Provenance{File: "local.kdl", Path: `wirecmd.lsp["primary"].selector[0].language-id`}) || selector.PatternProvenance != (Provenance{File: "local.kdl", Path: `wirecmd.lsp["primary"].selector[0].pattern`}) {
		t.Fatalf("selector provenance = %#v", selector)
	}
	if primary.Stdio.Command != "local-lsp" || primary.Stdio.CommandProvenance != (Provenance{File: "local.kdl", Path: `wirecmd.lsp["primary"].stdio`}) {
		t.Fatalf("stdio command = %#v", primary.Stdio)
	}
	if got, want := valueTexts(primary.Stdio.Args), []string{"--stdio"}; !sameStrings(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
	if got, want := envNames(primary.Stdio.Env), []string{"BASE", "SHARED", "EMPTY"}; !sameStrings(got, want) {
		t.Fatalf("environment order = %#v, want %#v", got, want)
	}
	if primary.Stdio.Env[0].Value.Text != "base" || primary.Stdio.Env[0].Value.File != "base.kdl" {
		t.Fatalf("inherited environment = %#v", primary.Stdio.Env[0])
	}
	if primary.Stdio.Env[1].Value.Text != "local" || primary.Stdio.Env[1].Value.File != "local.kdl" {
		t.Fatalf("overridden environment = %#v", primary.Stdio.Env[1])
	}
	if primary.Stdio.Env[2].Value.Text != "" || primary.Stdio.Env[2].Value.File != "local.kdl" {
		t.Fatalf("present-empty environment = %#v", primary.Stdio.Env[2])
	}

	retained := config.LSPs[1]
	if retained.Scope != ScopeWorkspace || retained.ScopeProvenance != (Provenance{File: "base.kdl", Path: `wirecmd.lsp["retained"].scope`}) {
		t.Fatalf("explicit scope = %#v", retained)
	}
}

func TestParseAllowsPartialLSPSources(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "definition only", source: `wirecmd { lsp "primary" }`},
		{name: "implementation only", source: `wirecmd { lsp "primary" { implementation-id "example" } }`},
		{name: "selector only", source: `wirecmd { lsp "primary" { selector language-id="go" } }`},
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
	base, err := ParseString("base.kdl", `wirecmd {
        lsp "primary" {
            selector language-id="go"
        }
    }`)
	if err != nil {
		t.Fatal(err)
	}
	local, err := ParseString("local.kdl", `wirecmd {
        lsp "primary" { stdio "gopls" }
    }`)
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
		{name: "missing selector", source: `wirecmd { lsp "primary" { stdio "gopls" } }`, want: `lsp["primary"].selector: at least one selector is required`},
		{name: "empty implementation ID", source: `wirecmd { lsp "primary" { implementation-id ""; selector language-id="go"; stdio "gopls" } }`, want: ".implementation-id: implementation-id must not be empty"},
		{name: "empty language ID", source: `wirecmd { lsp "primary" { selector language-id=""; stdio "gopls" } }`, want: ".language-id: language-id is required"},
		{name: "empty pattern", source: `wirecmd { lsp "primary" { selector language-id="go" pattern=""; stdio "gopls" } }`, want: ".pattern: pattern must not be empty"},
		{name: "invalid pattern", source: `wirecmd { lsp "primary" { selector language-id="go" pattern="["; stdio "gopls" } }`, want: ".pattern: invalid pattern"},
		{name: "missing stdio", source: `wirecmd { lsp "primary" { selector language-id="go" } }`, want: `lsp["primary"].stdio: stdio is required`},
		{name: "missing executable", source: `wirecmd { lsp "primary" { selector language-id="go"; stdio { env FLAG="one" } } }`, want: `lsp["primary"].stdio: executable is required`},
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

func TestLSPInvalidWeakerSelectorCanBeReplaced(t *testing.T) {
	weaker, err := ParseString("weaker.kdl", `wirecmd { lsp "primary" { selector language-id="go" pattern="["; stdio "gopls" } }`)
	if err != nil {
		t.Fatal(err)
	}
	stronger, err := ParseString("stronger.kdl", `wirecmd { lsp "primary" { selector language-id="go" pattern="**/*.go" } }`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compose(weaker, stronger); err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
}

func TestParseUsesMCPKeywordAndRejectsServerKeyword(t *testing.T) {
	source, err := ParseString("mcp.kdl", `wirecmd { mcp "memory" { scope "workspace"; stdio "memory" } }`)
	if err != nil {
		t.Fatal(err)
	}
	config, err := Compose(source)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := config.Servers[0].ScopeProvenance.Path, `wirecmd.mcp["memory"].scope`; got != want {
		t.Fatalf("scope provenance path = %q, want %q", got, want)
	}

	if _, err := ParseString("server.kdl", `wirecmd { server "memory" { scope "workspace"; stdio "memory" } }`); err == nil || !strings.Contains(err.Error(), `unknown child node "server"`) {
		t.Fatalf("server keyword error = %v", err)
	}
}

func lspNames(values []LSP) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.Name
	}
	return result
}
