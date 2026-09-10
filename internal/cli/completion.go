package cli

import (
	"io"
	"os"
	"strings"
	"unicode"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/discovery"
)

// runServerCompletion prints safe configured server names for the shell
// completion script. It deliberately does not contact the daemon, resolve
// secrets, or initialize any state. Completion failure is intentionally quiet:
// an empty candidate list is safer and more useful than an error in a shell UI.
func runServerCompletion(opts options, positionals []string, parseErr error, out io.Writer) int {
	if parseErr != nil || len(positionals) != 0 || opts.direct || opts.jsonSet || opts.stdin || opts.help || opts.version || opts.formatSet || opts.colorSet || opts.legacyHelpSeparator {
		return exitOK
	}

	cwd, err := os.Getwd()
	if err != nil {
		return exitOK
	}

	paths := opts.configs
	discovered := len(paths) == 0
	if discovered {
		paths, err = discovery.Paths(cwd)
	} else {
		paths, err = absoluteConfigPaths(cwd, paths)
	}
	if err != nil {
		return exitOK
	}
	// An explicit config can name a FIFO or device for ordinary execution.
	// Completion must not wait for such a stream just because Tab was pressed.
	for _, path := range paths {
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			return exitOK
		}
	}

	var cfg *config.Config
	if discovered {
		cfg, err = config.LoadEffectiveDiscovered(paths)
	} else {
		cfg, err = config.LoadEffective(paths)
	}
	if err != nil {
		return exitOK
	}

	for _, server := range cfg.Servers {
		if completionNameSafe(server.Name) {
			_, _ = io.WriteString(out, server.Name+"\n")
		}
	}
	return exitOK
}

func completionNameSafe(name string) bool {
	if name == "" {
		return false
	}
	return !strings.ContainsFunc(name, func(character rune) bool {
		return unicode.IsControl(character) || isBidiControl(character)
	})
}
