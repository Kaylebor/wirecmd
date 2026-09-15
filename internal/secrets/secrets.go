// Package secrets resolves provider-qualified secret references.
//
// It deliberately provides a closed, explicitly constructed registry. It is
// an internal routing boundary, not a plugin or provider-extension API.
package secrets

import (
	"context"
	"sort"
	"strings"
)

// Scope identifies the lifecycle context for one batch of secret resolution.
// The registry does not interpret it; callers keep separate scopes separate.
type Scope string

// Reference is a canonical provider-qualified secret reference. It is
// comparable so callers can use it as a map key without carrying plaintext.
type Reference struct {
	Scheme  string
	Locator string
}

func (r Reference) String() string { return r.Scheme + "://" + r.Locator }

// Code classifies a secret-resolution failure without requiring callers to
// parse an error string.
type Code string

const (
	CodeInvalidReference  Code = "invalid_secret_reference"
	CodeUnknownProvider   Code = "unknown_secret_provider"
	CodeDuplicateProvider Code = "duplicate_secret_provider"
	CodeProviderInvalid   Code = "secret_provider_invalid"
	CodeProviderFailed    Code = "secret_provider_failed"
	CodeSecretUnavailable Code = "secret_not_available"

	CodeAgeUnavailable        Code = "age_unavailable"
	CodeAgeVersionUnsupported Code = "age_version_unsupported"
	CodeAgeDecryptionTimeout  Code = "age_decryption_timeout"
	CodeAgeDecryptionFailed   Code = "age_decryption_failed"
	CodeSecretStoreInvalid    Code = "secret_store_invalid"
)

