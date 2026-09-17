package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"math/big"
	"strings"

	"github.com/go-json-experiment/json/jsontext"
)

// ProtectJSON registers one JSON value for whole-field semantic redaction.
// Provider text is redacted only when the complete field is equivalent JSON;
// JSON fragments embedded in ordinary prose are deliberately left alone.
func (r *redactor) ProtectJSON(raw []byte) {
	value, ok := comparableJSON(raw)
	if ok {
		r.protectedJSON = append(r.protectedJSON, value)
	}
}

func (r *redactor) matchesProtectedJSON(value string) bool {
	if len(r.protectedJSON) == 0 {
		return false
	}
	for _, protected := range r.protectedJSON {
		if decoded, ok := protected.(string); ok && decoded == value {
			return true
		}
	}
	candidate, ok := comparableJSON([]byte(value))
	if !ok {
		return false
	}
	for _, protected := range r.protectedJSON {
		if equalJSON(protected, candidate) {
			return true
		}
	}
	return false
}

// RedactPath also detects a protected JSON value occupying one complete path
// component. This covers file URI normalization without treating JSON-looking
// fragments embedded in ordinary filenames as protected values.
func (r *redactor) RedactPath(value string) string {
	redacted := r.Redact(value)
	valueComponents := splitPathComponents(value)
	for _, protected := range r.protectedJSON {
		decoded, ok := protected.(string)
		if !ok || decoded == "" {
			continue
		}
		if containsComponentSequence(valueComponents, splitPathComponents(decoded)) {
			return "[REDACTED]"
		}
	}
	for start := 0; start < len(value); start++ {
		if start > 0 && value[start-1] != '/' && value[start-1] != '\\' {
			continue
		}
		end := start
		for end < len(value) && value[end] != '/' && value[end] != '\\' {
			end++
		}
		decoder := json.NewDecoder(strings.NewReader(value[start:]))
		decoder.UseNumber()
		var candidate any
		if err := decoder.Decode(&candidate); err != nil {
			continue
		}
		end = start + int(decoder.InputOffset())
		if end < len(value) && value[end] != '/' && value[end] != '\\' {
			continue
		}
		for _, protected := range r.protectedJSON {
			if equalJSON(protected, candidate) {
				return "[REDACTED]"
			}
		}
	}
	return redacted
}

func splitPathComponents(value string) []string {
	return strings.FieldsFunc(value, func(character rune) bool {
		return character == '/' || character == '\\'
	})
}

func containsComponentSequence(value, protected []string) bool {
	if len(protected) == 0 || len(protected) > len(value) {
		return false
	}
	for start := 0; start <= len(value)-len(protected); start++ {
		matched := true
		for offset := range protected {
			if value[start+offset] != protected[offset] {
				matched = false
				break
			}
		}
		if matched {
			return true
		}
	}
	return false
}

func comparableJSON(raw []byte) (any, bool) {
	if !jsontext.Value(raw).IsValid() {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, false
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, false
	}
	return value, true
}

func equalJSON(left, right any) bool {
	switch left := left.(type) {
	case nil:
		return right == nil
	case bool:
		right, ok := right.(bool)
		return ok && left == right
	case string:
		right, ok := right.(string)
		return ok && left == right
	case json.Number:
		right, ok := right.(json.Number)
		return ok && equalJSONNumber(string(left), string(right))
	case []any:
		right, ok := right.([]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for index := range left {
			if !equalJSON(left[index], right[index]) {
				return false
			}
		}
		return true
	case map[string]any:
		right, ok := right.(map[string]any)
		if !ok || len(left) != len(right) {
			return false
		}
		for name, value := range left {
			other, ok := right[name]
			if !ok || !equalJSON(value, other) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func equalJSONNumber(left, right string) bool {
	leftValue, leftOK := normalizeJSONNumber(left)
	rightValue, rightOK := normalizeJSONNumber(right)
	return leftOK && rightOK && leftValue == rightValue
}

// normalizeJSONNumber produces an exact decimal scientific representation
// without expanding exponents or converting through a bounded numeric type.
func normalizeJSONNumber(value string) (string, bool) {
	sign := ""
	if strings.HasPrefix(value, "-") {
		sign, value = "-", value[1:]
	}
	exponent := new(big.Int)
	if index := strings.IndexAny(value, "eE"); index >= 0 {
		if _, ok := exponent.SetString(value[index+1:], 10); !ok {
			return "", false
		}
		value = value[:index]
	}
	fractionDigits := 0
	if index := strings.IndexByte(value, '.'); index >= 0 {
		fractionDigits = len(value) - index - 1
		value = value[:index] + value[index+1:]
	}
	value = strings.TrimLeft(value, "0")
	if value == "" {
		return "0", true
	}
	exponent.Sub(exponent, big.NewInt(int64(fractionDigits)))
	trimmed := strings.TrimRight(value, "0")
	exponent.Add(exponent, big.NewInt(int64(len(value)-len(trimmed))))
	return sign + trimmed + "e" + exponent.String(), true
}
