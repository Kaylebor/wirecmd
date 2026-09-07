package cli

// normalizeTrailingHelp recognizes the deliberately small trailing-help
// grammar. It returns canonical positionals and options for the existing
// offline/help parsers. The caller can invoke it before ordinary request
// parsing; no configuration, daemon, or upstream operation is touched here.
//
// A trailing help token is only special when it is the final, exact token.
// Tool-side suffixes remain owned by the tool, and explicit prefix help or
// the -- separator keeps its existing ownership.
func normalizeTrailingHelp(positionals []string, opts options) ([]string, options, *appError, bool) {
	if len(positionals) == 0 || opts.help || opts.helpServer || opts.jsonSet || opts.stdin {
		return positionals, opts, nil, false
	}
	last := positionals[len(positionals)-1]
	if last != "--help" && last != "-h" {
		return positionals, opts, nil, false
	}

	// A bare server followed by --help is the documented server-help form.
	// This is intentionally the only ordinary tool form that consumes a
	// trailing help token; SERVER TOOL --help belongs to the tool's projected
	// argument space.
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
		} else if isLSPNavigation(canonical[1]) || canonical[1] == lspStatus {
			// LSP operation flags are execution-only. Keeping the operation name
			// selects the existing focused static help page.
			canonical = canonical[:2]
		} else {
			return canonical, opts, invocationError("lsp_help_usage", "unrecognized LSP help form", "use wirecmd --help lsp [definition|declaration|type-definition|implementation|references|status]"), true
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
