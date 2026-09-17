// Package contexttmpl expands Wirecmd's fixed invocation-context references.
package contexttmpl

import "fmt"

const (
	CWD         = "wirecmd.cwd"
	ProjectRoot = "wirecmd.project-root"
	GlobalRoot  = "wirecmd.global-root"
)

// Context contains the only values available to a Wirecmd template.
type Context struct {
	CWD         string
	ProjectRoot string
	GlobalRoot  string
}

// UnavailableError reports a known reference whose value is unavailable.
type UnavailableError struct{ Name string }

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("context value %q is unavailable", e.Name)
}

// Validate checks a template without expanding it.
func Validate(input string) error {
	_, references, err := expand(input, nil)
	if err != nil {
		return err
	}
	if references == 0 {
		return fmt.Errorf("template must contain at least one context reference")
	}
	return nil
}

// Expand substitutes fixed context references once. Resolved values are never
// scanned again, and $$ emits one literal dollar sign.
func Expand(input string, context Context) (string, error) {
	result, references, err := expand(input, &context)
	if err != nil {
		return "", err
	}
	if references == 0 {
		return "", fmt.Errorf("template must contain at least one context reference")
	}
	return result, nil
}

func expand(input string, context *Context) (string, int, error) {
	result := make([]byte, 0, len(input))
	references := 0
	for index := 0; index < len(input); {
		if input[index] != '$' {
			result = append(result, input[index])
			index++
			continue
		}
		if index+1 < len(input) && input[index+1] == '$' {
			result = append(result, '$')
			index += 2
			continue
		}
		if index+1 >= len(input) || input[index+1] != '{' {
			return "", references, fmt.Errorf("malformed context reference at byte %d", index)
		}
		end := index + 2
		for end < len(input) && input[end] != '}' {
			end++
		}
		if end == len(input) {
			return "", references, fmt.Errorf("unterminated context reference")
		}
		name := input[index+2 : end]
		var value string
		switch name {
		case CWD:
			if context != nil {
				value = context.CWD
			}
		case ProjectRoot:
			if context != nil {
				value = context.ProjectRoot
			}
		case GlobalRoot:
			if context != nil {
				value = context.GlobalRoot
			}
		default:
			return "", references, fmt.Errorf("unknown context reference %q", name)
		}
		references++
		if context != nil {
			if value == "" {
				return "", references, &UnavailableError{Name: name}
			}
			result = append(result, value...)
		}
		index = end + 1
	}
	return string(result), references, nil
}
