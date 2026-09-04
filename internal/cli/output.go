package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"unicode"
)

type outputFormat uint8

const (
	formatAuto outputFormat = iota
	formatJSON
	formatPretty
)

type colorMode uint8

const (
	colorAuto colorMode = iota
	colorAlways
	colorNever
)

type presentation struct {
	pretty bool
	color  bool
}

var isOutputTerminal = terminalOutput

func parseOutputFormat(value string) (outputFormat, error) {
	switch value {
	case "auto":
		return formatAuto, nil
	case "json":
		return formatJSON, nil
	case "pretty":
		return formatPretty, nil
	default:
		return formatAuto, fmt.Errorf("invalid --format value %q (want auto, json, or pretty)", value)
	}
}

func parseColorMode(value string) (colorMode, error) {
	switch value {
	case "auto":
		return colorAuto, nil
	case "always":
		return colorAlways, nil
	case "never":
		return colorNever, nil
	default:
		return colorAuto, fmt.Errorf("invalid --color value %q (want auto, always, or never)", value)
	}
}

func presentationFor(opts options, out io.Writer) presentation {
	terminal := isOutputTerminal(out)
	pretty := opts.format == formatPretty || (opts.format == formatAuto && terminal)
	color := opts.color == colorAlways || (opts.color == colorAuto && terminal && os.Getenv("NO_COLOR") == "" && os.Getenv("TERM") != "dumb")
	if opts.color == colorNever {
		color = false
	}
	return presentation{pretty: pretty, color: color}
}

func terminalOutput(out io.Writer) bool {
	file, ok := out.(*os.File)
	if !ok {
		return false
	}
	return outputFileIsTerminal(file)
}

func writeOutput(writer io.Writer, value any, style presentation) {
	if !style.pretty {
		writeCompactJSON(writer, value, style.color)
		return
	}
	if envelope, ok := outputObject(value); ok {
		_, hasServers := envelope["servers"]
		_, hasTools := envelope["tools"]
		switch {
		case hasServers:
			writeServers(writer, envelope, style)
			return
		case hasTools:
			writeTools(writer, envelope, style)
			return
		case envelope["auth"] != nil:
			writeAuth(writer, envelope, style)
			return
		case envelope["trust"] != nil:
			writeTrust(writer, envelope, style)
			return
		case envelope["daemon"] != nil:
			writeDaemon(writer, envelope, style)
			return
		case envelope["reload"] != nil:
			writeReload(writer, envelope, style)
			return
		case envelope["error"] != nil:
			writeFailure(writer, envelope, style)
			return
		}
	}
	writePrettyJSON(writer, value, style.color)
}

func writeCompactJSON(writer io.Writer, value any, color bool) {
	if !color {
		writeJSON(writer, value)
		return
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		writeJSON(writer, value)
		return
	}
	_, _ = io.WriteString(writer, colorizeJSON(escapeTerminalControlsJSON(string(encoded)))+"\n")
}

func outputObject(value any) (map[string]any, bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, false
	}
	return object, true
}

func writeServers(writer io.Writer, envelope map[string]any, style presentation) {
	servers, _ := envelope["servers"].([]any)
	rows := make([][]string, 0, len(servers))
	for _, raw := range servers {
		server, _ := raw.(map[string]any)
		rows = append(rows, []string{singleLine(stringValue(server["name"])), singleLine(stringValue(server["scope"])), singleLine(stringValue(server["transport"]))})
	}
	writeTable(writer, []string{"SERVER", "SCOPE", "TRANSPORT"}, rows, style)
}

func writeTools(writer io.Writer, envelope map[string]any, style presentation) {
	server := singleLine(stringValue(envelope["server"]))
	if server != "" {
		writeLabel(writer, "Tools for", style)
		fmt.Fprintln(writer, " "+server+":")
	}
	tools, _ := envelope["tools"].([]any)
	rows := make([][]string, 0, len(tools))
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		name, title := singleLine(stringValue(tool["name"])), singleLine(stringValue(tool["title"]))
		description := readableText(stringValue(tool["description"]))
		rows = append(rows, []string{name, title, description})
	}
	writeTable(writer, []string{"NAME", "TITLE", "DESCRIPTION"}, rows, style)
}

