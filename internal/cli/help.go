package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type helpKind uint8

const (
	noHelp helpKind = iota
	globalHelp
	serverHelp
	toolHelp
)

type helpText string

// projectedArgument preserves command-line syntax until the active MCP tool
// schema can map it to an original JSON property name.
type projectedArgument struct {
	Name     string `json:"name"`
	Value    string `json:"value,omitempty"`
	ValueSet bool   `json:"value_set"`
}

type toolDescription struct {
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	OutputSchema json.RawMessage `json:"output_schema,omitempty"`
}

type helpResponse struct {
	Kind   helpKind        `json:"kind"`
	Server string          `json:"server"`
	Tools  []toolSummary   `json:"tools,omitempty"`
	Tool   toolDescription `json:"tool"`
}

type projectedProperty struct {
	Name        string
	Flag        string
	Schema      map[string]any
	Required    bool
	Projectable bool
}

func parseProjectedSuffix(tokens []string) ([]projectedArgument, map[string]any, *appError) {
	var projected []projectedArgument
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		if token == "--" {
			if i+2 != len(tokens) {
				return nil, nil, invocationError("raw_overlay_invalid", "-- must be followed by exactly one JSON object", "place one JSON object after -- and no further arguments")
			}
			overlay, err := decodeJSONObject([]byte(tokens[i+1]), "raw argument overlay")
			if err != nil {
				return nil, nil, err
			}
			return projected, overlay, nil
		}
		if !strings.HasPrefix(token, "--") || token == "--" || len(token) == 2 {
			return nil, nil, invocationError("projected_argument_invalid", fmt.Sprintf("expected a projected --name argument, got %q", token), "use focused help to see supported arguments")
		}
		body := strings.TrimPrefix(token, "--")
		name, value, hasValue := strings.Cut(body, "=")
		if name == "" {
			return nil, nil, invocationError("projected_argument_invalid", "projected argument name must not be empty", "use focused help to see supported arguments")
		}
		argument := projectedArgument{Name: name, Value: value, ValueSet: hasValue}
		if !hasValue && i+1 < len(tokens) && !strings.HasPrefix(tokens[i+1], "--") {
			argument.Value = tokens[i+1]
			argument.ValueSet = true
			i++
		}
		projected = append(projected, argument)
	}
	return projected, nil, nil
}

func directToolDescription(ctx context.Context, target connectionTarget, tool string, redactor *redactor) (toolDescription, *appError) {
	session, appErr := connectTarget(ctx, target, redactor)
	if appErr != nil {
		return toolDescription{}, appErr
	}
	defer session.Close()
	return sessionToolDescription(ctx, session, tool, redactor)
}

func directProjectedCall(ctx context.Context, target connectionTarget, tool string, projected []projectedArgument, overlay map[string]any, redactor *redactor) (toolResult, *appError) {
	session, appErr := connectTarget(ctx, target, redactor)
	if appErr != nil {
		return toolResult{}, appErr
	}
	defer session.Close()
	description, appErr := sessionToolProjection(ctx, session, tool)
	if appErr != nil {
		return toolResult{}, appErr
	}
	arguments, appErr := resolveProjectedArguments(description, projected, overlay)
	if appErr != nil {
		return toolResult{}, appErr
	}
	return sessionCall(ctx, session, tool, arguments, redactor)
}

// sessionToolProjection deliberately retains the upstream spelling privately.
// Presentation always uses sessionToolDescription, which redacts display data.
func sessionToolProjection(ctx context.Context, session *mcp.ClientSession, name string) (toolDescription, *appError) {
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return toolDescription{}, mcpOperationError(err, "tool_list_failed")
		}
		if tool.Name != name {
			continue
		}
		input, err := canonicalSchema(tool.InputSchema)
		if err != nil {
			return toolDescription{}, protocolError("tool_schema_invalid", err.Error(), "use the exact JSON invocation path or check the upstream tool schema")
		}
		return toolDescription{Name: tool.Name, InputSchema: input}, nil
	}
	return toolDescription{}, protocolError("tool_not_found", fmt.Sprintf("server did not advertise tool %q", name), "list the server tools and choose an advertised name")
}

