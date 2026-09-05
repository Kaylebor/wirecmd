package cli

import (
	"fmt"
	"strings"
)

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
	if flag, command, found := misplacedAdministrativeFlag(args); found {
		return "", misplacedAdministrativeFlagError(group, command, flag), true
	}
	topic, valid := administrativeHelpTopicFor(args)
	if !valid {
		return "", invocationError("admin_help_usage", "unrecognized administrative help form", "run wirecmd --help "+group+"; use --help -- SERVER [TOOL] for server help"), true
	}
	if topic.command != "" {
		return helpText(adminLeafHelpText(topic.group, topic.command)), nil, true
	}
	return helpText(adminHelpText(topic.group)), nil, true
}

type administrativeHelpTopic struct {
	group   string
	command string
}

func administrativeHelpTopicFor(args []string) (administrativeHelpTopic, bool) {
	if len(args) == 0 {
		return administrativeHelpTopic{}, false
	}
	topic := administrativeHelpTopic{group: args[0]}
	switch args[0] {
	case "daemon":
		if len(args) == 1 {
			return topic, true
		}
		if len(args) == 2 && isDaemonAdminCommand(args[1]) {
			topic.command = args[1]
			return topic, true
		}
	case "config":
		if len(args) == 1 {
			return topic, true
		}
		switch args[1] {
		case "trust":
			switch len(args) {
			case 2:
				topic.command = "trust"
			case 3:
				if args[2] == "list" {
					topic.command = "list"
				} else if args[2] == "status" {
					topic.command = "status"
				} else {
					topic.command = "trust"
				}
			case 4:
				if args[2] == "status" && args[3] != "" {
					topic.command = "status"
				}
			}
		case "untrust":
			if len(args) == 2 || len(args) == 3 && args[2] != "" {
				topic.command = "untrust"
			}
		}
		if topic.command != "" {
			return topic, true
		}
	case "auth":
		if len(args) == 1 {
			return topic, true
		}
		if (len(args) == 2 || len(args) == 3 && args[2] != "") && isAuthAdminCommand(args[1]) {
			topic.command = args[1]
			return topic, true
		}
	}
	return administrativeHelpTopic{}, false
}

// misplacedAdministrativeFlag recognizes client flags only in the small,
// already-reserved administrative grammar. It intentionally does not scan
// arbitrary suffixes, which remain available to normal tool calls.
func misplacedAdministrativeFlag(args []string) (flag, command string, found bool) {
	if len(args) < 2 {
		return "", "", false
	}
	switch args[0] {
	case "daemon":
		if isDaemonAdminCommand(args[1]) && len(args) > 2 {
			if flag, found := knownWirecmdFlag(args[2]); found {
				return flag, args[1], true
			}
		}
	case "config":
		return misplacedConfigHelpFlag(args)
	case "auth":
		if isAuthAdminCommand(args[1]) {
			if len(args) >= 3 {
				if flag, found := knownWirecmdFlag(args[2]); found {
					return flag, args[1], true
				}
			}
			if len(args) >= 4 {
				if flag, found := knownWirecmdFlag(args[3]); found {
					return flag, args[1], true
				}
			}
		}
	}
	return "", "", false
}

func misplacedConfigHelpFlag(positionals []string) (flag, command string, found bool) {
	if flag, command, found = misplacedConfigAdminFlag(positionals); found {
		return flag, command, true
	}
	if len(positionals) != 4 || positionals[0] != "config" || positionals[1] != "trust" {
		return "", "", false
	}
	if positionals[2] != "list" && positionals[2] != "status" {
		return "", "", false
	}
	if flag, found = knownWirecmdFlag(positionals[3]); found {
		return flag, positionals[2], true
	}
	return "", "", false
}

func misplacedAdministrativeFlagError(group, command, flag string) *appError {
	adminCommand := command
	if group == "config" && (command == "status" || command == "list") {
		adminCommand = "trust " + command
	}
	admin := "wirecmd " + group + " " + adminCommand
	message := fmt.Sprintf("%s is a Wirecmd client flag, but it appears after %s", flag, admin)
	switch group {
	case "daemon":
		if flag == "--config" {
			message = fmt.Sprintf("%s selects an ordinary server call; %s does not load configuration", flag, admin)
		}
		return invocationError("admin_misplaced_flag", message, "move presentation flags before "+admin+"; use ordinary calls for --config, --direct, --json, or --stdin")
	case "config":
		return invocationError("admin_misplaced_flag", message, "move presentation flags before "+admin+"; configuration administration does not accept --config, --direct, --json, or --stdin")
	default:
		return invocationError("admin_misplaced_flag", message, "place allowed client flags before "+admin+" SERVER; --json and --stdin are tool-call escapes")
	}
}

