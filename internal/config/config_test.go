package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseAndComposeOneSource(t *testing.T) {
	sourceText, err := os.ReadFile(filepath.Join("testdata", "valid.kdl"))
	if err != nil {
		t.Fatal(err)
	}

	source, err := ParseString("testdata/valid.kdl", string(sourceText))
	if err != nil {
		t.Fatalf("ParseString() error = %v", err)
	}
	config, err := Compose(source)
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	if config.Root == nil || config.Root.Path != "/workspace" || config.Root.Provenance != (Provenance{File: "testdata/valid.kdl", Path: "wirecmd.root"}) {
		t.Fatalf("root = %#v", config.Root)
	}
	if len(config.Servers) != 1 {
		t.Fatalf("server count = %d, want 1", len(config.Servers))
	}

	server := config.Servers[0]
	if server.Name != "memory" || server.Scope != ScopeWorkspace {
		t.Fatalf("server = %#v", server)
	}
	if server.ScopeProvenance != (Provenance{File: "testdata/valid.kdl", Path: `wirecmd.server["memory"].scope`}) {
		t.Fatalf("scope provenance = %#v", server.ScopeProvenance)
	}
	if server.Stdio.Command != "go" || server.Stdio.CommandProvenance != (Provenance{File: "testdata/valid.kdl", Path: `wirecmd.server["memory"].stdio`}) {
		t.Fatalf("command = %#v", server.Stdio)
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

func TestParseAllowsPartialSources(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "root only", source: `wirecmd { root "/workspace" }`},
		{name: "server only", source: `wirecmd { server "memory" }`},
		{name: "scope only", source: `wirecmd { server "memory" { scope "workspace"; } }`},
		{name: "stdio only", source: `wirecmd { server "memory" { stdio { env FLAG="" } } }`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParseString("partial.kdl", test.source); err != nil {
				t.Fatalf("ParseString() error = %v", err)
			}
		})
	}
}

func TestComposeMergesPartialSourcesAndRetainsProvenance(t *testing.T) {
	base, err := ParseString("base.kdl", `wirecmd {
        root "/base"
        server "memory" {
            scope "workspace"
            stdio "go" {
                arg "run"
                arg "./memory"
                env BASE="base"
                env SHARED="base"
            }
        }
		server "keep" { scope "workspace"; stdio "keep" }
    }`)
	if err != nil {
		t.Fatal(err)
	}
	local, err := ParseString("local.kdl", `wirecmd {
        root "/local"
        server "memory" {
            stdio "memory-local" {
                arg "serve"
                env SHARED="local"
                env EMPTY=""
            }
        }
		server "added" { scope "workspace"; stdio "added" { env FIRST="one" } }
    }`)
	if err != nil {
		t.Fatal(err)
	}

	config, err := Compose(base, local)
	if err != nil {
		t.Fatalf("Compose() error = %v", err)
	}
	if config.Root == nil || config.Root.Path != "/local" || config.Root.File != "local.kdl" {
		t.Fatalf("root = %#v", config.Root)
	}
	if got, want := serverNames(config.Servers), []string{"memory", "keep", "added"}; !sameStrings(got, want) {
		t.Fatalf("server order = %#v, want %#v", got, want)
	}

	memory := config.Servers[0]
	if memory.Scope != ScopeWorkspace || memory.ScopeProvenance.File != "base.kdl" {
		t.Fatalf("scope = %#v", memory)
	}
	if memory.Stdio.Command != "memory-local" || memory.Stdio.CommandProvenance.File != "local.kdl" {
		t.Fatalf("command = %#v", memory.Stdio)
	}
	if got, want := valueTexts(memory.Stdio.Args), []string{"serve"}; !sameStrings(got, want) {
		t.Fatalf("arguments = %#v, want %#v", got, want)
	}
	if memory.Stdio.Args[0].File != "local.kdl" || memory.Stdio.Args[0].Path != `wirecmd.server["memory"].stdio.arg[0]` {
		t.Fatalf("argument provenance = %#v", memory.Stdio.Args[0].Provenance)
	}
	if got, want := envNames(memory.Stdio.Env), []string{"BASE", "SHARED", "EMPTY"}; !sameStrings(got, want) {
		t.Fatalf("environment order = %#v, want %#v", got, want)
	}
	if memory.Stdio.Env[0].Value.File != "base.kdl" || memory.Stdio.Env[1].Value.File != "local.kdl" || memory.Stdio.Env[2].Value.Text != "" {
		t.Fatalf("environment values = %#v", memory.Stdio.Env)
	}
}

func TestComposeUsesSameOrderForExplicitAndDiscoverySources(t *testing.T) {
	global, err := ParseString("global.kdl", `wirecmd { server "memory" { scope "workspace"; stdio "global" } }`)
	if err != nil {
		t.Fatal(err)
	}
	project, err := ParseString("project.kdl", `wirecmd { server "memory" { stdio "project" } }`)
	if err != nil {
		t.Fatal(err)
	}

	explicit, err := Compose(global, project)
	if err != nil {
		t.Fatal(err)
	}
	discoveryShaped, err := Compose(global, project)
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Servers[0].Stdio.Command != discoveryShaped.Servers[0].Stdio.Command || explicit.Servers[0].Stdio.CommandProvenance != discoveryShaped.Servers[0].Stdio.CommandProvenance {
		t.Fatalf("composition differs: explicit=%#v discovery=%#v", explicit, discoveryShaped)
	}
}