func sessionToolDescription(ctx context.Context, session *mcp.ClientSession, name string, redactor *redactor) (toolDescription, *appError) {
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return toolDescription{}, mcpOperationError(err, "tool_list_failed")
		}
		if tool.Name != name {
			continue
		}
		input, err := redactedSchema(tool.InputSchema, redactor)
		if err != nil {
			return toolDescription{}, protocolError("tool_schema_invalid", err.Error(), "use the exact JSON invocation path or check the upstream tool schema")
		}
		output, err := redactedSchema(tool.OutputSchema, redactor)
		if err != nil {
			return toolDescription{}, protocolError("tool_schema_invalid", err.Error(), "use the exact JSON invocation path or check the upstream tool schema")
		}
		return toolDescription{Name: redactor.Redact(tool.Name), Title: redactor.Redact(tool.Title), Description: redactor.Redact(tool.Description), InputSchema: input, OutputSchema: output}, nil
	}
	return toolDescription{}, protocolError("tool_not_found", fmt.Sprintf("server did not advertise tool %q", name), "list the server tools and choose an advertised name")
}

func redactedSchema(schema any, redactor *redactor) (json.RawMessage, error) {
	encoded, err := canonicalSchema(schema)
	if err != nil || len(encoded) == 0 {
		return encoded, err
	}
	var decoded any
	if err := decodeJSON(encoded, &decoded); err != nil {
		return nil, err
	}
	encoded, err = json.Marshal(redactJSON(decoded, redactor))
	if err != nil {
		return nil, err
	}
	return encoded, nil
}

func canonicalSchema(schema any) (json.RawMessage, error) {
	if schema == nil {
		return nil, nil
	}
	encoded, err := json.Marshal(schema)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := decodeJSON(encoded, &decoded); err != nil {
		return nil, err
	}
	return json.Marshal(decoded)
}

func toolProperties(description toolDescription) ([]projectedProperty, bool) {
	var schema map[string]any
	if len(description.InputSchema) == 0 || decodeJSON(description.InputSchema, &schema) != nil || schema["type"] != "object" {
		return nil, false
	}
	rawProperties, ok := schema["properties"].(map[string]any)
	if !ok {
		return nil, true
	}
	required := map[string]bool{}
	if rawRequired, ok := schema["required"].([]any); ok {
		for _, raw := range rawRequired {
			if name, ok := raw.(string); ok {
				required[name] = true
			}
		}
	}
	properties := make([]projectedProperty, 0, len(rawProperties))
	flagCounts := map[string]int{}
	for name, raw := range rawProperties {
		property, ok := raw.(map[string]any)
		if !ok {
			property = map[string]any{}
		}
		flag, projectable := projectedFlag(name)
		if projectable {
			flagCounts[flag]++
		}
		properties = append(properties, projectedProperty{Name: name, Flag: flag, Schema: property, Required: required[name], Projectable: projectable})
	}
	for i := range properties {
		if properties[i].Projectable && flagCounts[properties[i].Flag] > 1 {
			properties[i].Projectable = false
		}
	}
	sort.Slice(properties, func(i, j int) bool { return properties[i].Name < properties[j].Name })
	return properties, true
}

func projectedFlag(name string) (string, bool) {
	if name == "" {
		return "", false
	}
	for _, r := range name {
		if !(r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-')) {
			return "", false
		}
	}
	var out []rune
	lastSeparator := true
	runes := []rune(name)
	for i, r := range runes {
		if r == '_' || r == '-' {
			if !lastSeparator {
				out = append(out, '-')
				lastSeparator = true
			}
			continue
		}
		if i > 0 && unicode.IsUpper(r) && !lastSeparator {
			previous := runes[i-1]
			nextLower := i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if unicode.IsLower(previous) || unicode.IsDigit(previous) || (unicode.IsUpper(previous) && nextLower) {
				out = append(out, '-')
			}
		}
		out = append(out, unicode.ToLower(r))
		lastSeparator = false
	}
	flag := strings.Trim(strings.TrimRight(string(out), "-"), "-")
	if flag == "" || strings.HasPrefix(flag, "-") {
		return "", false
	}
	return flag, true
}

