package cli

import "strings"

// Administrative help is resolved without loading configuration or executing
// administration. A prefix -- leaves these names available as server names.
func administrativeHelp(args []string, opts options) (helpText, *appError, bool) {
	if opts.helpServer || len(args) == 0 {
		return "", nil, false
	}
	group := args[0]
	if group != "daemon" && group != "config" && group != "auth" {
		return "", nil, false
	}
	if opts.jsonSet || opts.stdin {
		return "", invocationError("input_with_help", "--json and --stdin cannot be used with --help", "request help without a tool input mode"), true
	}
	if group != "auth" && (opts.direct || len(opts.configs) != 0) {
		return "", invocationError("admin_help_flags", group+" help does not accept --direct or --config", "run wirecmd --help "+group+"; use --help -- for a configured server"), true
	}
	valid := len(args) == 1
	if len(args) > 1 {
		switch group {
		case "daemon":
			valid = len(args) == 2 && (args[1] == "run" || args[1] == "status" || args[1] == "reload")
		case "auth":
			valid = (len(args) == 2 || len(args) == 3 && args[2] != "") && (args[1] == "login" || args[1] == "status" || args[1] == "logout")
		case "config":
			// Reuse the existing path/subcommand grammar, clearing only help.
			plain := opts
			plain.help = false
			admin, recognized := parseConfigAdmin(args, plain)
			valid = recognized && admin.err == nil
		}
	}
	if !valid {
		return "", invocationError("admin_help_usage", "unrecognized administrative help form", "run wirecmd --help "+group+"; use --help -- SERVER [TOOL] for server help"), true
	}
	text := adminHelpText(group)
	if len(args) > 1 {
		text = "Help for wirecmd " + strings.Join(args[:2], " ") + "\n\n" + text
	}
	return helpText(text), nil, true
}

func adminHelpText(group string) string {
	switch group {
	case "daemon":
		return `Daemon administration:
  wirecmd daemon run       run in the foreground; keep this terminal open
  wirecmd daemon status    report retained sessions and cached contexts
  wirecmd daemon reload    retire cached configuration and sessions

Start the daemon in a separate terminal before ordinary calls. Supply --config
to those calls, not daemon run. Reload lets active calls finish; subsequent
calls use fresh sessions and configuration. Ctrl-C stops the foreground daemon.
Linux requires a private, same-user absolute XDG_RUNTIME_DIR. On macOS, an
unset XDG_RUNTIME_DIR uses the validated per-user temporary directory.
Only presentation flags are accepted by these administrative commands.
`
	case "config":
		return `Configuration trust:
  wirecmd config trust [PATH]         trust a directory recursively
  wirecmd config untrust [PATH]       remove that directory's trust entry
  wirecmd config trust status [PATH]  inspect effective directory trust
  wirecmd config trust list           list trusted directories

PATH defaults to the current directory. Trust permits future configuration
changes below that root. Untrust does not remove trust granted by an ancestor.
Automatic discovery composes global configuration and trusted workspace files.
Repeated --config PATH on ordinary calls replaces discovery and bypasses trust
checks; later files override earlier ones. Trust commands do not accept --config
or --direct. Only presentation flags are accepted.
`
	default:
		return `OAuth credentials:
  wirecmd [--config PATH] [--direct] auth login SERVER
  wirecmd [--config PATH] [--direct] auth status SERVER
  wirecmd [--config PATH] [--direct] auth logout SERVER

login starts fresh browser authorization, reusing a valid client registration.
status inspects stored credentials without contacting the provider.
logout deletes local credentials and retires matching daemon sessions; it does
not revoke provider-side tokens. These commands normally require the daemon.
--config is repeatable; --direct selects deliberate daemonless operation.
Login requires interactive stdin and stderr. Ordinary protected calls may also
open a browser; WIRECMD_NONINTERACTIVE=1 disables automatic authorization.
Persistence requires an available native keyring (Secret Service on Linux,
Keychain on macOS). Browser handoffs go to stderr. Static Authorization headers
disable OAuth. --json and --stdin are tool inputs, not auth command options.
`
	}
}
