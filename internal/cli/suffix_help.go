package cli

// normalizeTrailingHelp recognizes the deliberately small trailing-help
// grammar. It returns canonical positionals and options for the existing
// offline/help parsers. The caller can invoke it before ordinary request
// parsing; no configuration, daemon, or upstream operation is touched here.
//
// A trailing help token is only special when it is the final, exact token.
// Tool-side suffixes are resolved later against the live tool schema: an
// explicitly projected help property wins, otherwise the token becomes
// focused tool help. Explicit prefix help and JSON input modes keep their
// existing ownership.
func normalizeTrailingHelp(positionals []string, opts options) ([]string, options, *appError, bool) {
	if len(positionals) == 0 || opts.help {
		return positionals, opts, nil, false
	}
	last := positionals[len(positionals)-1]
	if last != "--help" && last != "-h" {
		return positionals, opts, nil, false
	}

	if positionals[0] == "mcp" {
		// In `mcp -- --help`, the final token is the explicitly escaped
		// server alias, not trailing help. A second final help token still
		// works: `mcp -- --help --help`.
		if opts.mcpServerEscaped && len(positionals) == 2 {
			return positionals, opts, nil, false
		}
		// `mcp SERVER tool TOOL --help` remains owned by the live input schema.
		// The shorter forms are namespace/server static or focused help.
		switch len(positionals) {
		case 2, 3:
			canonical := append([]string(nil), positionals[:len(positionals)-1]...)
			updated := opts
			updated.help = true
			return canonical, updated, nil, true
		}
	}

	// JSON and stdin retain ownership of tool calls. The namespace/server
	// suffix forms above are help requests, so they normalize first and then
	// receive the normal input-with-help diagnostic from run.
	if opts.jsonSet || opts.stdin {
		return positionals, opts, nil, false
	}

	// A bare server followed by --help is the documented server-help form.
	// Tool help needs live schema discovery and is therefore left for projected
	// argument resolution below this grammar-only normalization layer.
	if len(positionals) == 2 {
		if isNativeHelpGroup(positionals[0]) {
			return canonicalNativeHelp(positionals[:len(positionals)-1], opts)
		}
		canonical := append([]string(nil), positionals[:len(positionals)-1]...)
		updated := opts
		updated.help = true
		return canonical, updated, nil, true
	}

	if len(positionals) < 2 || !isNativeHelpGroup(positionals[0]) {
		return positionals, opts, nil, false
	}

	return canonicalNativeHelp(positionals[:len(positionals)-1], opts)
}

func isNativeHelpGroup(group string) bool {
	switch group {
	case "daemon", "config", "auth", "lsp":
		return true
	default:
		return false
	}
}

// canonicalNativeHelp strips operation arguments that are meaningful only to
// execution, then leaves the existing static-help functions to validate the
// remaining command grammar. Administrative path/server arguments are kept so
// their existing focused topics remain available.
func canonicalNativeHelp(positionals []string, opts options) ([]string, options, *appError, bool) {
	if len(positionals) == 0 {
		return positionals, opts, nil, false
	}
	canonical := append([]string(nil), positionals...)
	group := canonical[0]

	switch group {
	case "lsp":
		if len(canonical) == 1 {
			// `lsp --help` is the group page.
		} else if isLSPNavigation(canonical[1]) || isLSPInspection(canonical[1]) || canonical[1] == lspStatus {
			// LSP operation flags are execution-only. Keeping the operation name
			// selects the existing focused static help page.
			canonical = canonical[:2]
		} else {
			return canonical, opts, invocationError("lsp_help_usage", "unrecognized LSP help form", "use wirecmd --help lsp [definition|declaration|type-definition|implementation|references|hover|document-symbols|workspace-symbols|status]"), true
		}
	case "daemon":
		// Leave command arity and misplaced-flag diagnostics to the existing
		// administrative help parser. It has contextual errors for forms such
		// as `daemon run --config PATH --help`.
	case "config":
		// Existing administrative help owns optional PATH slots and validates
		// the command shape after this normalization.
	case "auth":
		// Existing administrative help owns the server argument and command
		// validation after this normalization.
	}

	updated := opts
	updated.help = true
	return canonical, updated, nil, true
}
