package cli

import "testing"

func TestNormalizeTrailingHelp(t *testing.T) {
	tests := []struct {
		name        string
		positionals []string
		opts        options
		want        []string
		wantHelp    bool
		handled     bool
		code        string
	}{
		{name: "server", positionals: []string{"memory", "--help"}, want: []string{"memory"}, wantHelp: true, handled: true},
		{name: "short server", positionals: []string{"memory", "-h"}, want: []string{"memory"}, wantHelp: true, handled: true},
		{name: "daemon group", positionals: []string{"daemon", "--help"}, want: []string{"daemon"}, wantHelp: true, handled: true},
		{name: "daemon leaf", positionals: []string{"daemon", "status", "--help"}, want: []string{"daemon", "status"}, wantHelp: true, handled: true},
		{name: "config path", positionals: []string{"config", "trust", "/workspace", "--help"}, want: []string{"config", "trust", "/workspace"}, wantHelp: true, handled: true},
		{name: "config leaf", positionals: []string{"config", "trust", "status", "--help"}, want: []string{"config", "trust", "status"}, wantHelp: true, handled: true},
		{name: "auth server", positionals: []string{"auth", "login", "figma", "--help"}, want: []string{"auth", "login", "figma"}, wantHelp: true, handled: true},
		{name: "lsp flags removed", positionals: []string{"lsp", "definition", "--file", "main.go", "--line", "1", "--column", "2", "--help"}, want: []string{"lsp", "definition"}, wantHelp: true, handled: true},
		{name: "lsp status file removed", positionals: []string{"lsp", "status", "--file", "main.go", "--help"}, want: []string{"lsp", "status"}, wantHelp: true, handled: true},
		{name: "tool suffix preserved", positionals: []string{"memory", "search", "--help"}, want: []string{"memory", "search", "--help"}, handled: false},
		{name: "non-final help preserved", positionals: []string{"memory", "--help", "search"}, want: []string{"memory", "--help", "search"}, handled: false},
		{name: "exact flag only", positionals: []string{"memory", "--help=now"}, want: []string{"memory", "--help=now"}, handled: false},
		{name: "prefix help preserved", positionals: []string{"memory", "--help"}, opts: options{help: true}, want: []string{"memory", "--help"}, wantHelp: true, handled: false},
		{name: "separator escape preserved", positionals: []string{"daemon", "status", "--help"}, opts: options{helpServer: true}, want: []string{"daemon", "status", "--help"}, handled: false},
		{name: "json escape preserved", positionals: []string{"memory", "--help"}, opts: options{jsonSet: true}, want: []string{"memory", "--help"}, handled: false},
		{name: "stdin escape preserved", positionals: []string{"memory", "--help"}, opts: options{stdin: true}, want: []string{"memory", "--help"}, handled: false},
		{name: "invalid daemon leaf", positionals: []string{"daemon", "wat", "--help"}, want: []string{"daemon", "wat"}, wantHelp: true, handled: true},
		{name: "invalid lsp leaf", positionals: []string{"lsp", "wat", "--help"}, want: []string{"lsp", "wat"}, handled: true, code: "lsp_help_usage"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, gotOpts, appErr, handled := normalizeTrailingHelp(test.positionals, test.opts)
			if !equalStrings(got, test.want) || gotOpts.help != test.wantHelp || handled != test.handled {
				t.Fatalf("normalizeTrailingHelp() = %v, help=%v, err=%v, handled=%v; want %v, help=%v, err=%q, handled=%v", got, gotOpts.help, appErr, handled, test.want, test.wantHelp, test.code, test.handled)
			}
			if test.code == "" && appErr != nil {
				t.Fatalf("unexpected error: %#v", appErr)
			}
			if test.code != "" && (appErr == nil || appErr.code != test.code) {
				t.Fatalf("error = %#v; want code %q", appErr, test.code)
			}
		})
	}
}

func TestNormalizedTrailingAdministrativeHelpUsesExistingValidation(t *testing.T) {
	for _, test := range []struct {
		name string
		args []string
		code string
	}{
		{name: "invalid daemon", args: []string{"daemon", "wat", "--help"}, code: "admin_help_usage"},
		{name: "invalid config", args: []string{"config", "wat", "--help"}, code: "admin_help_usage"},
		{name: "invalid auth", args: []string{"auth", "wat", "server", "--help"}, code: "admin_help_usage"},
		{name: "misplaced daemon flag", args: []string{"daemon", "run", "--config", "x", "--help"}, code: "admin_misplaced_flag"},
	} {
		t.Run(test.name, func(t *testing.T) {
			positionals, opts, appErr, handled := normalizeTrailingHelp(test.args, options{})
			if !handled || appErr != nil {
				t.Fatalf("normalize = %v, %#v, handled=%v", positionals, appErr, handled)
			}
			if _, helpErr, helpHandled := administrativeHelp(positionals, opts); !helpHandled || helpErr == nil || helpErr.code != test.code {
				t.Fatalf("administrativeHelp = handled:%v err:%#v; want %q", helpHandled, helpErr, test.code)
			}
		})
	}
}

func TestTrailingNativeHelpIsOfflineAndSideEffectFree(t *testing.T) {
	for _, args := range [][]string{
		{"daemon", "status", "--help"},
		{"config", "trust", "--help"},
		{"auth", "login", "figma", "--help"},
		{"lsp", "definition", "--file", "missing.go", "--line", "1", "--column", "1", "--help"},
	} {
		positionals, opts, appErr, handled := normalizeTrailingHelp(args, options{})
		if !handled || appErr != nil {
			t.Fatalf("%v: normalize = %v, %#v, handled=%v", args, positionals, appErr, handled)
		}
		var help helpText
		var helpErr *appError
		if positionals[0] == "lsp" {
			help, helpErr, handled = lspHelp(positionals, opts)
		} else {
			help, helpErr, handled = administrativeHelp(positionals, opts)
		}
		if !handled || helpErr != nil || help == "" {
			t.Fatalf("%v: help = %q, err=%#v, handled=%v", args, help, helpErr, handled)
		}
	}
}

func TestTrailingToolHelpRemainsLiveSchemaInput(t *testing.T) {
	positionals, opts, appErr, handled := normalizeTrailingHelp([]string{"memory", "search", "--help"}, options{})
	if handled || appErr != nil || opts.help {
		t.Fatalf("tool suffix normalized unexpectedly: %v, %#v, %#v, handled=%v", positionals, opts, appErr, handled)
	}
	request, requestErr := parseRequest(positionals, opts, nil)
	if requestErr != nil || request.operation != callTool || len(request.projected) != 1 || request.projected[0].Name != "help" {
		t.Fatalf("parseRequest = %#v, %#v; want live-schema help argument", request, requestErr)
	}
}

func TestShortTrailingToolHelpUsesHelpProjection(t *testing.T) {
	request, requestErr := parseRequest([]string{"memory", "search", "--query", "value", "-h"}, options{}, nil)
	if requestErr != nil || request.operation != callTool || len(request.projected) != 2 || request.projected[1].Name != "help" {
		t.Fatalf("parseRequest = %#v, %#v; want projected help argument", request, requestErr)
	}
}

func TestNormalizeTrailingHelpDoesNotMutateInput(t *testing.T) {
	input := []string{"lsp", "definition", "--file", "main.go", "--help"}
	want := append([]string(nil), input...)
	_, _, _, _ = normalizeTrailingHelp(input, options{})
	if !equalStrings(input, want) {
		t.Fatalf("input mutated: %#v", input)
	}
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