func TestComposeValidatesOnlyEffectiveConfiguration(t *testing.T) {
	rootOnly, err := ParseString("root.kdl", `wirecmd { root "/workspace" }`)
	if err != nil {
		t.Fatal(err)
	}
	serverOnly, err := ParseString("server.kdl", `wirecmd { server "memory" { scope "workspace"; } }`)
	if err != nil {
		t.Fatal(err)
	}
	stdioOnly, err := ParseString("stdio.kdl", `wirecmd { server "memory" { stdio "memory" } }`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compose(rootOnly, serverOnly, stdioOnly); err != nil {
		t.Fatalf("Compose() error = %v", err)
	}

	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "no server", source: `wirecmd { root "/workspace" }`, want: "expected at least one server"},
		{name: "missing scope", source: `wirecmd { server "memory" { stdio "memory" } }`, want: `server["memory"].scope: scope is required`},
		{name: "missing stdio", source: `wirecmd { server "memory" { scope "workspace"; } }`, want: `server["memory"].stdio: stdio is required`},
		{name: "missing executable", source: `wirecmd { server "memory" { scope "workspace"; stdio { env FLAG="one" } } }`, want: `server["memory"].stdio: executable is required`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, err := ParseString("partial.kdl", test.source)
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

func TestComposeAllowsInvalidWeakerScalarsToBeOverridden(t *testing.T) {
	weaker, err := ParseString("weaker.kdl", `wirecmd {
        root ""
        server "memory" { scope "unsupported"; stdio "memory" }
    }`)
	if err != nil {
		t.Fatalf("ParseString(weaker) error = %v", err)
	}
	stronger, err := ParseString("stronger.kdl", `wirecmd {
        root "/workspace"
        server "memory" { scope "workspace" }
    }`)
	if err != nil {
		t.Fatalf("ParseString(stronger) error = %v", err)
	}
	if _, err := Compose(weaker, stronger); err != nil {
		t.Fatalf("Compose() error = %v", err)
	}

	_, err = Compose(weaker)
	if err == nil || !strings.Contains(err.Error(), `weaker.kdl: wirecmd.root: path must not be empty`) {
		t.Fatalf("Compose(weaker) error = %v", err)
	}
	invalidScope, err := ParseString("invalid-scope.kdl", `wirecmd { server "memory" { scope "unsupported"; stdio "memory" } }`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Compose(invalidScope); err == nil || !strings.Contains(err.Error(), `invalid-scope.kdl: wirecmd.server["memory"].scope: unsupported scope "unsupported"`) {
		t.Fatalf("Compose(invalidScope) error = %v", err)
	}
}

func TestLoadEffectiveUsesOrderedPathsAndIdentifiesFailures(t *testing.T) {
	directory := t.TempDir()
	basePath := filepath.Join(directory, "base.kdl")
	localPath := filepath.Join(directory, "local.kdl")
	if err := os.WriteFile(basePath, []byte(`wirecmd { server "memory" { scope "workspace"; stdio "base" } }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localPath, []byte(`wirecmd { server "memory" { stdio "local" } }`), 0o600); err != nil {
		t.Fatal(err)
	}

	config, err := LoadEffective([]string{basePath, localPath})
	if err != nil {
		t.Fatalf("LoadEffective() error = %v", err)
	}
	if got := config.Servers[0].Stdio.Command; got != "local" {
		t.Fatalf("command = %q, want local", got)
	}
	if _, err := LoadEffective(nil); err == nil || !strings.Contains(err.Error(), "at least one config file") {
		t.Fatalf("LoadEffective(nil) error = %v", err)
	}
	missingPath := filepath.Join(directory, "missing.kdl")
	if _, err := LoadEffective([]string{basePath, missingPath}); err == nil || !strings.Contains(err.Error(), missingPath) {
		t.Fatalf("LoadEffective(missing) error = %v, want source path", err)
	}
	invalidPath := filepath.Join(directory, "invalid.kdl")
	if err := os.WriteFile(invalidPath, []byte(`wirecmd {`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEffective([]string{basePath, invalidPath}); err == nil || !strings.Contains(err.Error(), invalidPath) {
		t.Fatalf("LoadEffective(invalid) error = %v, want source path", err)
	}
}

func TestParseRejectsMalformedAndUnknownStructure(t *testing.T) {
	tests := []struct {
		name   string
		source string
		want   string
	}{
		{name: "unknown root child", source: `wirecmd { transport "http" }`, want: `unknown child node "transport"`},
		{name: "duplicate configured root", source: `wirecmd { root "/one"; root "/two" }`, want: "duplicate root"},
		{name: "unknown server child", source: `wirecmd { server "memory" { http "https://example.test" } }`, want: `unknown child node "http"`},
		{name: "duplicate environment", source: `wirecmd { server "memory" { stdio "go" { env TOKEN="one"; env TOKEN="two" } } }`, want: "duplicate environment name"},
		{name: "invalid KDL 2", source: `wirecmd {`, want: "parse KDL 2:"},
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
				source = `wirecmd { server "memory" { stdio ` + test.value + ` } }`
			} else {
				source = `wirecmd { server "memory" { stdio "go" { env TOKEN=` + test.value + ` } } }`
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
	_, err := ParseString("test.kdl", `wirecmd { server "memory" { stdio "go" { env FLAG=true } } }`)
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

func serverNames(values []Server) []string {
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