func writeAuth(writer io.Writer, envelope map[string]any, style presentation) {
	auth, _ := envelope["auth"].(map[string]any)
	writeFields(writer, "Authentication", [][2]string{
		{"Server", singleLine(stringValue(auth["server"]))},
		{"Status", singleLine(stringValue(auth["status"]))},
		{"Registration", singleLine(stringValue(auth["registration"]))},
		{"Expires", singleLine(stringValue(auth["expires_at"]))},
	}, style)
}

func writeTrust(writer io.Writer, envelope map[string]any, style presentation) {
	trust, _ := envelope["trust"].(map[string]any)
	if _, listed := trust["workspaces"]; listed {
		workspaces, _ := trust["workspaces"].([]any)
		writeLabel(writer, "Trusted workspaces", style)
		fmt.Fprintln(writer, ":")
		for _, workspace := range workspaces {
			fmt.Fprintln(writer, "  "+singleLine(stringValue(workspace)))
		}
		return
	}
	fields := [][2]string{{"Workspace", singleLine(stringValue(trust["workspace"]))}, {"Status", singleLine(stringValue(trust["status"]))}, {"Root", singleLine(stringValue(trust["root"]))}}
	if trusted, ok := trust["trusted"].(bool); ok {
		fields = append(fields, [2]string{"Trusted", strconv.FormatBool(trusted)})
	}
	writeFields(writer, "Workspace trust", fields, style)
}

func writeDaemon(writer io.Writer, envelope map[string]any, style presentation) {
	daemon, _ := envelope["daemon"].(map[string]any)
	writeFields(writer, "Daemon", [][2]string{
		{"Status", singleLine(stringValue(daemon["status"]))},
		{"PID", scalarString(daemon["pid"])},
		{"Protocol", scalarString(daemon["protocol"])},
		{"Generation", scalarString(daemon["generation"])},
		{"Cached contexts", scalarString(daemon["cached_contexts"])},
		{"Active instances", scalarString(daemon["active_instances"])},
		{"Retiring instances", scalarString(daemon["retiring_instances"])},
		{"Broken instances", scalarString(daemon["broken_instances"])},
	}, style)
}

func writeReload(writer io.Writer, envelope map[string]any, style presentation) {
	reload, _ := envelope["reload"].(map[string]any)
	writeFields(writer, "Daemon reload", [][2]string{{"Contexts retired", scalarString(reload["contexts_retired"])}, {"Instances retired", scalarString(reload["instances_retired"])}}, style)
}

func writeFailure(writer io.Writer, envelope map[string]any, style presentation) {
	err, _ := envelope["error"].(map[string]any)
	writeFields(writer, "Error", [][2]string{
		{"Category", singleLine(stringValue(err["category"]))},
		{"Code", singleLine(stringValue(err["code"]))},
		{"Message", readableText(stringValue(err["message"]))},
		{"Action", readableText(stringValue(err["action"]))},
	}, style)
	if result, ok := envelope["result"]; ok && result != nil {
		writeLabel(writer, "Result", style)
		fmt.Fprintln(writer, ":")
		writePrettyJSON(writer, result, style.color)
	}
}

func writeFields(writer io.Writer, heading string, fields [][2]string, style presentation) {
	writeLabel(writer, heading, style)
	fmt.Fprintln(writer, ":")
	for _, field := range fields {
		if field[1] == "" {
			continue
		}
		writeLabel(writer, "  "+field[0], style)
		fmt.Fprintln(writer, ": "+field[1])
	}
}