func resolveProjectedArguments(description toolDescription, projected []projectedArgument, overlay map[string]any) (map[string]any, *appError) {
	if len(projected) == 0 {
		arguments := map[string]any{}
		for name, value := range overlay {
			arguments[name] = value
		}
		return arguments, nil
	}
	properties, safe := toolProperties(description)
	if !safe {
		return nil, invocationError("projected_arguments_unavailable", "the tool input schema cannot be safely projected", "use --json, --stdin, or an exact JSON call object")
	}
	byFlag := map[string]projectedProperty{}
	for _, property := range properties {
		if property.Projectable {
			byFlag[property.Flag] = property
		}
	}
	arguments := map[string]any{}
	for _, raw := range projected {
		property, ok := byFlag[raw.Name]
		if !ok {
			return nil, invocationError("projected_argument_unknown", fmt.Sprintf("unknown or ambiguous projected argument --%s", raw.Name), "use focused help or provide the property through -- JSON_OBJECT")
		}
		if _, exists := arguments[property.Name]; exists {
			return nil, invocationError("projected_argument_duplicate", fmt.Sprintf("argument %q was supplied more than once", property.Name), "supply each argument once")
		}
		value, err := parseProjectedValue(property, raw)
		if err != nil {
			return nil, err
		}
		arguments[property.Name] = value
	}
	for name, value := range overlay {
		if _, exists := arguments[name]; exists {
			return nil, invocationError("projected_argument_duplicate", fmt.Sprintf("argument %q was supplied both as a flag and in the raw overlay", name), "supply each argument once")
		}
		arguments[name] = value
	}
	return arguments, nil
}

func parseProjectedValue(property projectedProperty, raw projectedArgument) (any, *appError) {
	typeName, direct := property.Schema["type"].(string)
	if typeName == "boolean" && !raw.ValueSet {
		return true, nil
	}
	if !raw.ValueSet {
		return nil, invocationError("projected_value_required", fmt.Sprintf("--%s requires a value", property.Flag), "supply a value or use the raw JSON overlay")
	}
	if !direct || (typeName != "string" && typeName != "number" && typeName != "integer" && typeName != "boolean") {
		var value any
		if err := decodeJSON([]byte(raw.Value), &value); err != nil {
			return nil, invocationError("projected_json_invalid", fmt.Sprintf("--%s requires one valid JSON value: %v", property.Flag, err), "quote one JSON value for this argument")
		}
		return value, nil
	}
	switch typeName {
	case "string":
		return raw.Value, nil
	case "boolean":
		if raw.Value == "true" {
			return true, nil
		}
		if raw.Value == "false" {
			return false, nil
		}
		return nil, invocationError("projected_boolean_invalid", fmt.Sprintf("--%s must be true or false", property.Flag), "use --"+property.Flag+" or --"+property.Flag+"=true|false")
	case "number", "integer":
		var value any
		if err := decodeJSON([]byte(raw.Value), &value); err != nil {
			return nil, invocationError("projected_number_invalid", fmt.Sprintf("--%s requires a JSON number", property.Flag), "supply a JSON number")
		}
		number, ok := value.(json.Number)
		if !ok {
			return nil, invocationError("projected_number_invalid", fmt.Sprintf("--%s requires a JSON %s", property.Flag, typeName), "supply a JSON "+typeName)
		}
		return number, nil
	}
	return nil, invocationError("projected_value_invalid", "unsupported projected argument type", "use the raw JSON overlay")
}

