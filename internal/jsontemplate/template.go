// Package jsontemplate validates and materializes context templates embedded
// in JSON string values without changing the surrounding JSON representation.
package jsontemplate

import (
	"bytes"
	"fmt"

	"github.com/Kaylebor/wirecmd/internal/contexttmpl"
	"github.com/go-json-experiment/json/jsontext"
)

// Validate checks strict JSON syntax and template syntax in every string value.
// Object names are deliberately ignored. At least one value must contain a
// context reference.
func Validate(raw []byte) error {
	if !jsontext.Value(raw).IsValid() {
		return fmt.Errorf("invalid JSON value")
	}
	_, references, err := transform(raw, nil)
	if err != nil {
		return err
	}
	if references == 0 {
		return fmt.Errorf("template must contain at least one context reference in a JSON string value")
	}
	return nil
}

// Expand replaces context references in JSON string values only. It preserves
// all bytes outside replaced string tokens, including object order, whitespace,
// and number spelling.
func Expand(raw []byte, context contexttmpl.Context) ([]byte, error) {
	if !jsontext.Value(raw).IsValid() {
		return nil, fmt.Errorf("invalid JSON value")
	}
	result, references, err := transform(raw, &context)
	if err != nil {
		return nil, err
	}
	if references == 0 {
		return nil, fmt.Errorf("template must contain at least one context reference in a JSON string value")
	}
	return result, nil
}

type replacement struct {
	start int
	end   int
	value []byte
}

func transform(raw []byte, context *contexttmpl.Context) ([]byte, int, error) {
	scanner := jsonScanner{raw: raw, context: context}
	if err := scanner.value(); err != nil {
		return nil, 0, err
	}
	if context == nil || len(scanner.replacements) == 0 {
		return append([]byte(nil), raw...), scanner.references, nil
	}
	capacity := len(raw)
	for _, replacement := range scanner.replacements {
		capacity += len(replacement.value) - (replacement.end - replacement.start)
	}
	result := make([]byte, 0, capacity)
	previous := 0
	for _, replacement := range scanner.replacements {
		result = append(result, raw[previous:replacement.start]...)
		result = append(result, replacement.value...)
		previous = replacement.end
	}
	result = append(result, raw[previous:]...)
	return result, scanner.references, nil
}

type jsonScanner struct {
	raw          []byte
	index        int
	context      *contexttmpl.Context
	references   int
	replacements []replacement
}

func (s *jsonScanner) value() error {
	s.space()
	switch s.raw[s.index] {
	case '{':
		return s.object()
	case '[':
		return s.array()
	case '"':
		return s.stringValue()
	default:
		for s.index < len(s.raw) && !isDelimiter(s.raw[s.index]) {
			s.index++
		}
		return nil
	}
}

func (s *jsonScanner) object() error {
	s.index++
	s.space()
	if s.raw[s.index] == '}' {
		s.index++
		return nil
	}
	for {
		_, end := stringSpan(s.raw, s.index)
		s.index = end // Object names are never templates.
		s.space()
		s.index++ // ':'
		if err := s.value(); err != nil {
			return err
		}
		s.space()
		if s.raw[s.index] == '}' {
			s.index++
			return nil
		}
		s.index++ // ','
		s.space()
	}
}

func (s *jsonScanner) array() error {
	s.index++
	s.space()
	if s.raw[s.index] == ']' {
		s.index++
		return nil
	}
	for {
		if err := s.value(); err != nil {
			return err
		}
		s.space()
		if s.raw[s.index] == ']' {
			s.index++
			return nil
		}
		s.index++ // ','
	}
}

func (s *jsonScanner) stringValue() error {
	start, end := stringSpan(s.raw, s.index)
	s.index = end
	decoder := jsontext.NewDecoder(bytes.NewReader(s.raw[start:end]))
	token, err := decoder.ReadToken()
	if err != nil {
		return fmt.Errorf("decode JSON string value")
	}
	value := token.String()
	if s.context == nil {
		hasReference, err := contexttmpl.ValidateFragment(value)
		if err != nil {
			return fmt.Errorf("invalid template in JSON string value: %w", err)
		}
		if hasReference {
			s.references++
		}
		return nil
	}
	expanded, hasReference, err := contexttmpl.ExpandFragment(value, *s.context)
	if err != nil {
		return fmt.Errorf("invalid template in JSON string value: %w", err)
	}
	if !hasReference && expanded == value {
		return nil
	}
	if hasReference {
		s.references++
	}
	quoted, err := jsontext.AppendQuote(nil, expanded)
	if err != nil {
		return fmt.Errorf("encode materialized JSON string value")
	}
	s.replacements = append(s.replacements, replacement{start: start, end: end, value: quoted})
	return nil
}

func (s *jsonScanner) space() {
	for s.index < len(s.raw) {
		switch s.raw[s.index] {
		case ' ', '\t', '\r', '\n':
			s.index++
		default:
			return
		}
	}
}

func stringSpan(raw []byte, start int) (int, int) {
	index := start + 1
	for {
		switch raw[index] {
		case '\\':
			index += 2
		case '"':
			return start, index + 1
		default:
			index++
		}
	}
}

func isDelimiter(value byte) bool {
	switch value {
	case ' ', '\t', '\r', '\n', ',', ']', '}':
		return true
	default:
		return false
	}
}