// Error is a safe, structured secret-resolution error. Message never contains
// provider stderr, plaintext, or identity contents. Missing contains requested
// locators only, never their resolved values.
type Error struct {
	Code    Code
	Message string
	Missing []Reference
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// BatchResult is the result of resolving one provider's deduplicated locators.
// A locator appears in exactly one of Values or Missing.
type BatchResult struct {
	Values  map[string]string
	Missing []string
}

// Provider resolves opaque locators for one normalized scheme. Provider
// implementations own their locator grammar and never receive other schemes.
type Provider interface {
	Scheme() string
	ValidateLocator(string) error
	Resolve(context.Context, Scope, []string) (BatchResult, error)
}

// Registry holds the fixed provider set selected by the CLI or daemon.
type Registry struct {
	providers map[string]Provider
}

// NewRegistry constructs a closed registry. Schemes are normalized to lower
// case and duplicate registrations are rejected.
func NewRegistry(providers ...Provider) (*Registry, error) {
	r := &Registry{providers: make(map[string]Provider, len(providers))}
	for _, provider := range providers {
		if provider == nil {
			return nil, &Error{Code: CodeProviderInvalid, Message: "secret provider is nil"}
		}
		scheme := normalizeScheme(provider.Scheme())
		if !validScheme(scheme) {
			return nil, &Error{Code: CodeProviderInvalid, Message: "secret provider scheme is invalid"}
		}
		if _, exists := r.providers[scheme]; exists {
			return nil, &Error{Code: CodeDuplicateProvider, Message: "secret provider scheme is registered more than once"}
		}
		r.providers[scheme] = provider
	}
	return r, nil
}

// Parse validates raw SCHEME://LOCATOR input and returns its canonical form.
func (r *Registry) Parse(raw string) (Reference, error) {
	reference, err := ParseReference(raw)
	if err != nil {
		return Reference{}, err
	}
	provider, ok := r.providers[reference.Scheme]
	if !ok {
		return Reference{}, &Error{Code: CodeUnknownProvider, Message: "secret reference uses an unknown provider"}
	}
	if err := provider.ValidateLocator(reference.Locator); err != nil {
		return Reference{}, providerError(err, CodeInvalidReference, "secret reference locator is invalid")
	}
	return reference, nil
}

// ParseReference validates provider-independent SCHEME://LOCATOR syntax and
// returns its canonical representation. A Registry additionally verifies that
// the scheme exists and applies the provider's locator grammar.
func ParseReference(raw string) (Reference, error) {
	index := strings.Index(raw, "://")
	if index <= 0 || index == len(raw)-3 {
		return Reference{}, &Error{Code: CodeInvalidReference, Message: "secret reference must use SCHEME://LOCATOR"}
	}
	reference := Reference{Scheme: normalizeScheme(raw[:index]), Locator: raw[index+3:]}
	if !validScheme(reference.Scheme) {
		return Reference{}, &Error{Code: CodeInvalidReference, Message: "secret reference scheme is invalid"}
	}
	return reference, nil
}

// Resolve batches references by provider in deterministic scheme order. It
// validates References again so manually constructed values cannot bypass a
// provider's locator rules.
func (r *Registry) Resolve(ctx context.Context, scope Scope, references []Reference) (map[Reference]string, error) {
	groups := make(map[string]map[string]Reference)
	for _, reference := range references {
		canonical, err := r.Parse(reference.String())
		if err != nil {
			return nil, err
		}
		if groups[canonical.Scheme] == nil {
			groups[canonical.Scheme] = map[string]Reference{}
		}
		groups[canonical.Scheme][canonical.Locator] = canonical
	}

	schemes := make([]string, 0, len(groups))
	for scheme := range groups {
		schemes = append(schemes, scheme)
	}
	sort.Strings(schemes)

	resolved := make(map[Reference]string, len(references))
	missing := make([]Reference, 0)
	for _, scheme := range schemes {
		group := groups[scheme]
		locators := make([]string, 0, len(group))
		for locator := range group {
			locators = append(locators, locator)
		}
		sort.Strings(locators)

		result, err := r.providers[scheme].Resolve(ctx, scope, locators)
		if err != nil {
			return nil, providerError(err, CodeProviderFailed, "secret provider failed")
		}
		for _, locator := range locators {
			reference := group[locator]
			value, ok := result.Values[locator]
			if ok {
				resolved[reference] = value
			}
		}
		for _, locator := range result.Missing {
			if reference, ok := group[locator]; ok {
				missing = append(missing, reference)
			}
		}
		for _, locator := range locators {
			if _, ok := result.Values[locator]; ok {
				continue
			}
			foundMissing := false
			for _, absent := range result.Missing {
				if absent == locator {
					foundMissing = true
					break
				}
			}
			if !foundMissing {
				return nil, &Error{Code: CodeProviderFailed, Message: "secret provider returned an incomplete batch result"}
			}
		}
	}
	if len(missing) != 0 {
		sort.Slice(missing, func(i, j int) bool { return missing[i].String() < missing[j].String() })
		return nil, &Error{Code: CodeSecretUnavailable, Message: "one or more requested secret values are unavailable", Missing: missing}
	}
	return resolved, nil
}

func providerError(err error, fallback Code, message string) error {
	if typed, ok := err.(*Error); ok {
		return typed
	}
	return &Error{Code: fallback, Message: message}
}

func normalizeScheme(scheme string) string { return strings.ToLower(scheme) }

func validScheme(scheme string) bool {
	if len(scheme) == 0 || !isASCIIAlpha(rune(scheme[0])) {
		return false
	}
	for _, character := range scheme[1:] {
		if isASCIIAlpha(character) || character >= '0' && character <= '9' || character == '+' || character == '.' || character == '-' {
			continue
		}
		return false
	}
	return true
}

func isASCIIAlpha(character rune) bool {
	return character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z'
}

// EnvProvider resolves environment values through an injected lookup function.
// The function must preserve the difference between an unset variable and an
// explicitly empty variable, as os.LookupEnv does.
type EnvProvider struct {
	lookup func(string) (string, bool)
}

// NewEnvProvider constructs an env provider. A nil lookup is invalid because
// it would make unset values indistinguishable from empty values.
func NewEnvProvider(lookup func(string) (string, bool)) (*EnvProvider, error) {
	if lookup == nil {
		return nil, &Error{Code: CodeProviderInvalid, Message: "environment secret provider requires a lookup function"}
	}
	return &EnvProvider{lookup: lookup}, nil
}

func (*EnvProvider) Scheme() string { return "env" }

func (*EnvProvider) ValidateLocator(locator string) error {
	if locator == "" {
		return &Error{Code: CodeInvalidReference, Message: "environment secret locator is empty"}
	}
	return nil
}

func (p *EnvProvider) Resolve(_ context.Context, _ Scope, locators []string) (BatchResult, error) {
	result := BatchResult{Values: make(map[string]string, len(locators))}
	for _, locator := range locators {
		value, ok := p.lookup(locator)
		if !ok {
			result.Missing = append(result.Missing, locator)
			continue
		}
		result.Values[locator] = value
	}
	return result, nil
}

func invalidLocator(message string) error {
	return &Error{Code: CodeInvalidReference, Message: message}
}
