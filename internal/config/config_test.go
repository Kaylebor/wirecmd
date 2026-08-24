package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseValidConfig(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("testdata", "valid.kdl"))
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := ParseString("testdata/valid.kdl", string(source))
	if err != nil {
		t.Fatalf("ParseString() error = %v", err)
	}
	if cfg.Provenance != (Provenance{File: "testdata/valid.kdl", Path: "wirecmd"}) {
		t.Fatalf("config provenance = %#v", cfg.Provenance)
	}
	if cfg.Root == nil || cfg.Root.Path != "/workspace" || cfg.Root.Provenance != (Provenance{File: "testdata/valid.kdl", Path: "wirecmd.root"}) {
		t.Fatalf("root = %#v", cfg.Root)
	}
	if len(cfg.Servers) != 1 {
		t.Fatalf("server count = %d, want 1", len(cfg.Servers))
	}

	server := cfg.Servers[0]
	if server.Name != "memory" || server.Scope != ScopeWorkspace {
		t.Fatalf("server = %#v", server)
	}
	if server.Stdio.Command != "go" {
		t.Fatalf("command = %q, want go", server.Stdio.Command)
	}
	if got, want := valueTexts(server.Stdio.Args), []string{"run", "./cmd/memory"}; !sameStrings(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
	if got, want := envNames(server.Stdio.Env), []string{"PLAIN", "TOKEN"}; !sameStrings(got, want) {
		t.Fatalf("environment order = %#v, want %#v", got, want)
	}
	if plain := server.Stdio.Env[0].Value; plain.Kind != ValueLiteral || plain.Text != "literal" || plain.IsSecret() {
		t.Fatalf("plain value = %#v", plain)
	}
	secret := server.Stdio.Env[1].Value
	if secret.Kind != ValueSecretReference || secret.Text != "env://MEMORY_TOKEN" || !secret.IsSecret() {
		t.Fatalf("secret value = %#v", secret)
	}
	if secret.Provenance != (Provenance{File: "testdata/valid.kdl", Path: `wirecmd.server["memory"].stdio.env["TOKEN"]`}) {
		t.Fatalf("secret provenance = %#v", secret.Provenance)
	}
}

func TestParseAllowsNoConfiguredRoot(t *testing.T) {
	cfg, err := ParseString("test.kdl", `wirecmd {
        server "memory" {
            scope "workspace"
            stdio "go"
        }
    }`)
	if err != nil {
		t.Fatalf("ParseString() error = %v", err)
	}
	if cfg.Root != nil {
		t.Fatalf("root = %#v, want nil", cfg.Root)
	}
}

func TestParseRejectsMalformedAndUnknownStructure(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{
			name:   "unknown root child",
			source: `wirecmd { transport "http" }`,
			want:   `unknown child node "transport"`,
		},
		{
			name: "duplicate configured root",
			source: `wirecmd {
                root "/one"
                root "/two"
                server "memory" {
                    scope "workspace"
                    stdio "go"
                }
            }`,
			want: "duplicate root",
		},
		{
			name: "missing scope",
			source: `wirecmd { server "memory" {
                stdio "go"
            } }`,
			want: "scope and stdio are required",
		},
		{
			name: "unknown server child",
			source: `wirecmd { server "memory" {
                scope "workspace"
                http "https://example.test"
            } }`,
			want: `unknown child node "http"`,
		},
		{
			name: "duplicate environment",
			source: `wirecmd { server "memory" {
                scope "workspace"
                stdio "go" {
                    env TOKEN="one"
                    env TOKEN="two"
                }
            } }`,
			want: "duplicate environment name",
		},
		{
			name:   "invalid KDL 2",
			source: `wirecmd {`,
			want:   "parse KDL 2:",
		},
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

func TestParseSecretReferences(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "unknown annotation", value: `(runtime)"TOKEN"`, want: "unsupported value annotation"},
		{name: "unqualified secret", value: `(secret)"TOKEN"`, want: "secret reference must be env://NAME"},
		{name: "empty environment name", value: `(secret)"env://"`, want: "secret reference must be env://NAME"},
		{name: "secret command", value: `(secret)"env://COMMAND"`, want: "value annotation"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var source string
			if test.name == "secret command" {
				source = `wirecmd { server "memory" {
                    scope "workspace"
                    stdio ` + test.value + `
                } }`
			} else {
				source = `wirecmd { server "memory" {
                    scope "workspace"
                    stdio "go" { env TOKEN=` + test.value + ` }
                } }`
			}
			_, err := ParseString("test.kdl", source)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("ParseString() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestResolveEnvDistinguishesMissingAndEmpty(t *testing.T) {
	secret := Value{Kind: ValueSecretReference, Text: "env://TOKEN", Provenance: Provenance{Path: `wirecmd.server["memory"].stdio.env["TOKEN"]`}}

	empty, err := secret.ResolveEnv(func(name string) (string, bool) {
		if name != "TOKEN" {
			t.Fatalf("lookup name = %q", name)
		}
		return "", true
	})
	if err != nil {
		t.Fatalf("ResolveEnv(empty) error = %v", err)
	}
	if empty.Text != "" || !empty.Sensitive {
		t.Fatalf("ResolveEnv(empty) = %#v", empty)
	}

	_, err = secret.ResolveEnv(func(string) (string, bool) { return "", false })
	if err == nil || !strings.Contains(err.Error(), "is not set") {
		t.Fatalf("ResolveEnv(missing) error = %v", err)
	}
}

func TestParseUsesKDL2Only(t *testing.T) {
	_, err := ParseString("test.kdl", `wirecmd { server "memory" {
        scope "workspace"
        stdio "go" { env FLAG=true }
    } }`)
	if err == nil {
		t.Fatal("ParseString() error = nil, want KDL 2 rejection for bare true")
	}
}

func valueTexts(values []Value) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.Text
	}
	return result
}

func envNames(values []Environment) []string {
	result := make([]string, len(values))
	for i, value := range values {
		result[i] = value.Name
	}
	return result
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
