package jsontemplate

import (
	"bytes"
	"errors"
	"testing"

	"github.com/Kaylebor/wirecmd/internal/contexttmpl"
)

func TestExpandChangesOnlyStringValues(t *testing.T) {
	raw := []byte(" { \"${wirecmd.cwd}\" : [1.2300e+40, \"${wirecmd.cwd}\", {\"key\":\"$$ ${wirecmd.project-root}\\n\"}], \"flag\": true } ")
	got, err := Expand(raw, contexttmpl.Context{CWD: `/tmp/a\b"c`, ProjectRoot: "/project"})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte(" { \"${wirecmd.cwd}\" : [1.2300e+40, \"/tmp/a\\\\b\\\"c\", {\"key\":\"$ /project\\n\"}], \"flag\": true } ")
	if !bytes.Equal(got, want) {
		t.Fatalf("Expand() = %s, want %s", got, want)
	}
}

func TestValidateRequiresValueReference(t *testing.T) {
	for name, raw := range map[string][]byte{
		"literal":  []byte(`{"value":"literal"}`),
		"key only": []byte(`{"${wirecmd.cwd}":"literal"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if err := Validate(raw); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestStrictJSONValidation(t *testing.T) {
	for name, raw := range map[string][]byte{
		"duplicate":    []byte(`{"key":"${wirecmd.cwd}","key":"two"}`),
		"trailing":     []byte(`"${wirecmd.cwd}" null`),
		"invalid utf8": {'"', '$', '{', 'w', 'i', 'r', 'e', 'c', 'm', 'd', '.', 'c', 'w', 'd', '}', 0xff, '"'},
	} {
		t.Run(name, func(t *testing.T) {
			if err := Validate(raw); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestExpandIsNonRecursiveAndReportsUnavailable(t *testing.T) {
	got, err := Expand([]byte(`"${wirecmd.cwd}"`), contexttmpl.Context{CWD: "${wirecmd.project-root}", ProjectRoot: "/project"})
	if err != nil || string(got) != `"${wirecmd.project-root}"` {
		t.Fatalf("Expand() = %s, %v", got, err)
	}
	_, err = Expand([]byte(`"${wirecmd.global-root}"`), contexttmpl.Context{})
	var unavailable *contexttmpl.UnavailableError
	if !errors.As(err, &unavailable) || unavailable.Name != contexttmpl.GlobalRoot {
		t.Fatalf("Expand() error = %v", err)
	}
}

func TestUntouchedValuePreservesBytes(t *testing.T) {
	raw := []byte("[\n  123456789012345678901234567890,\n  \"${wirecmd.cwd}\"\n]")
	got, err := Expand(raw, contexttmpl.Context{CWD: "/work"})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte("[\n  123456789012345678901234567890,\n  \"/work\"\n]")
	if !bytes.Equal(got, want) {
		t.Fatalf("Expand() = %s", got)
	}
}