func globalHelpText() string {
	return "Usage:\n  " + usage + "\n\nDiscover configured servers, inspect focused help, then invoke tools. Configuration is discovered automatically unless --config is supplied.\n\nFocused help:\n  wirecmd [client flags] --help <server>\n  wirecmd [client flags] --help <server> <tool>\n\nWorkspace trust:\n  wirecmd config trust [PATH]\n  wirecmd config untrust [PATH]\n  wirecmd config trust status [PATH]\n  wirecmd config trust list\n\nOAuth credentials:\n  wirecmd auth login <server>\n  wirecmd auth status <server>\n  wirecmd auth logout <server>\n\nProtected HTTP calls may open a browser when stdin and stderr are TTYs. Set WIRECMD_NONINTERACTIVE=1 to require an actionable structured authentication error. Use --direct for deliberate daemonless testing; normal commands are daemon-backed.\n\nTool arguments follow <server> <tool>. Use -- JSON_OBJECT for a raw argument overlay.\n"
}

func renderServerHelp(server string, tools []toolSummary) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Usage:\n  wirecmd [client flags] %s <tool> [tool arguments]\n\nTools for %s:\n", server, server)
	for _, tool := range tools {
		if tool.Description == "" {
			fmt.Fprintf(&text, "  %s\n", tool.Name)
		} else {
			fmt.Fprintf(&text, "  %-24s %s\n", tool.Name, tool.Description)
		}
	}
	fmt.Fprintf(&text, "\nInspect one tool:\n  wirecmd [client flags] --help %s <tool>\n", server)
	return text.String()
}

func renderToolHelp(server string, tool toolDescription) string {
	var text strings.Builder
	fmt.Fprintf(&text, "Usage:\n  wirecmd [client flags] %s %s [tool arguments]\n", server, tool.Name)
	if tool.Title != "" || tool.Description != "" {
		fmt.Fprint(&text, "\nTool:\n")
		if tool.Title != "" {
			fmt.Fprintf(&text, "  %s\n", tool.Title)
		}
		if tool.Description != "" {
			fmt.Fprintf(&text, "  %s\n", tool.Description)
		}
	}
	properties, safe := toolProperties(tool)
	if safe && len(properties) != 0 {
		fmt.Fprint(&text, "\nArguments:\n")
		for _, property := range properties {
			if property.Projectable {
				fmt.Fprintf(&text, "  --%-22s %s", property.Flag, displayType(property.Schema))
				if property.Required {
					fmt.Fprint(&text, "  required")
				} else {
					fmt.Fprint(&text, "  optional")
				}
				fmt.Fprintf(&text, "\n                          JSON: %s (%s)\n", property.Name, displayType(property.Schema))
				if !directScalar(property.Schema) {
					fmt.Fprintf(&text, "                          Value: one JSON value for %q, not {%s: ...}\n", property.Name, strconv.Quote(property.Name))
					if shape, ok := projectedShape(property.Schema); ok {
						fmt.Fprintf(&text, "                          Illustrative shape (consult Input schema): --%s %s\n", property.Flag, shellQuote(shape))
					}
				}
				if defaultValue, ok := property.Schema["default"]; ok {
					fmt.Fprintf(&text, "                          Default: %s\n", compactJSON(defaultValue))
				}
				if enum, ok := property.Schema["enum"]; ok {
					fmt.Fprintf(&text, "                          Enum: %s\n", compactJSON(enum))
				}
			} else {
				fmt.Fprintf(&text, "  %s  JSON-only (%s)", property.Name, displayType(property.Schema))
				if property.Required {
					fmt.Fprint(&text, " required")
				}
				fmt.Fprint(&text, "\n")
			}
		}
	} else {
		fmt.Fprint(&text, "\nArguments:\n  The input schema cannot be safely projected; use exact JSON.\n")
	}
	fmt.Fprintf(&text, "\nRaw overlay:\n  wirecmd [client flags] %s %s [projected arguments] -- '{\"property\": \"value\"}'\n", server, tool.Name)
	fmt.Fprintf(&text, "\nExact JSON fallback:\n  wirecmd [client flags] %s '{\"tool\":%s,\"arguments\":%s}'\n", server, strconv.Quote(tool.Name), exactArgumentsTemplate(tool))
	fmt.Fprint(&text, "\nInput schema:\n")
	text.WriteString(prettyJSON(tool.InputSchema))
	if len(tool.OutputSchema) != 0 {
		fmt.Fprint(&text, "\nOutput schema:\n")
		text.WriteString(prettyJSON(tool.OutputSchema))
	}
	return text.String()
}

