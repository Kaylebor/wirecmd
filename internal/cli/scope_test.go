package cli

import (
	"path/filepath"
	"testing"

	"github.com/Kaylebor/wirecmd/internal/config"
)

func TestServerRuntimeContextByScopeAndTransport(t *testing.T) {
	workspace := t.TempDir()
	global := t.TempDir()
	context := invocationContext{ProjectRoot: workspace, GlobalRoot: global}

	workspaceContext, appErr := serverRuntimeContext(config.Server{Scope: config.ScopeWorkspace, Stdio: config.Stdio{Command: "server"}}, context)
	if appErr != nil || workspaceContext.Owner != workspace || workspaceContext.Root != workspace {
		t.Fatalf("workspace context = %#v, %v", workspaceContext, appErr)
	}
	globalStdio, appErr := serverRuntimeContext(config.Server{Scope: config.ScopeGlobal, Stdio: config.Stdio{Command: "server"}}, context)
	if appErr != nil || globalStdio.Owner != globalScopeOwner || globalStdio.Root != canonicalPath(global) {
		t.Fatalf("global stdio context = %#v, %v", globalStdio, appErr)
	}
	globalHTTP, appErr := serverRuntimeContext(config.Server{Scope: config.ScopeGlobal, HTTP: &config.HTTP{Endpoint: "https://example.test"}}, invocationContext{ProjectRoot: workspace, GlobalRoot: filepath.Join(t.TempDir(), "missing")})
	if appErr != nil || globalHTTP.Owner != globalScopeOwner || globalHTTP.Root != "" {
		t.Fatalf("global HTTP context = %#v, %v", globalHTTP, appErr)
	}
}

func TestGlobalStdioAndLSPRequireGlobalRootDirectory(t *testing.T) {
	context := invocationContext{ProjectRoot: t.TempDir(), GlobalRoot: filepath.Join(t.TempDir(), "missing")}
	if _, appErr := serverRuntimeContext(config.Server{Scope: config.ScopeGlobal, Stdio: config.Stdio{Command: "server"}}, context); appErr == nil || appErr.code != "global_root_unavailable" {
		t.Fatalf("global stdio error = %#v", appErr)
	}
	if _, appErr := lspRuntimeContext(config.LSP{Scope: config.ScopeGlobal}, context); appErr == nil || appErr.code != "global_root_unavailable" {
		t.Fatalf("global LSP error = %#v", appErr)
	}
}

func TestGlobalSecretStoresAreBoundToGlobalRoot(t *testing.T) {
	globalRoot := filepath.Join(t.TempDir(), "wirecmd")
	values := []config.Value{{Kind: config.ValueSecretReference, Text: "age://TOKEN"}}
	globalStore := filepath.Join(globalRoot, "secrets.json.age")
	if appErr := validateScopedSecretStores(config.ScopeGlobal, globalRoot, values, []string{globalStore}); appErr != nil {
		t.Fatalf("valid global store: %v", appErr)
	}
	if appErr := validateScopedSecretStores(config.ScopeGlobal, globalRoot, values, []string{filepath.Join(t.TempDir(), "secrets.json.age")}); appErr == nil || appErr.code != "daemon_context_invalid" {
		t.Fatalf("foreign global store error = %#v", appErr)
	}
	if appErr := validateScopedSecretStores(config.ScopeWorkspace, globalRoot, values, []string{filepath.Join(t.TempDir(), "secrets.json.age")}); appErr != nil {
		t.Fatalf("workspace store validation = %v", appErr)
	}
}
