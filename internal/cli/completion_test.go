package cli

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/Kaylebor/wirecmd/internal/discovery"
)

func TestServerCompletionUsesEffectiveConfigOrderAndDoesNotResolveSecrets(t *testing.T) {
	base := writeConfig(t, `wirecmd {
server "first" { scope "workspace"; stdio "not-started" }
server "unsafe\tname" { scope "workspace"; stdio "not-started" }
server "unsafe\nname" { scope "workspace"; stdio "not-started" }
server "needs-secret" { scope "workspace"; stdio "not-started" { env SECRET=(secret)"env://WIRECMD_COMPLETION_ABSENT_SECRET" } }
}`)
	stronger := writeConfig(t, `wirecmd {
server "first" { scope "workspace"; stdio "still-not-started" }
server "last" { scope "workspace"; stdio "not-started" }
}`)
	secretName := "WIRECMD_COMPLETION_ABSENT_SECRET"
	previous, existed := os.LookupEnv(secretName)
	if err := os.Unsetenv(secretName); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if existed {
			_ = os.Setenv(secretName, previous)
		} else {
			_ = os.Unsetenv(secretName)
		}
	})

	code, stdout, stderr := invoke(t, []string{"--completion-servers", "--config=" + base, "--config", stronger})
	if code != exitOK || stderr != "" {
		t.Fatalf("completion: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if want := "first\nneeds-secret\nlast\n"; stdout != want {
		t.Fatalf("completion output = %q, want %q", stdout, want)
	}
}

func TestServerCompletionIsQuietForRejectedOrUnavailableInput(t *testing.T) {
	config := writeConfig(t, `wirecmd { server "available" { scope "workspace"; stdio "not-started" } }`)
	for _, args := range [][]string{
		{"--completion-servers", "--direct", "--config", config},
		{"--completion-servers", "--help", "--config", config},
		{"--completion-servers", "--config", config, "available"},
		{"--completion-servers", "--config"},
		{"--completion-servers", "--unknown"},
	} {
		code, stdout, stderr := invoke(t, args)
		if code != exitOK || stdout != "" || stderr != "" {
			t.Fatalf("completion %v: code=%d stdout=%q stderr=%q", args, code, stdout, stderr)
		}
	}
}

func TestServerCompletionDiscoveryIsQuietAndDoesNotCreateState(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "wirecmd.kdl"), []byte(`wirecmd { server "untrusted" { scope "workspace"; stdio "not-started" } }`), 0o600); err != nil {
		t.Fatal(err)
	}
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "config"))

	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workspace); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })

	code, stdout, stderr := invoke(t, []string{"--completion-servers"})
	if code != exitOK || stdout != "" || stderr != "" {
		t.Fatalf("untrusted completion: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(stateHome); !os.IsNotExist(err) {
		t.Fatalf("completion created state home: stat error=%v", err)
	}
}

func TestServerCompletionDiscoversGlobalConfigWithoutStateWrites(t *testing.T) {
	configHome := t.TempDir()
	configPath := filepath.Join(configHome, "wirecmd", "config.kdl")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`wirecmd { server "global" { scope "workspace"; stdio "not-started" } }`), 0o600); err != nil {
		t.Fatal(err)
	}
	stateHome := filepath.Join(t.TempDir(), "state")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_STATE_HOME", stateHome)

	code, stdout, stderr := invoke(t, []string{"--completion-servers"})
	if code != exitOK || stdout != "global\n" || stderr != "" {
		t.Fatalf("global completion: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if _, err := os.Stat(stateHome); !os.IsNotExist(err) {
		t.Fatalf("completion created state home: stat error=%v", err)
	}
}

func TestServerCompletionIsQuietForMalformedConfiguration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wirecmd.kdl")
	if err := os.WriteFile(path, []byte("wirecmd { server"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(t, []string{"--completion-servers", "--config", path})
	if code != exitOK || stdout != "" || stderr != "" {
		t.Fatalf("malformed completion: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestCompletionNameSafe(t *testing.T) {
	for _, name := range []string{"server", "space server", "emoji-😀"} {
		if !completionNameSafe(name) {
			t.Errorf("completionNameSafe(%q) = false, want true", name)
		}
	}
	for _, name := range []string{"", "tab\tname", "line\nname", "escape\x1bname", "bidi\u202ename"} {
		if completionNameSafe(name) {
			t.Errorf("completionNameSafe(%q) = true, want false", name)
		}
	}
}

func TestServerCompletionOnlyAcceptsConfigFlags(t *testing.T) {
	config := writeConfig(t, `wirecmd { server "available" { scope "workspace"; stdio "not-started" } }`)
	code, stdout, stderr := invoke(t, []string{"--completion-servers", "--config", config})
	if code != exitOK || stdout != "available\n" || stderr != "" {
		t.Fatalf("accepted config: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	code, stdout, stderr = invoke(t, []string{"--completion-servers", "--config", config, "--", "available"})
	if code != exitOK || stdout != "" || stderr != "" {
		t.Fatalf("separator completion: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	if strings.Contains(stdout, "available") {
		t.Fatalf("rejected completion leaked a server name: %q", stdout)
	}
}

func TestServerCompletionTrustedWorkspace(t *testing.T) {
	workspace := t.TempDir()
	t.Setenv("XDG_STATE_HOME", filepath.Join(t.TempDir(), "state"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(t.TempDir(), "global"))
	if err := os.WriteFile(filepath.Join(workspace, "wirecmd.kdl"), []byte(`wirecmd { server "trusted" { scope "workspace"; stdio "never-started" } }`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := discovery.Trust(workspace); err != nil {
		t.Fatal(err)
	}
	t.Chdir(workspace)
	code, stdout, stderr := invoke(t, []string{"--completion-servers"})
	if code != exitOK || stdout != "trusted\n" || stderr != "" {
		t.Fatalf("trusted discovery: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}

func TestServerCompletionSkipsFIFO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.fifo")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := invoke(t, []string{"--completion-servers", "--config", path})
	if code != exitOK || stdout != "" || stderr != "" {
		t.Fatalf("FIFO completion: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
}