func displayType(schema map[string]any) string {
	if typeName, ok := schema["type"].(string); ok {
		return typeName
	}
	return "JSON"
}

func directScalar(schema map[string]any) bool {
	typeName, ok := schema["type"].(string)
	return ok && (typeName == "string" || typeName == "number" || typeName == "integer" || typeName == "boolean")
}

func compactJSON(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "<unavailable>"
	}
	return string(encoded)
}

func prettyJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "  <not provided>\n"
	}
	var text bytes.Buffer
	if err := json.Indent(&text, raw, "  ", "  "); err != nil {
		return "  <unavailable>\n"
	}
	return "  " + text.String() + "\n"
}

func exactArgumentsTemplate(tool toolDescription) string {
	properties, safe := toolProperties(tool)
	if !safe {
		return "{}"
	}
	arguments := map[string]any{}
	for _, property := range properties {
		if property.Required {
			arguments[property.Name] = placeholderValue(property.Schema)
		}
	}
	return compactJSON(arguments)
}

func placeholderValue(schema map[string]any) any {
	if enum, ok := schema["enum"].([]any); ok && len(enum) != 0 {
		return enum[0]
	}
	switch schema["type"] {
	case "string":
		return "..."
	case "number", "integer":
		return 0
	case "boolean":
		return false
	case "array":
		return []any{}
	case "object":
		return map[string]any{}
	default:
		return nil
	}
}

// projectedShape is illustrative only; the Input schema remains authoritative.
func projectedShape(schema map[string]any) (string, bool) {
	value, ok := projectedShapeValue(schema)
	if !ok {
		return "", false
	}
	return compactJSON(value), true
}

func projectedShapeValue(schema map[string]any) (any, bool) {
	typeName, ok := schema["type"].(string)
	if !ok {
		return nil, false
	}
	switch typeName {
	case "string":
		return "...", true
	case "number", "integer":
		return 0, true
	case "boolean":
		return false, true
	case "array":
		if rawItems, found := schema["items"]; found {
			items, ok := rawItems.(map[string]any)
			if !ok {
				return nil, false
			}
			item, ok := projectedShapeValue(items)
			if !ok {
				return nil, false
			}
			return []any{item}, true
		}
		return []any{}, true
	case "object":
		rawRequired, required := schema["required"]
		if !required {
			return map[string]any{}, true
		}
		names, ok := rawRequired.([]any)
		if !ok {
			return nil, false
		}
		properties, ok := schema["properties"].(map[string]any)
		if !ok {
			return nil, false
		}
		value := make(map[string]any, len(names))
		for _, rawName := range names {
			name, ok := rawName.(string)
			if !ok {
				return nil, false
			}
			rawProperty, found := properties[name]
			if !found {
				return nil, false
			}
			property, ok := rawProperty.(map[string]any)
			if !ok {
				return nil, false
			}
			shape, ok := projectedShapeValue(property)
			if !ok {
				return nil, false
			}
			value[name] = shape
		}
		return value, true
	default:
		return nil, false
	}
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func renderDaemonHelp(value any) (any, *appError) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, transportError("daemon_response_invalid", err.Error(), "restart the Wirecmd daemon")
	}
	var response helpResponse
	if err := decodeJSON(encoded, &response); err != nil {
		return nil, transportError("daemon_response_invalid", err.Error(), "restart the Wirecmd daemon")
	}
	switch response.Kind {
	case serverHelp:
		return helpText(renderServerHelp(response.Server, response.Tools)), nil
	case toolHelp:
		return helpText(renderToolHelp(response.Server, response.Tool)), nil
	default:
		return nil, transportError("daemon_response_invalid", "daemon returned an invalid help response", "restart the Wirecmd daemon")
	}
}
