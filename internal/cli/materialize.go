package cli

import (
	"errors"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/contexttmpl"
	"github.com/Kaylebor/wirecmd/internal/jsontemplate"
)

func materializationContext(context invocationContext) contexttmpl.Context {
	return contexttmpl.Context{CWD: context.CWD, ProjectRoot: context.ProjectRoot, GlobalRoot: context.GlobalRoot}
}

func materializeValue(value config.Value, context invocationContext) (config.Value, *appError) {
	if !value.IsTemplate() {
		return value, nil
	}
	expanded, err := contexttmpl.Expand(value.Text, materializationContext(context))
	if err != nil {
		var unavailable *contexttmpl.UnavailableError
		if errors.As(err, &unavailable) {
			return config.Value{}, configurationError("context_value_unavailable", err.Error(), "use this template only when the referenced invocation context is available")
		}
		return config.Value{}, configurationError("config_invalid", err.Error(), "correct the configured context template")
	}
	value.Kind = config.ValueLiteral
	value.Text = expanded
	return value, nil
}

func materializeStdio(stdio config.Stdio, context invocationContext) (config.Stdio, *appError) {
	result := stdio
	var appErr *appError
	result.Command, appErr = materializeValue(stdio.Command, context)
	if appErr != nil {
		return config.Stdio{}, appErr
	}
	result.Args = append([]config.Value(nil), stdio.Args...)
	for index := range result.Args {
		result.Args[index], appErr = materializeValue(result.Args[index], context)
		if appErr != nil {
			return config.Stdio{}, appErr
		}
	}
	result.Env = append([]config.Environment(nil), stdio.Env...)
	for index := range result.Env {
		result.Env[index].Value, appErr = materializeValue(result.Env[index].Value, context)
		if appErr != nil {
			return config.Stdio{}, appErr
		}
	}
	return result, nil
}

func materializeServer(server config.Server, context invocationContext) (config.Server, *appError) {
	result := server
	var appErr *appError
	if server.HTTP == nil {
		result.Stdio, appErr = materializeStdio(server.Stdio, context)
		if appErr != nil {
			return config.Server{}, appErr
		}
		if appErr := validateMaterializedServer(result); appErr != nil {
			return config.Server{}, appErr
		}
		return result, nil
	}
	http := *server.HTTP
	http.Endpoint, appErr = materializeValue(http.Endpoint, context)
	if appErr != nil {
		return config.Server{}, appErr
	}
	http.Query = append([]config.HTTPField(nil), http.Query...)
	for index := range http.Query {
		http.Query[index].Value, appErr = materializeValue(http.Query[index].Value, context)
		if appErr != nil {
			return config.Server{}, appErr
		}
	}
	http.Headers = append([]config.HTTPField(nil), http.Headers...)
	for index := range http.Headers {
		http.Headers[index].Value, appErr = materializeValue(http.Headers[index].Value, context)
		if appErr != nil {
			return config.Server{}, appErr
		}
	}
	if http.OAuth != nil {
		oauth := *http.OAuth
		oauth.ClientID, appErr = materializeValue(oauth.ClientID, context)
		if appErr != nil {
			return config.Server{}, appErr
		}
		if oauth.ClientSecret != nil {
			secret, secretErr := materializeValue(*oauth.ClientSecret, context)
			if secretErr != nil {
				return config.Server{}, secretErr
			}
			oauth.ClientSecret = &secret
		}
		oauth.RedirectURI, appErr = materializeValue(oauth.RedirectURI, context)
		if appErr != nil {
			return config.Server{}, appErr
		}
		http.OAuth = &oauth
	}
	result.HTTP = &http
	if appErr := validateMaterializedServer(result); appErr != nil {
		return config.Server{}, appErr
	}
	return result, nil
}

func validateMaterializedServer(server config.Server) *appError {
	field, err := config.ValidateMaterializedServer(server)
	if err == nil {
		return nil
	}
	switch field {
	case "endpoint":
		return configurationError("invalid_http_endpoint", err.Error(), "correct the configured HTTP endpoint template")
	case "client-id", "redirect-uri":
		return configurationError("oauth_configuration_invalid", err.Error(), "correct the configured OAuth template")
	default:
		return configurationError("config_invalid", err.Error(), "correct the configured provider template")
	}
}

func materializeLSP(definition config.LSP, context invocationContext) (config.LSP, *appError) {
	result := definition
	stdio, appErr := materializeStdio(definition.Stdio, context)
	if appErr != nil {
		return config.LSP{}, appErr
	}
	result.Stdio = stdio
	if definition.InitializationOptions != nil && definition.InitializationOptions.Template {
		raw, err := jsontemplate.Expand(definition.InitializationOptions.Raw, materializationContext(context))
		if err != nil {
			var unavailable *contexttmpl.UnavailableError
			if errors.As(err, &unavailable) {
				return config.LSP{}, configurationError("context_value_unavailable", err.Error(), "use this template only when the referenced invocation context is available")
			}
			return config.LSP{}, configurationError("config_invalid", err.Error(), "correct the configured initialization-options template")
		}
		value := *definition.InitializationOptions
		value.Raw = raw
		value.Template = false
		result.InitializationOptions = &value
	}
	return result, nil
}

func materializeLSPMatches(matches []lspMatch, context invocationContext) ([]lspMatch, *appError) {
	result := append([]lspMatch(nil), matches...)
	for index := range result {
		definition, appErr := materializeLSP(result[index].Definition, context)
		if appErr != nil {
			return nil, appErr
		}
		result[index].Definition = definition
	}
	return result, nil
}

func materializeLSPDefinitions(definitions []config.LSP, context invocationContext) ([]config.LSP, *appError) {
	result := append([]config.LSP(nil), definitions...)
	for index := range result {
		definition, appErr := materializeLSP(result[index], context)
		if appErr != nil {
			return nil, appErr
		}
		result[index] = definition
	}
	return result, nil
}

func materializeLSPStatusDefinitions(definitions []config.LSP, context invocationContext) ([]config.LSP, *appError) {
	result := append([]config.LSP(nil), definitions...)
	for index := range result {
		command, appErr := materializeValue(result[index].Stdio.Command, context)
		if appErr != nil {
			return nil, appErr
		}
		result[index].Stdio.Command = command
	}
	return result, nil
}
