package secrets

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRegistryParsesCanonicalReferences(t *testing.T) {
	env, err := NewEnvProvider(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	age, err := NewAgeProvider(AgeProviderOptions{})
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(env, age)
	if err != nil {
		t.Fatal(err)
	}

	reference, err := registry.Parse("AgE://API.token-1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (Reference{Scheme: "age", Locator: "API.token-1"}); reference != want {
		t.Fatalf("reference = %#v, want %#v", reference, want)
	}
	for _, raw := range []string{"TOKEN", "://TOKEN", "env://", "unknown://TOKEN", "age://bad/key"} {
		if _, err := registry.Parse(raw); err == nil {
			t.Fatalf("Parse(%q) succeeded", raw)
		}
	}
}

func TestRegistryRejectsDuplicateProviders(t *testing.T) {
	first := &recordingProvider{scheme: "test"}
	second := &recordingProvider{scheme: "TEST"}
	_, err := NewRegistry(first, second)
	if got := errorCode(t, err); got != CodeDuplicateProvider {
		t.Fatalf("code = %q, want %q", got, CodeDuplicateProvider)
	}
}

func TestRegistryBatchesDeterministically(t *testing.T) {
	alpha := &recordingProvider{scheme: "alpha", values: map[string]string{"one": "first", "two": "second"}}
	beta := &recordingProvider{scheme: "beta", values: map[string]string{"three": "third"}}
	registry, err := NewRegistry(beta, alpha)
	if err != nil {
		t.Fatal(err)
	}
	references := []Reference{
		{Scheme: "beta", Locator: "three"},
		{Scheme: "alpha", Locator: "two"},
		{Scheme: "alpha", Locator: "one"},
		{Scheme: "alpha", Locator: "one"},
	}
	resolved, err := registry.Resolve(context.Background(), Scope("workspace"), references)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := alpha.calls, [][]string{{"one", "two"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("alpha calls = %#v, want %#v", got, want)
	}
	if got, want := beta.calls, [][]string{{"three"}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("beta calls = %#v, want %#v", got, want)
	}
	if alpha.scopes[0] != Scope("workspace") || beta.scopes[0] != Scope("workspace") {
		t.Fatalf("scopes = %#v / %#v", alpha.scopes, beta.scopes)
	}
	if len(resolved) != 3 || resolved[Reference{Scheme: "alpha", Locator: "one"}] != "first" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestRegistryAggregatesMissingReferences(t *testing.T) {
	env, err := NewEnvProvider(func(name string) (string, bool) { return "", name == "PRESENT" })
	if err != nil {
		t.Fatal(err)
	}
	registry, err := NewRegistry(env)
	if err != nil {
		t.Fatal(err)
	}
	_, err = registry.Resolve(context.Background(), Scope("workspace"), []Reference{{Scheme: "env", Locator: "MISSING"}, {Scheme: "env", Locator: "PRESENT"}})
	typed := errorValue(t, err)
	if typed.Code != CodeSecretUnavailable || !reflect.DeepEqual(typed.Missing, []Reference{{Scheme: "env", Locator: "MISSING"}}) {
		t.Fatalf("error = %#v", typed)
	}
}

func TestEnvProviderPreservesEmptyValue(t *testing.T) {
	env, err := NewEnvProvider(func(name string) (string, bool) {
		return "", name == "EMPTY"
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := env.Resolve(context.Background(), Scope("workspace"), []string{"EMPTY", "MISSING"})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := result.Values["EMPTY"]; !ok || value != "" {
		t.Fatalf("empty environment value = %q, %t", value, ok)
	}
	if !reflect.DeepEqual(result.Missing, []string{"MISSING"}) {
		t.Fatalf("missing = %#v", result.Missing)
	}
}

func TestAgeProviderUsesFixedCommandAndStorePrecedence(t *testing.T) {
	fake := writeFakeAge(t)
	t.Setenv("AGE_TEST_VERSION", "1.3.2")
	arguments := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("AGE_TEST_ARGUMENTS", arguments)
	identity := filepath.Join(t.TempDir(), "identity")
	strongest := writeStore(t, `{"FIRST":"strong","SECOND":"two"}`)
	weakest := writeStore(t, `{"FIRST":"weak","THIRD":"three"}`)
	provider, err := NewAgeProvider(AgeProviderOptions{
		Command:    fake,
		Identities: []string{identity},
		Stores:     []Store{{Path: strongest}, {Path: weakest}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := provider.Resolve(context.Background(), Scope("workspace"), []string{"FIRST", "SECOND", "THIRD"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := result.Values, map[string]string{"FIRST": "strong", "SECOND": "two", "THIRD": "three"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("values = %#v, want %#v", got, want)
	}
	data, err := os.ReadFile(arguments)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(string(data)); !reflect.DeepEqual(got, []string{"--decrypt", "-i", identity}) {
		t.Fatalf("age arguments = %#v", got)
	}
}

func TestAgeProviderRejectsUnsafeAndInvalidStores(t *testing.T) {
	fake := writeFakeAge(t)
	t.Setenv("AGE_TEST_VERSION", "1.3.2")

	tests := []struct {
		name string
		data string
	}{
		{name: "nested value", data: `{"TOKEN":{"nested":"value"}}`},
		{name: "duplicate key", data: `{"TOKEN":"one","TOKEN":"two"}`},
		{name: "trailing JSON", data: `{"TOKEN":"value"} {}`},
		{name: "invalid key", data: `{"bad/key":"value"}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			provider, err := NewAgeProvider(AgeProviderOptions{Command: fake, Identities: []string{testIdentity(t)}, Stores: []Store{{Path: writeStore(t, test.data)}}})
			if err != nil {
				t.Fatal(err)
			}
			_, err = provider.Resolve(context.Background(), Scope("workspace"), []string{"TOKEN"})
			if got := errorCode(t, err); got != CodeSecretStoreInvalid {
				t.Fatalf("code = %q, want %q", got, CodeSecretStoreInvalid)
			}
		})
	}
	t.Run("invalid UTF-8", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "secrets.json.age")
		if err := os.WriteFile(path, []byte{'{', '"', 'T', 'O', 'K', 'E', 'N', '"', ':', '"', 0xff, '"', '}'}, 0o600); err != nil {
			t.Fatal(err)
		}
		provider, err := NewAgeProvider(AgeProviderOptions{Command: fake, Identities: []string{testIdentity(t)}, Stores: []Store{{Path: path}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = provider.Resolve(context.Background(), Scope("workspace"), []string{"TOKEN"})
		if got := errorCode(t, err); got != CodeSecretStoreInvalid {
			t.Fatalf("code = %q, want %q", got, CodeSecretStoreInvalid)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		target := writeStore(t, `{"TOKEN":"value"}`)
		link := filepath.Join(t.TempDir(), "secrets.json.age")
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlink unavailable: %v", err)
		}
		provider, err := NewAgeProvider(AgeProviderOptions{Command: fake, Identities: []string{testIdentity(t)}, Stores: []Store{{Path: link}}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = provider.Resolve(context.Background(), Scope("workspace"), []string{"TOKEN"})
		if got := errorCode(t, err); got != CodeSecretStoreInvalid {
			t.Fatalf("code = %q, want %q", got, CodeSecretStoreInvalid)
		}
	})
}

func TestAgeProviderSanitizesFailuresAndBoundsPlaintext(t *testing.T) {
	fake := writeFakeAge(t)
	t.Setenv("AGE_TEST_VERSION", "1.3.2")
	t.Setenv("AGE_TEST_STDERR", "sensitive-failure-detail")
	store := writeStore(t, `{"TOKEN":"a value long enough to exceed the limit"}`)
	provider, err := NewAgeProvider(AgeProviderOptions{Command: fake, Identities: []string{testIdentity(t)}, Stores: []Store{{Path: store}}, MaxPlaintext: 4})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Resolve(context.Background(), Scope("workspace"), []string{"TOKEN"})
	typed := errorValue(t, err)
	if typed.Code != CodeSecretStoreInvalid {
		t.Fatalf("code = %q", typed.Code)
	}
	if strings.Contains(typed.Error(), "sensitive-failure-detail") {
		t.Fatal("raw age stderr leaked through error")
	}
}

func TestAgeProviderRejectsOldAgeAndTimesOut(t *testing.T) {
	fake := writeFakeAge(t)
	t.Run("old version", func(t *testing.T) {
		t.Setenv("AGE_TEST_VERSION", "1.2.9")
		provider, err := NewAgeProvider(AgeProviderOptions{Command: fake, Identities: []string{testIdentity(t)}})
		if err != nil {
			t.Fatal(err)
		}
		_, err = provider.Resolve(context.Background(), Scope("workspace"), []string{"TOKEN"})
		if got := errorCode(t, err); got != CodeAgeVersionUnsupported {
			t.Fatalf("code = %q", got)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		t.Setenv("AGE_TEST_VERSION", "1.3.2")
		t.Setenv("AGE_TEST_MODE", "sleep")
		provider, err := NewAgeProvider(AgeProviderOptions{Command: fake, Identities: []string{testIdentity(t)}, Stores: []Store{{Path: writeStore(t, `{"TOKEN":"value"}`)}}, Timeout: 20 * time.Millisecond})
		if err != nil {
			t.Fatal(err)
		}
		_, err = provider.Resolve(context.Background(), Scope("workspace"), []string{"TOKEN"})
		if got := errorCode(t, err); got != CodeAgeDecryptionTimeout {
			t.Fatalf("code = %q", got)
		}
	})
}

func TestAgeProviderRequiresExplicitIdentities(t *testing.T) {
	store := writeStore(t, `{"TOKEN":"value"}`)
	provider, err := NewAgeProvider(AgeProviderOptions{Command: filepath.Join(t.TempDir(), "not-run"), Stores: []Store{{Path: store}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.Resolve(context.Background(), Scope("workspace"), []string{"TOKEN"})
	if got := errorCode(t, err); got != CodeProviderInvalid {
		t.Fatalf("code = %q, want %q", got, CodeProviderInvalid)
	}
}

type recordingProvider struct {
	scheme string
	values map[string]string
	calls  [][]string
	scopes []Scope
}

func (p *recordingProvider) Scheme() string { return p.scheme }

func (*recordingProvider) ValidateLocator(locator string) error {
	if locator == "" {
		return errors.New("empty")
	}
	return nil
}

func (p *recordingProvider) Resolve(_ context.Context, scope Scope, locators []string) (BatchResult, error) {
	p.calls = append(p.calls, append([]string(nil), locators...))
	p.scopes = append(p.scopes, scope)
	result := BatchResult{Values: map[string]string{}}
	for _, locator := range locators {
		value, ok := p.values[locator]
		if !ok {
			result.Missing = append(result.Missing, locator)
			continue
		}
		result.Values[locator] = value
	}
	return result, nil
}

func writeFakeAge(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "age")
	script := `#!/bin/sh
case "$1" in
  --version)
    printf 'v%s\n' "$AGE_TEST_VERSION"
    ;;
  --decrypt)
    if [ -n "$AGE_TEST_ARGUMENTS" ]; then
      printf '%s\n' "$@" > "$AGE_TEST_ARGUMENTS"
    fi
    if [ -n "$AGE_TEST_STDERR" ]; then
      printf '%s' "$AGE_TEST_STDERR" >&2
    fi
    if [ "$AGE_TEST_MODE" = "sleep" ]; then
      sleep 2
    fi
    if [ "$AGE_TEST_MODE" = "spawn" ]; then
      sleep 5 &
      printf '%s' "$!" > "$AGE_TEST_CHILD_PID"
      wait
    fi
    cat
    ;;
esac
`
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeStore(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "secrets.json.age")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func testIdentity(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "identity")
}

func errorValue(t *testing.T, err error) *Error {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	var typed *Error
	if !errors.As(err, &typed) {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	return typed
}

func errorCode(t *testing.T, err error) Code { return errorValue(t, err).Code }

func ExampleReference_String() {
	fmt.Println(Reference{Scheme: "age", Locator: "TOKEN"})
	// Output: age://TOKEN
}
