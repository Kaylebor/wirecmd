package config

import (
	"strings"
	"testing"
)

func TestParseContextTemplatesAcrossProviderInputs(t *testing.T) {
	source, err := ParseString("templates.kdl", `wirecmd {
        mcp "local" {
            stdio (template)"${wirecmd.global-root}/bin/server" {
                arg (template)"--root=${wirecmd.project-root}"
                env CALLER=(template)"${wirecmd.cwd}"
            }
        }
        mcp "remote" {
            http (template)"https://example.test/${wirecmd.project-root}" {
                query cwd=(template)"${wirecmd.cwd}"
                header X-Global=(template)"${wirecmd.global-root}"
                oauth {
                    client-id (template)"id-${wirecmd.project-root}"
                    client-secret (template)"secret-${wirecmd.cwd}"
                    redirect-uri (template)"http://127.0.0.1:8765/${wirecmd.cwd}"
                }
            }
        }
        lsp "language" {
            selector language-id="go"
            stdio (template)"${wirecmd.global-root}/bin/lsp" {
                arg (template)"--root=${wirecmd.project-root}"
                env CALLER=(template)"${wirecmd.cwd}"
            }
        }
    }`)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Compose(source)
	if err != nil {
		t.Fatal(err)
	}
	local := cfg.Servers[0]
	remote := cfg.Servers[1]
	for name, value := range map[string]Value{
		"command": local.Stdio.Command, "arg": local.Stdio.Args[0], "env": local.Stdio.Env[0].Value,
		"endpoint": remote.HTTP.Endpoint, "query": remote.HTTP.Query[0].Value, "header": remote.HTTP.Headers[0].Value,
		"client-id": remote.HTTP.OAuth.ClientID, "client-secret": *remote.HTTP.OAuth.ClientSecret, "redirect-uri": remote.HTTP.OAuth.RedirectURI,
		"lsp command": cfg.LSPs[0].Stdio.Command, "lsp arg": cfg.LSPs[0].Stdio.Args[0], "lsp env": cfg.LSPs[0].Stdio.Env[0].Value,
	} {
		if value.Kind != ValueTemplate || value.File != "templates.kdl" {
			t.Fatalf("%s = %#v", name, value)
		}
	}
}

func TestTemplateCompositionReplacesLiteralWithoutLosingKind(t *testing.T) {
	base, err := ParseString("base.kdl", `wirecmd { mcp "server" { stdio "plain" { arg "plain" } } }`)
	if err != nil {
		t.Fatal(err)
	}
	strong, err := ParseString("strong.kdl", `wirecmd { mcp "server" { stdio (template)"${wirecmd.global-root}/server" { arg (template)"${wirecmd.cwd}" } } }`)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := Compose(base, strong)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Servers[0].Stdio.Command.Kind != ValueTemplate || cfg.Servers[0].Stdio.Command.File != "strong.kdl" || cfg.Servers[0].Stdio.Args[0].Kind != ValueTemplate {
		t.Fatalf("composed stdio = %#v", cfg.Servers[0].Stdio)
	}
}

func TestTemplatesRejectInvalidAndControlFieldUses(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "no reference", source: `wirecmd { mcp "x" { stdio (template)"plain" } }`, want: "at least one context reference"},
		{name: "unknown", source: `wirecmd { mcp "x" { stdio (template)"${wirecmd.unknown}" } }`, want: "unknown context reference"},
		{name: "malformed", source: `wirecmd { mcp "x" { stdio (template)"$wirecmd.cwd ${wirecmd.project-root}" } }`, want: "malformed context reference"},
		{name: "secret command", source: `wirecmd { mcp "x" { stdio (secret)"env://COMMAND" } }`, want: "unsupported value annotation"},
		{name: "scope", source: `wirecmd { mcp "x" { scope (template)"${wirecmd.cwd}"; stdio "x" } }`, want: "annotation \"template\" is not allowed"},
		{name: "root", source: `wirecmd { root (template)"${wirecmd.cwd}"; mcp "x" { stdio "x" } }`, want: "annotation \"template\" is not allowed"},
		{name: "selector", source: `wirecmd { lsp "x" { selector language-id=(template)"${wirecmd.cwd}"; stdio "x" } }`, want: "annotation \"template\" is not allowed"},
		{name: "implementation", source: `wirecmd { lsp "x" { implementation-id (template)"${wirecmd.cwd}"; selector language-id="go"; stdio "x" } }`, want: "annotation \"template\" is not allowed"},
		{name: "identity", source: `wirecmd { secrets { age { identity (template)"${wirecmd.cwd}" } }; mcp "x" { stdio "x" } }`, want: "annotation \"template\" is not allowed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseString("test.kdl", test.source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseString() error = %v, want %q", err, test.want)
			}
		})
	}
}