func knownWirecmdFlag(value string) (string, bool) {
	prefix := "--"
	nameValue, found := strings.CutPrefix(value, prefix)
	if !found {
		prefix = "-"
		nameValue, found = strings.CutPrefix(value, prefix)
	}
	if !found || nameValue == "" {
		return "", false
	}
	name, _, _ := strings.Cut(nameValue, "=")
	switch name {
	case "completion-servers", "config", "direct", "json", "stdin", "help", "h", "version", "format", "color", "colour":
		return prefix + name, true
	default:
		return "", false
	}
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

func adminLeafHelpText(group, command string) string {
	switch group {
	case "daemon":
		switch command {
		case "run":
			return `Run the foreground daemon.

Usage:
  wirecmd [--format auto|json|pretty] [--color auto|always|never] daemon run

The daemon binds Wirecmd's private same-user socket, prints one readiness
envelope, and serves until Ctrl-C or SIGTERM. It does not load configuration:
each ordinary client call supplies its own selected configuration. Start it in a
separate terminal before ordinary daemon-backed calls.
`
		case "status":
			return `Inspect the running daemon.

Usage:
  wirecmd [--format auto|json|pretty] [--color auto|always|never] daemon status

Reports the daemon protocol and PID plus cached contexts and retained-instance
counts. It does not start a daemon. When no compatible daemon is listening,
Wirecmd returns daemon_unavailable (exit 7).
`
		case "reload":
			return `Reload daemon-managed configuration and sessions.

Usage:
  wirecmd [--format auto|json|pretty] [--color auto|always|never] daemon reload

Invalidates cached configurations and retires retained MCP sessions globally.
Active requests finish; later requests create fresh sessions using configuration
loaded after the reload. It does not watch files or reload only one workspace.
`
		}
	case "config":
		switch command {
		case "trust":
			return `Trust a workspace for automatic configuration discovery.

Usage:
  wirecmd [--format auto|json|pretty] [--color auto|always|never] config trust [PATH]

PATH defaults to the current directory. Trust is recursive, so it permits
future wirecmd.kdl changes below that root. This records local Wirecmd security
state; it neither loads project configuration nor starts the daemon.
`
		case "untrust":
			return `Remove one workspace trust entry.

Usage:
  wirecmd [--format auto|json|pretty] [--color auto|always|never] config untrust [PATH]

PATH defaults to the current directory. Removing an entry does not remove trust
inherited from a trusted ancestor. This changes only Wirecmd's local trust
registry; it does not modify workspace configuration.
`
		case "status":
			return `Inspect effective workspace trust.

Usage:
  wirecmd [--format auto|json|pretty] [--color auto|always|never] config trust status [PATH]

PATH defaults to the current directory. Reports the resolved workspace, whether
it is trusted, and the nearest trust root when one applies. It does not load
wirecmd.kdl or contact the daemon.
`
		case "list":
			return `List trusted workspace roots.

Usage:
  wirecmd [--format auto|json|pretty] [--color auto|always|never] config trust list

Lists only the private local trust registry. It does not search for project
configuration, contact the daemon, or modify trust state.
`
		}
	case "auth":
		switch command {
		case "login":
			return `Authenticate one configured HTTP server.

Usage:
  wirecmd [--config PATH] [--direct] [--format auto|json|pretty] auth login SERVER

Starts a fresh browser authorization flow and replaces the stored token while
reusing a valid dynamic registration. Login requires interactive stdin and
stderr. Without --direct it uses the running daemon; --direct is deliberate
one-shot operation. Authorization URLs and browser diagnostics go to stderr.
`
		case "status":
			return `Inspect local credentials for one configured HTTP server.

Usage:
  wirecmd [--config PATH] [--direct] [--format auto|json|pretty] auth status SERVER

Reports locally stored credential state, registration method, and known expiry
without contacting the provider. It never prints tokens, client secrets, or
sensitive authorization URLs.
`
		case "logout":
			return `Delete local credentials for one configured HTTP server.

Usage:
  wirecmd [--config PATH] [--direct] [--format auto|json|pretty] auth logout SERVER

Deletes Wirecmd's local token and registration record and retires matching
daemon sessions. It does not revoke provider-side tokens or otherwise contact
the authorization provider.
`
		}
	}
	return ""
}
