package cli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Kaylebor/wirecmd/internal/config"
)

func writeFakeAge(t *testing.T, plaintext string) string {
	t.Helper()
	directory := t.TempDir()
	path := filepath.Join(directory, "age")
	script := `#!/bin/sh
if [ "$1" = "--version" ]; then
  printf '%s\n' 'v1.3.2'
  exit 0
fi
printf '%s\n' decrypt >> "$WIRECMD_FAKE_AGE_CALLS"
cat >/dev/null
printf '%s' "$WIRECMD_FAKE_AGE_PLAINTEXT"
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", directory+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WIRECMD_FAKE_AGE_PLAINTEXT", plaintext)
	return path
}

func ageHelperConfig(t *testing.T, directory, identity, extra string) string {
	t.Helper()
	configPath := filepath.Join(directory, ".wirecmd", "config.kdl")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	source := "wirecmd {\nsecrets { age { identity " + strconv.Quote(identity) + " } }\nmcp \"helper\" {\nstdio " + strconv.Quote(os.Args[0]) + " {\narg \"-test.run=TestHelperProcess\"\narg \"--\"\n" + extra + "\n}\n}\n}\n"
	if err := os.WriteFile(configPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(configPath), "secrets.json.age"), []byte("fixture ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	return configPath
}

func fakeAgeDecryptCount(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return len(strings.Fields(string(data)))
}

func childPIDs(t *testing.T, path string) []int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	fields := strings.Fields(string(data))
	result := make([]int, len(fields))
	for index, field := range fields {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatalf("child PID %q: %v", field, err)
		}
		result[index] = pid
	}
	return result
}

func waitForProcessExit(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if errors.Is(err, syscall.ESRCH) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("superseded child process %d remained alive", pid)
}

func TestDirectAgeSecretResolvesAndRedacts(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	secret := "direct-age-sentinel"
	calls := filepath.Join(t.TempDir(), "age.calls")
	t.Setenv("WIRECMD_FAKE_AGE_CALLS", calls)
	writeFakeAge(t, `{"TOKEN":"`+secret+`"}`)
	directory := t.TempDir()
	configPath := ageHelperConfig(t, directory, filepath.Join(directory, "identity.age"), `env SECRET=(secret)"age://TOKEN"`)

	code, output, stderr := invoke(t, []string{"--direct", "--config", configPath, "helper", "secret"})
	if code != exitOK || strings.Contains(output, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("direct age call: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	if !strings.Contains(output, "[REDACTED]") || fakeAgeDecryptCount(t, calls) != 1 {
		t.Fatalf("direct age result: stdout=%q decrypts=%d", output, fakeAgeDecryptCount(t, calls))
	}
	code, output, stderr = invoke(t, []string{"--direct", "--config", configPath, "helper", "secret"})
	if code != exitOK || strings.Contains(output, secret) || strings.Contains(stderr, secret) || fakeAgeDecryptCount(t, calls) != 2 {
		t.Fatalf("second direct age call: code=%d stdout=%q stderr=%q decrypts=%d", code, output, stderr, fakeAgeDecryptCount(t, calls))
	}
}

func TestStaticSurfacesDoNotResolveAgeSecrets(t *testing.T) {
	directory := t.TempDir()
	configPath := filepath.Join(directory, "config.kdl")
	identity := filepath.Join(directory, "identity.age")
	source := `wirecmd {
secrets { age { identity ` + strconv.Quote(identity) + ` } }
mcp "helper" { stdio ` + strconv.Quote(os.Args[0]) + ` { env SECRET=(secret)"age://TOKEN" } }
lsp "helper" {
selector language-id="go" pattern="**/*.go"
stdio ` + strconv.Quote(os.Args[0]) + ` { env SECRET=(secret)"age://TOKEN" }
}
}
`
	if err := os.WriteFile(configPath, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(directory, filepath.Join(directory, "secrets.json.age")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	for _, arguments := range [][]string{
		{"--help"},
		{"--completion-servers", "--config", configPath},
		{"--direct", "--config", configPath, "mcp"},
		{"--direct", "--config", configPath, "lsp", "status"},
	} {
		code, output, stderr := invoke(t, arguments)
		if code != exitOK {
			t.Fatalf("static command %v: code=%d stdout=%q stderr=%q", arguments, code, output, stderr)
		}
	}
}

func TestDaemonAgeSnapshotReuseAndSameValueRotation(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	secret := "daemon-age-sentinel"
	directory := t.TempDir()
	calls := filepath.Join(directory, "age.calls")
	children := filepath.Join(directory, "children")
	t.Setenv("WIRECMD_FAKE_AGE_CALLS", calls)
	writeFakeAge(t, `{"TOKEN":"`+secret+`"}`)
	configPath := ageHelperConfig(t, directory, filepath.Join(directory, "identity.age"), `env SECRET=(secret)"age://TOKEN"; env WIRECMD_CHILD_COUNT_FILE=`+strconv.Quote(children))
	startTestDaemon(t)

	for iteration := 0; iteration < 2; iteration++ {
		code, output, stderr := invoke(t, []string{"--config", configPath, "helper", "secret"})
		if code != exitOK || strings.Contains(output, secret) || strings.Contains(stderr, secret) {
			t.Fatalf("daemon age call %d: code=%d stdout=%q stderr=%q", iteration, code, output, stderr)
		}
	}
	if got := fakeAgeDecryptCount(t, calls); got != 1 {
		t.Fatalf("unchanged snapshot decrypts = %d, want 1", got)
	}
	store := filepath.Join(filepath.Dir(configPath), "secrets.json.age")
	if err := os.WriteFile(store, []byte("rotated fixture ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, output, stderr := invoke(t, []string{"--config", configPath, "helper", "secret"})
	if code != exitOK || strings.Contains(output, secret) || strings.Contains(stderr, secret) {
		t.Fatalf("rotated daemon age call: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	if got := fakeAgeDecryptCount(t, calls); got != 2 {
		t.Fatalf("rotated snapshot decrypts = %d, want 2", got)
	}
	if data, err := os.ReadFile(children); err != nil || len(strings.Fields(string(data))) != 1 {
		t.Fatalf("retained child count: data=%q err=%v", data, err)
	}
	firstPID := childPIDs(t, children)[0]
	rotatedSecret := "daemon-age-rotated-sentinel"
	t.Setenv("WIRECMD_FAKE_AGE_PLAINTEXT", `{"TOKEN":"`+rotatedSecret+`"}`)
	if err := os.WriteFile(store, []byte("second rotated fixture ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, output, stderr = invoke(t, []string{"--config", configPath, "helper", "secret"})
	if code != exitOK || strings.Contains(output, rotatedSecret) || strings.Contains(stderr, rotatedSecret) {
		t.Fatalf("value-rotated daemon call: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	if got := fakeAgeDecryptCount(t, calls); got != 3 {
		t.Fatalf("value-rotated snapshot decrypts = %d, want 3", got)
	}
	if data, err := os.ReadFile(children); err != nil || len(strings.Fields(string(data))) != 2 {
		t.Fatalf("replaced child count: data=%q err=%v", data, err)
	}
	waitForProcessExit(t, firstPID)
	code, output, stderr = invoke(t, []string{"daemon", "reload"})
	if code != exitOK || stderr != "" {
		t.Fatalf("daemon reload: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	code, output, stderr = invoke(t, []string{"--config", configPath, "helper", "secret"})
	if code != exitOK || strings.Contains(output, rotatedSecret) || strings.Contains(stderr, rotatedSecret) {
		t.Fatalf("post-reload daemon call: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	if got := fakeAgeDecryptCount(t, calls); got != 4 {
		t.Fatalf("post-reload decrypts = %d, want 4", got)
	}
	if data, err := os.ReadFile(children); err != nil || len(strings.Fields(string(data))) != 3 {
		t.Fatalf("post-reload child count: data=%q err=%v", data, err)
	}
}

func TestAgeReferenceRequiresExplicitIdentity(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	calls := filepath.Join(t.TempDir(), "age.calls")
	t.Setenv("WIRECMD_FAKE_AGE_CALLS", calls)
	writeFakeAge(t, `{"TOKEN":"unused"}`)
	directory := t.TempDir()
	configPath := helperConfigAt(t, filepath.Join(directory, ".wirecmd", "config.kdl"), "", `env SECRET=(secret)"age://TOKEN"`)
	if err := os.WriteFile(filepath.Join(filepath.Dir(configPath), "secrets.json.age"), []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, output, _ := invoke(t, []string{"--direct", "--config", configPath, "helper", "secret"})
	if code != exitConfiguration || decodeOutput(t, output)["error"].(map[string]any)["code"] != "secret_provider_invalid" || fakeAgeDecryptCount(t, calls) != 0 {
		t.Fatalf("missing identity: code=%d output=%s decrypts=%d", code, output, fakeAgeDecryptCount(t, calls))
	}
}

func TestAgeBatchFeedsHTTPAndOAuthDestinations(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "age.calls")
	t.Setenv("WIRECMD_FAKE_AGE_CALLS", calls)
	writeFakeAge(t, `{"QUERY":"query-value","HEADER":"header-value","OAUTH":"oauth-value"}`)
	directory := t.TempDir()
	store := filepath.Join(directory, "secrets.json.age")
	if err := os.WriteFile(store, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretValue := func(name string) config.Value {
		return config.Value{Kind: config.ValueSecretReference, Text: "age://" + name}
	}
	transport := config.HTTP{
		Endpoint: "https://example.test/mcp",
		Query:    []config.HTTPField{{Name: "token", Value: secretValue("QUERY")}},
		Headers:  []config.HTTPField{{Name: "X-API-Key", Value: secretValue("HEADER")}},
		OAuth:    &config.OAuth{ClientID: "client", ClientSecret: func() *config.Value { value := secretValue("OAUTH"); return &value }(), RedirectURI: "http://127.0.0.1:8765/callback"},
	}
	server := config.Server{Name: "remote", HTTP: &transport}
	configured := config.Secrets{Age: &config.AgeSecrets{Identities: []config.AgeIdentity{{Path: filepath.Join(directory, "identity.age")}}}}
	resolved, appErr := resolveSelectedSecrets(context.Background(), configured, []string{store}, false, nil, serverSecretValues(server), os.LookupEnv)
	if appErr != nil {
		t.Fatal(appErr)
	}
	target, protected, appErr := makeHTTPTarget(transport, resolved.lookup)
	if appErr != nil {
		t.Fatal(appErr)
	}
	clientSecret, oauthProtected, appErr := resolveOAuthClientSecret(transport, resolved.lookup)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if target.endpoint != "https://example.test/mcp?token=query-value" || target.httpClient.Transport.(*configuredHeaderTransport).headers.Get("X-API-Key") != "header-value" || clientSecret != "oauth-value" {
		t.Fatal("resolved age values did not reach every HTTP destination")
	}
	if len(protected) != 2 || len(oauthProtected) != 1 || fakeAgeDecryptCount(t, calls) != 1 {
		t.Fatalf("batch result: http=%d oauth=%d decrypts=%d", len(protected), len(oauthProtected), fakeAgeDecryptCount(t, calls))
	}
}

func TestLSPMatchesShareOneAgeBatch(t *testing.T) {
	calls := filepath.Join(t.TempDir(), "age.calls")
	t.Setenv("WIRECMD_FAKE_AGE_CALLS", calls)
	writeFakeAge(t, `{"FIRST":"one","SECOND":"two"}`)
	directory := t.TempDir()
	store := filepath.Join(directory, "secrets.json.age")
	if err := os.WriteFile(store, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	matches := []lspMatch{
		{Definition: config.LSP{Stdio: config.Stdio{Args: []config.Value{{Kind: config.ValueSecretReference, Text: "age://FIRST"}}}}},
		{Definition: config.LSP{Stdio: config.Stdio{Env: []config.Environment{{Name: "TOKEN", Value: config.Value{Kind: config.ValueSecretReference, Text: "age://SECOND"}}}}}},
	}
	configured := config.Secrets{Age: &config.AgeSecrets{Identities: []config.AgeIdentity{{Path: filepath.Join(directory, "identity.age")}}}}
	resolved, appErr := resolveSelectedSecrets(context.Background(), configured, []string{store}, false, nil, lspSecretValues(matches), os.LookupEnv)
	if appErr != nil {
		t.Fatal(appErr)
	}
	if first, _ := resolved.lookup("age://FIRST"); first != "one" {
		t.Fatalf("first LSP value = %q", first)
	}
	if second, _ := resolved.lookup("age://SECOND"); second != "two" {
		t.Fatalf("second LSP value = %q", second)
	}
	if got := fakeAgeDecryptCount(t, calls); got != 1 {
		t.Fatalf("LSP union decrypts = %d, want 1", got)
	}
}

func TestAgeIdentitiesAffectOnlySelectedAgeExecutionFingerprints(t *testing.T) {
	configured := config.Secrets{Age: &config.AgeSecrets{Identities: []config.AgeIdentity{{Path: "/identity/one"}}}}
	changed := config.Secrets{Age: &config.AgeSecrets{Identities: []config.AgeIdentity{{Path: "/identity/two"}}}}

	plainServer := config.Server{Name: "plain", Stdio: config.Stdio{Command: "server"}}
	if first, second := serverExecutionFingerprint(plainServer, nil, "/workspace", configured), serverExecutionFingerprint(plainServer, nil, "/workspace", changed); first != second {
		t.Fatal("unused age identities changed MCP execution fingerprint")
	}
	ageServer := plainServer
	ageServer.Stdio = config.Stdio{Command: "server", Args: []config.Value{{Kind: config.ValueSecretReference, Text: "age://TOKEN"}}}
	if first, second := serverExecutionFingerprint(ageServer, nil, "/workspace", configured), serverExecutionFingerprint(ageServer, nil, "/workspace", changed); first == second {
		t.Fatal("selected age identities did not change MCP execution fingerprint")
	}

	plainMatches := []lspMatch{{Definition: config.LSP{Name: "plain", Stdio: config.Stdio{Command: "lsp"}}}}
	if first, second := matchedLSPExecutionFingerprint(plainMatches, nil, "/workspace", configured), matchedLSPExecutionFingerprint(plainMatches, nil, "/workspace", changed); first != second {
		t.Fatal("unused age identities changed LSP execution fingerprint")
	}
	ageMatches := []lspMatch{{Definition: config.LSP{Name: "age", Stdio: config.Stdio{Command: "lsp", Env: []config.Environment{{Name: "TOKEN", Value: config.Value{Kind: config.ValueSecretReference, Text: "age://TOKEN"}}}}}}}
	if first, second := matchedLSPExecutionFingerprint(ageMatches, nil, "/workspace", configured), matchedLSPExecutionFingerprint(ageMatches, nil, "/workspace", changed); first == second {
		t.Fatal("selected age identities did not change LSP execution fingerprint")
	}
}