func writeTable(writer io.Writer, headings []string, rows [][]string, style presentation) {
	widths := make([]int, len(headings))
	for i, heading := range headings {
		widths[i] = len(heading)
	}
	for _, row := range rows {
		for i, value := range row {
			if i < len(widths) && !strings.Contains(value, "\n") && len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}
	for i, heading := range headings {
		if i != 0 {
			fmt.Fprint(writer, "  ")
		}
		writeStyled(writer, fmt.Sprintf("%-*s", widths[i], heading), ansiBold, style)
	}
	fmt.Fprintln(writer)
	for _, row := range rows {
		for i, value := range row {
			if i != 0 {
				fmt.Fprint(writer, "  ")
			}
			if i == len(row)-1 && strings.Contains(value, "\n") {
				fmt.Fprint(writer, value)
				continue
			}
			fmt.Fprint(writer, fmt.Sprintf("%-*s", widths[i], value))
		}
		fmt.Fprintln(writer)
	}
}

func writePrettyJSON(writer io.Writer, value any, color bool) {
	encoded, err := json.Marshal(value)
	if err != nil {
		writeJSON(writer, value)
		return
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, encoded, "", "  "); err != nil {
		writeJSON(writer, value)
		return
	}
	text := pretty.String()
	text = escapeTerminalControlsJSON(text)
	if color {
		text = colorizeJSON(text)
	}
	_, _ = io.WriteString(writer, text+"\n")
}

const (
	ansiReset  = "\x1b[0m"
	ansiBold   = "\x1b[1m"
	ansiCyan   = "\x1b[36m"
	ansiGreen  = "\x1b[32m"
	ansiYellow = "\x1b[33m"
	ansiPurple = "\x1b[35m"
)

func writeLabel(writer io.Writer, label string, style presentation) {
	writeStyled(writer, label, ansiBold+ansiCyan, style)
}

func writeStyled(writer io.Writer, text, code string, style presentation) {
	if style.color {
		_, _ = io.WriteString(writer, code+text+ansiReset)
		return
	}
	_, _ = io.WriteString(writer, text)
}

func colorizeJSON(text string) string {
	var output strings.Builder
	for i := 0; i < len(text); {
		switch text[i] {
		case '"':
			end := i + 1
			for end < len(text) {
				if text[end] == '\\' {
					end += 2
					continue
				}
				if text[end] == '"' {
					end++
					break
				}
				end++
			}
			code := ansiGreen
			next := end
			for next < len(text) && (text[next] == ' ' || text[next] == '\n' || text[next] == '\t' || text[next] == '\r') {
				next++
			}
			if next < len(text) && text[next] == ':' {
				code = ansiCyan
			}
			output.WriteString(code + text[i:end] + ansiReset)
			i = end
		case 't', 'f', 'n':
			end := i
			for end < len(text) && (text[end] >= 'a' && text[end] <= 'z') {
				end++
			}
			word := text[i:end]
			if word == "true" || word == "false" || word == "null" {
				output.WriteString(ansiPurple + word + ansiReset)
			} else {
				output.WriteString(word)
			}
			i = end
		default:
			if (text[i] >= '0' && text[i] <= '9') || text[i] == '-' {
				end := i + 1
				for end < len(text) && strings.ContainsRune("0123456789.eE+-", rune(text[end])) {
					end++
				}
				output.WriteString(ansiYellow + text[i:end] + ansiReset)
				i = end
				continue
			}
			output.WriteByte(text[i])
			i++
		}
	}
	return output.String()
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func scalarString(value any) string {
	if value == nil {
		return ""
	}
	return fmt.Sprint(value)
}

func singleLine(value string) string { return sanitizeTerminalText(value, false) }

func readableText(value string) string { return sanitizeTerminalText(value, true) }

func sanitizeTerminalText(value string, allowLayout bool) string {
	var output strings.Builder
	for _, character := range value {
		if allowLayout && (character == '\n' || character == '\t') {
			output.WriteRune(character)
			continue
		}
		if unicode.IsControl(character) || isBidiControl(character) {
			if character <= 0xff {
				fmt.Fprintf(&output, "\\x%02X", character)
			} else {
				fmt.Fprintf(&output, "\\u%04X", character)
			}
			continue
		}
		output.WriteRune(character)
	}
	return output.String()
}

func escapeTerminalControlsJSON(value string) string {
	var output strings.Builder
	for _, character := range value {
		if (character >= 0x7f && character <= 0x9f) || isBidiControl(character) {
			fmt.Fprintf(&output, "\\u%04x", character)
			continue
		}
		output.WriteRune(character)
	}
	return output.String()
}

func isBidiControl(character rune) bool {
	return character == 0x061c || character == 0x200e || character == 0x200f || (character >= 0x202a && character <= 0x202e) || (character >= 0x2066 && character <= 0x2069)
}
