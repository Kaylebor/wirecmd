package cli

import (
	"io"
	"testing"
)

func TestRedactorProtectJSONMatchesEquivalentDocuments(t *testing.T) {
	redactor := newRedactor(nil, io.Discard)
	redactor.ProtectJSON([]byte(`{"b":"\u0061","a":[1.0,1e3],"large":123456789012345678901234567890}`))

	equivalent := "{\n  \"large\": 12345678901234567890123456789e1,\n  \"a\": [1, 1000],\n  \"b\": \"a\"\n}"
	if got := redactor.Redact(equivalent); got != "[REDACTED]" {
		t.Fatalf("equivalent JSON redaction = %q", got)
	}
	nearby := `{"a":[1,1000],"b":"a","large":123456789012345678901234567891}`
	if got := redactor.Redact(nearby); got != nearby {
		t.Fatalf("distinct JSON was redacted: %q", got)
	}
}

func TestRedactorProtectJSONDoesNotTreatScalarsAsSubstrings(t *testing.T) {
	redactor := newRedactor(nil, io.Discard)
	redactor.ProtectJSON([]byte(`1`))

	if got := redactor.Redact("version 1.0"); got != "version 1.0" {
		t.Fatalf("ordinary scalar-containing text = %q", got)
	}
	if got := redactor.Redact("1.0"); got != "[REDACTED]" {
		t.Fatalf("equivalent scalar JSON redaction = %q", got)
	}
}

func TestRedactorProtectJSONComparesUnboundedExponentsExactly(t *testing.T) {
	redactor := newRedactor(nil, io.Discard)
	redactor.ProtectJSON([]byte(`1e999999999999999999999999`))

	if got := redactor.Redact(`10e999999999999999999999998`); got != "[REDACTED]" {
		t.Fatalf("equivalent unbounded number redaction = %q", got)
	}
	if got := redactor.Redact(`2e999999999999999999999999`); got != `2e999999999999999999999999` {
		t.Fatalf("distinct unbounded number was redacted: %q", got)
	}
}

func TestRedactorProtectJSONDetectsCompletePathComponents(t *testing.T) {
	redactor := newRedactor(nil, io.Discard)
	redactor.ProtectJSON([]byte(`{"private":"x"}`))

	if got := redactor.RedactPath(`/workspace/{"private":"x"}/result.go`); got != "[REDACTED]" {
		t.Fatalf("protected JSON path component = %q", got)
	}
	ordinary := `/workspace/prefix-{"private":"x"}/result.go`
	if got := redactor.RedactPath(ordinary); got != ordinary {
		t.Fatalf("embedded filename fragment was redacted: %q", got)
	}
}

func TestRedactorProtectJSONDetectsDecodedStringRoots(t *testing.T) {
	redactor := newRedactor(nil, io.Discard)
	redactor.ProtectJSON([]byte(`"private-token"`))

	if got := redactor.Redact("private-token"); got != "[REDACTED]" {
		t.Fatalf("decoded string root = %q", got)
	}
	if got := redactor.Redact("prefix private-token"); got != "prefix private-token" {
		t.Fatalf("embedded string root was redacted: %q", got)
	}
	if got := redactor.RedactPath("/workspace/private-token/result.go"); got != "[REDACTED]" {
		t.Fatalf("decoded string path component = %q", got)
	}
}

func TestRedactorProtectJSONDoesNotMatchEmptyPathComponents(t *testing.T) {
	redactor := newRedactor(nil, io.Discard)
	redactor.ProtectJSON([]byte(`""`))

	path := "/workspace/result.go"
	if got := redactor.RedactPath(path); got != path {
		t.Fatalf("absolute path was redacted by empty string root: %q", got)
	}
}

func TestRedactorProtectJSONMatchesDecodedStringAcrossPathComponents(t *testing.T) {
	for _, protected := range []string{`"private/token"`, `"private\\token"`} {
		redactor := newRedactor(nil, nil)
		redactor.ProtectJSON([]byte(protected))

		if got := redactor.RedactPath("/workspace/private/token/result.go"); got != "[REDACTED]" {
			t.Fatalf("RedactPath() = %q for %s, want [REDACTED]", got, protected)
		}
		ordinary := "/workspace/xprivate/tokenx/result.go"
		if got := redactor.RedactPath(ordinary); got != ordinary {
			t.Fatalf("RedactPath() = %q for boundary-mismatched path, want %q", got, ordinary)
		}
	}
}

func TestRedactorProtectJSONScansPathsAfterSecretRedaction(t *testing.T) {
	redactor := newRedactor([]string{"TOKEN"}, io.Discard)
	redactor.ProtectJSON([]byte(`{"private":"x"}`))

	if got := redactor.RedactPath(`/workspace/TOKEN/{"private":"x"}/result.go`); got != "[REDACTED]" {
		t.Fatalf("RedactPath() = %q, want [REDACTED]", got)
	}
}
