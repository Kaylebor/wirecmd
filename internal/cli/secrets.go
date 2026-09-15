package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/discovery"
	secretpkg "github.com/Kaylebor/wirecmd/internal/secrets"
)

type resolvedSecretSet struct {
	values    map[string]string
	protected []string
}

func (s resolvedSecretSet) lookup(reference string) (string, bool) {
	value, ok := s.values[reference]
	return value, ok
}

func resolveSelectedSecrets(ctx context.Context, configured config.Secrets, stores []string, automatic bool, snapshot *secretpkg.StoreSnapshot, values []config.Value, envLookup func(string) (string, bool)) (resolvedSecretSet, *appError) {
	identities := []string(nil)
	if configured.Age != nil {
		identities = make([]string, len(configured.Age.Identities))
		for index, identity := range configured.Age.Identities {
			identities[index] = identity.Path
		}
	}
	ageStores := make([]secretpkg.Store, len(stores))
	for index, path := range stores {
		ageStores[index] = secretpkg.Store{Path: path, RejectParentSymlink: automatic && filepath.Base(filepath.Dir(path)) == ".wirecmd"}
	}
	envProvider, err := secretpkg.NewEnvProvider(envLookup)
	if err != nil {
		return resolvedSecretSet{}, secretResolutionError(err)
	}
	ageProvider, err := secretpkg.NewAgeProvider(secretpkg.AgeProviderOptions{Identities: identities, Stores: ageStores, Snapshot: snapshot})
	if err != nil {
		return resolvedSecretSet{}, secretResolutionError(err)
	}
	registry, err := secretpkg.NewRegistry(envProvider, ageProvider)
	if err != nil {
		return resolvedSecretSet{}, secretResolutionError(err)
	}

	type selected struct {
		raw       string
		reference secretpkg.Reference
	}
	selectedValues := make([]selected, 0, len(values))
	references := make([]secretpkg.Reference, 0, len(values))
	seen := map[secretpkg.Reference]struct{}{}
	for _, value := range values {
		if !value.IsSecret() {
			continue
		}
		reference, err := registry.Parse(value.Text)
		if err != nil {
			return resolvedSecretSet{}, secretResolutionErrorAt(value, err)
		}
		selectedValues = append(selectedValues, selected{raw: value.Text, reference: reference})
		if _, exists := seen[reference]; !exists {
			seen[reference] = struct{}{}
			references = append(references, reference)
		}
	}
	resolved, err := registry.Resolve(ctx, secretpkg.Scope(config.ScopeWorkspace), references)
	if err != nil {
		return resolvedSecretSet{}, secretResolutionError(err)
	}
	result := resolvedSecretSet{values: make(map[string]string, len(selectedValues))}
	protected := map[string]struct{}{}
	for _, value := range selectedValues {
		resolvedValue := resolved[value.reference]
		result.values[value.raw] = resolvedValue
		if resolvedValue != "" {
			protected[resolvedValue] = struct{}{}
		}
	}
	result.protected = make([]string, 0, len(protected))
	for value := range protected {
		result.protected = append(result.protected, value)
	}
	sort.Slice(result.protected, func(i, j int) bool { return len(result.protected[i]) > len(result.protected[j]) })
	return result, nil
}

func resolveConfiguredValue(value config.Value, lookup func(string) (string, bool)) (config.ResolvedValue, error) {
	if !value.IsSecret() {
		return config.ResolvedValue{Text: value.Text}, nil
	}
	if lookup == nil {
		return config.ResolvedValue{}, fmt.Errorf("%s: no secret lookup", value.Path)
	}
	resolved, ok := lookup(value.Text)
	if !ok {
		if locator, env := isEnvReference(value.Text); env {
			resolved, ok = lookup(locator)
		}
	}
	if !ok {
		return config.ResolvedValue{}, fmt.Errorf("%s: configured secret is unavailable", value.Path)
	}
	return config.ResolvedValue{Text: resolved, Sensitive: true}, nil
}

