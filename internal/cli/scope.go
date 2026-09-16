package cli

import (
	"os"
	"path/filepath"

	"github.com/Kaylebor/wirecmd/internal/config"
)

const globalScopeOwner = "daemon-global"

type providerRuntimeContext struct {
	Scope config.Scope
	Owner string
	Root  string
}

func normalizedScope(scope config.Scope) config.Scope {
	if scope == "" {
		return config.ScopeWorkspace
	}
	return scope
}

func providerRootForScope(scope config.Scope, context invocationContext) string {
	if normalizedScope(scope) == config.ScopeGlobal {
		return context.GlobalRoot
	}
	return context.ProjectRoot
}

func serverRuntimeContext(server config.Server, context invocationContext) (providerRuntimeContext, *appError) {
	return runtimeContextForScope(server.Scope, context, server.HTTP == nil)
}

func lspRuntimeContext(definition config.LSP, context invocationContext) (providerRuntimeContext, *appError) {
	return runtimeContextForScope(definition.Scope, context, true)
}

func runtimeContextForScope(scope config.Scope, context invocationContext, requireGlobalDirectory bool) (providerRuntimeContext, *appError) {
	switch scope {
	case "", config.ScopeWorkspace:
		return providerRuntimeContext{Scope: config.ScopeWorkspace, Owner: context.ProjectRoot, Root: context.ProjectRoot}, nil
	case config.ScopeGlobal:
		root := context.GlobalRoot
		if requireGlobalDirectory {
			info, err := os.Stat(root)
			if root == "" || err != nil || !info.IsDir() {
				return providerRuntimeContext{}, configurationError("global_root_unavailable", "the global Wirecmd root is unavailable", "create the global Wirecmd configuration directory and retry")
			}
			root = canonicalPath(root)
		}
		return providerRuntimeContext{Scope: scope, Owner: globalScopeOwner, Root: rootForProvider(requireGlobalDirectory, root)}, nil
	default:
		return providerRuntimeContext{}, configurationError("scope_unsupported", "the configured provider scope is unsupported", "use workspace or global scope")
	}
}

func rootForProvider(required bool, root string) string {
	if required {
		return filepath.Clean(root)
	}
	return ""
}
