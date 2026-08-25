package cli

import (
	"fmt"
	"os"

	"github.com/Kaylebor/wirecmd/internal/discovery"
)

type configAdmin struct {
	command string
	path    string
	err     *appError
}

// parseConfigAdmin reserves only the documented bare administrative forms.
// JSON input remains the escape hatch for a server named config.
func parseConfigAdmin(positionals []string, opts options) (configAdmin, bool) {
	if len(positionals) < 2 || positionals[0] != "config" || (opts.jsonSet || opts.stdin) {
		return configAdmin{}, false
	}
	admin := configAdmin{}
	switch positionals[1] {
	case "trust":
		admin.command = "trust"
		switch len(positionals) {
		case 2:
		case 3:
			if positionals[2] == "list" || positionals[2] == "status" {
				admin.command = positionals[2]
			} else {
				admin.path = positionals[2]
			}
		case 4:
			if positionals[2] != "status" {
				return configAdmin{}, false
			}
			admin.command, admin.path = "status", positionals[3]
		default:
			return configAdmin{}, false
		}
	case "untrust":
		admin.command = "untrust"
		if len(positionals) == 3 {
			admin.path = positionals[2]
		} else if len(positionals) != 2 {
			return configAdmin{}, false
		}
	default:
		return configAdmin{}, false
	}
	if opts.direct || len(opts.configs) != 0 || opts.help {
		admin.err = invocationError("config_admin_flags", "configuration administration does not accept --direct, --config, or --help", "run the configuration administration command without client flags")
	}
	return admin, true
}

func runConfigAdmin(admin configAdmin) (any, *appError) {
	if admin.err != nil {
		return nil, admin.err
	}
	path := admin.path
	if path == "" && admin.command != "list" {
		var err error
		path, err = os.Getwd()
		if err != nil {
			return nil, transportError("caller_cwd_unavailable", err.Error(), "run Wirecmd from an accessible working directory")
		}
	}
	switch admin.command {
	case "trust":
		workspace, err := discovery.Trust(path)
		if err != nil {
			return nil, configurationError("trust_update_failed", err.Error(), "check the workspace path and private Wirecmd state directory")
		}
		return map[string]any{"ok": true, "trust": map[string]any{"workspace": workspace, "status": "trusted"}}, nil
	case "untrust":
		workspace, err := discovery.Untrust(path)
		if err != nil {
			return nil, configurationError("trust_update_failed", err.Error(), "check the workspace path and private Wirecmd state directory")
		}
		return map[string]any{"ok": true, "trust": map[string]any{"workspace": workspace, "status": "untrusted"}}, nil
	case "status":
		workspace, trusted, root, err := discovery.Status(path)
		if err != nil {
			return nil, configurationError("trust_status_failed", err.Error(), "check the workspace path and private Wirecmd state directory")
		}
		return map[string]any{"ok": true, "trust": map[string]any{"workspace": workspace, "trusted": trusted, "root": root}}, nil
	case "list":
		workspaces, err := discovery.List()
		if err != nil {
			return nil, configurationError("trust_status_failed", err.Error(), "check the private Wirecmd state directory")
		}
		return map[string]any{"ok": true, "trust": map[string]any{"workspaces": workspaces}}, nil
	default:
		return nil, &appError{category: "internal", code: "invalid_config_admin", message: fmt.Sprintf("unknown config command %q", admin.command), action: "report this Wirecmd error", exitCode: exitInternal}
	}
}