func secretResolutionErrorAt(value config.Value, err error) *appError {
	mapped := secretResolutionError(err)
	if value.Path != "" {
		mapped.message = value.Path + ": " + mapped.message
	}
	return mapped
}

func secretResolutionError(err error) *appError {
	var providerError *secretpkg.Error
	if !errors.As(err, &providerError) {
		return configurationError("secret_provider_failed", "secret provider failed", "check the configured secret provider")
	}
	action := "check the configured secret provider"
	switch providerError.Code {
	case secretpkg.CodeSecretUnavailable:
		action = "provide the required secret values and retry"
	case secretpkg.CodeAgeUnavailable:
		action = "install age 1.3.0 or newer and ensure it is available on PATH"
	case secretpkg.CodeAgeVersionUnsupported:
		action = "upgrade age to version 1.3.0 or newer"
	case secretpkg.CodeAgeDecryptionTimeout:
		action = "complete hardware authorization within 60 seconds and retry"
	case secretpkg.CodeAgeDecryptionFailed:
		action = "check the configured age identities and encrypted secret stores"
	case secretpkg.CodeSecretStoreInvalid:
		action = "correct the encrypted age secret store"
	case secretpkg.CodeProviderInvalid:
		action = "configure at least one absolute age identity path"
	case secretpkg.CodeInvalidReference, secretpkg.CodeUnknownProvider:
		action = "correct the configured secret reference"
	}
	message := providerError.Message
	if providerError.Code == secretpkg.CodeSecretUnavailable && len(providerError.Missing) != 0 {
		names := make([]string, len(providerError.Missing))
		for index, reference := range providerError.Missing {
			names[index] = reference.String()
		}
		message = "configured secrets are unavailable: " + strings.Join(names, ", ")
	}
	return configurationError(string(providerError.Code), message, action)
}

func serverSecretValues(server config.Server) []config.Value {
	values := make([]config.Value, 0)
	if server.HTTP != nil {
		for _, field := range server.HTTP.Query {
			values = append(values, field.Value)
		}
		for _, field := range server.HTTP.Headers {
			values = append(values, field.Value)
		}
		if server.HTTP.OAuth != nil && server.HTTP.OAuth.ClientSecret != nil {
			values = append(values, *server.HTTP.OAuth.ClientSecret)
		}
		return values
	}
	return append(values, stdioSecretValues(server.Stdio)...)
}

func stdioSecretValues(stdio config.Stdio) []config.Value {
	values := append([]config.Value(nil), stdio.Args...)
	for _, assignment := range stdio.Env {
		values = append(values, assignment.Value)
	}
	return values
}

func lspSecretValues(matches []lspMatch) []config.Value {
	values := make([]config.Value, 0)
	for _, match := range matches {
		values = append(values, stdioSecretValues(match.Definition.Stdio)...)
	}
	return values
}

func isEnvReference(raw string) (string, bool) {
	scheme, locator, ok := strings.Cut(raw, "://")
	return locator, ok && strings.EqualFold(scheme, "env") && locator != ""
}

func selectedSecretStores(cwd string, configPaths []string, discovered bool, values []config.Value) ([]string, *appError) {
	if len(selectedAgeReferences(values)) == 0 {
		return nil, nil
	}
	stores, err := discovery.SecretStoreCandidates(cwd, configPaths, discovered)
	if err != nil {
		return nil, configurationError("secret_discovery_failed", "age secret stores could not be discovered", "check Wirecmd configuration and trust state")
	}
	return stores, nil
}

func selectedAgeReferences(values []config.Value) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if !value.IsSecret() {
			continue
		}
		scheme, locator, ok := strings.Cut(value.Text, "://")
		if ok && strings.EqualFold(scheme, "age") && locator != "" {
			seen["age://"+locator] = struct{}{}
		}
	}
	result := make([]string, 0, len(seen))
	for reference := range seen {
		result = append(result, reference)
	}
	sort.Strings(result)
	return result
}
