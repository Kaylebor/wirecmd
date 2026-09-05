package cli

import "fmt"

type authAdmin struct {
	command string
	server  string
	err     *appError
}

// parseAuthAdmin reserves only the documented bare administration forms.
// Exact JSON input remains the escape hatch for an MCP server named "auth".
func parseAuthAdmin(positionals []string, opts options) (authAdmin, bool) {
	if len(positionals) == 0 || positionals[0] != "auth" {
		return authAdmin{}, false
	}
	if opts.jsonSet || opts.stdin || (len(positionals) == 2 && startsJSONObject(positionals[1])) {
		return authAdmin{}, false
	}
	if len(positionals) >= 2 && isAuthAdminCommand(positionals[1]) {
		if flag, command, found := misplacedAdministrativeFlag(positionals); found {
			return authAdmin{err: misplacedAdministrativeFlagError("auth", command, flag)}, true
		}
	}
	if len(positionals) != 3 || !isAuthAdminCommand(positionals[1]) {
		return authAdmin{err: invocationError("auth_admin_arity", "auth administration requires login, status, or logout and one server name", "use wirecmd auth login|status|logout <server>")}, true
	}
	if opts.help {
		return authAdmin{err: invocationError("auth_admin_flags", "auth administration does not accept --help", "use wirecmd --help for OAuth usage")}, true
	}
	if positionals[2] == "" {
		return authAdmin{err: invocationError("server_required", "auth administration requires a non-empty server name", "supply a configured HTTP server name")}, true
	}
	return authAdmin{command: positionals[1], server: positionals[2]}, true
}

func isAuthAdminCommand(command string) bool {
	return command == "login" || command == "status" || command == "logout"
}

type authEnvelope struct {
	OK   bool       `json:"ok"`
	Auth authStatus `json:"auth"`
}

type authStatus struct {
	Server       string `json:"server"`
	Status       string `json:"status"`
	Registration string `json:"registration,omitempty"`
	ExpiresAt    string `json:"expires_at,omitempty"`
}

func authAction(server string) string {
	return "run wirecmd auth login " + shellQuote(server)
}

func authenticationError(code, message, action string) *appError {
	return &appError{category: "authentication", code: code, message: message, action: action, exitCode: exitAuthentication}
}

func authServerError(server string) *appError {
	return configurationError("oauth_unsupported", fmt.Sprintf("configured server %q does not use Streamable HTTP OAuth", server), "choose an HTTP server without a static Authorization header")
}
