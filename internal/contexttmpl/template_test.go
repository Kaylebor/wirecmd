package contexttmpl

import (
	"errors"
	"testing"
)

func TestExpand(t *testing.T) {
	context := Context{CWD: "/work/repo/sub", ProjectRoot: "/work/repo", GlobalRoot: "/home/user/.config/wirecmd"}
	got, err := Expand("$$ ${wirecmd.cwd} ${wirecmd.project-root}/cache ${wirecmd.global-root}", context)
	if err != nil {
		t.Fatal(err)
	}
	want := "$ /work/repo/sub /work/repo/cache /home/user/.config/wirecmd"
	if got != want {
		t.Fatalf("Expand() = %q, want %q", got, want)
	}
}

func TestExpandIsNotRecursive(t *testing.T) {
	got, err := Expand("${wirecmd.cwd}", Context{CWD: "${wirecmd.project-root}", ProjectRoot: "/project"})
	if err != nil {
		t.Fatal(err)
	}
	if got != "${wirecmd.project-root}" {
		t.Fatalf("Expand() = %q", got)
	}
}

func TestTemplateValidation(t *testing.T) {
	for _, test := range []struct {
		name  string
		value string
	}{
		{name: "no reference", value: "literal"},
		{name: "escaped reference", value: "$${wirecmd.cwd}"},
		{name: "unknown", value: "${wirecmd.other}"},
		{name: "unterminated", value: "${wirecmd.cwd"},
		{name: "malformed dollar", value: "$wirecmd.cwd ${wirecmd.project-root}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := Validate(test.value); err == nil {
				t.Fatal("Validate() succeeded")
			}
		})
	}
}

func TestUnavailableContext(t *testing.T) {
	_, err := Expand("${wirecmd.global-root}", Context{})
	var unavailable *UnavailableError
	if !errors.As(err, &unavailable) || unavailable.Name != GlobalRoot {
		t.Fatalf("Expand() error = %v", err)
	}
}

func TestFragmentsAllowNoReferences(t *testing.T) {
	hasReference, err := ValidateFragment("literal")
	if err != nil || hasReference {
		t.Fatalf("ValidateFragment() = %v, %v", hasReference, err)
	}
	got, hasReference, err := ExpandFragment("cost $$5", Context{})
	if err != nil || hasReference || got != "cost $5" {
		t.Fatalf("ExpandFragment() = %q, %v, %v", got, hasReference, err)
	}
}
