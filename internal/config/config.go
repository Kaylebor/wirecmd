// Package config parses the first-slice Wirecmd configuration shape.
package config

import (
	"fmt"
	"io"
	"os"
	"strings"

	kdl "github.com/njreid/gokdl2"
	"github.com/njreid/gokdl2/document"
)

// Scope identifies the context that owns a configured server instance.
type Scope string

const (
	// ScopeWorkspace makes a server instance specific to its effective workspace.
	ScopeWorkspace Scope = "workspace"
)

// ValueKind distinguishes public literals from references whose resolved value
// must be treated as sensitive.
type ValueKind uint8

const (
	ValueLiteral ValueKind = iota
	ValueSecretReference
)

// Provenance identifies the configuration source and semantic location that
// supplied a model value.
type Provenance struct {
	File string
	Path string
}

// Value is a textual literal or an annotated secret reference. Text is the
// literal text for ValueLiteral and the unresolved reference for
// ValueSecretReference.
type Value struct {
	Kind ValueKind
	Text string
	Provenance
}

// IsSecret reports whether the value is an unresolved secret reference.
func (v Value) IsSecret() bool {
	return v.Kind == ValueSecretReference
}

// ResolvedValue is a value ready for a destination. Sensitive marks values
// that must be kept out of normal diagnostics.
type ResolvedValue struct {
	Text      string
	Sensitive bool
}

// ResolveEnv resolves the only first-slice secret provider, env://. lookup
// must preserve os.LookupEnv's distinction between an unset name and a name
// set to the empty string.
func (v Value) ResolveEnv(lookup func(string) (string, bool)) (ResolvedValue, error) {
	if v.Kind == ValueLiteral {
		return ResolvedValue{Text: v.Text}, nil
	}

	if lookup == nil {
		return ResolvedValue{}, fmt.Errorf("%s: no environment lookup", v.Path)
	}

	name := strings.TrimPrefix(v.Text, "env://")
	resolved, ok := lookup(name)
	if !ok {
		return ResolvedValue{}, fmt.Errorf("%s: environment variable %q is not set", v.Path, name)
	}
	return ResolvedValue{Text: resolved, Sensitive: true}, nil
}

// Environment assigns a value to a child process environment name.
type Environment struct {
	Name  string
	Value Value
	Provenance
}

// Stdio describes one local command transport.
type Stdio struct {
	Command string
	Args    []Value
	Env     []Environment
	Provenance
}

// Server is a named configured capability source.
type Server struct {
	Name  string
	Scope Scope
	Stdio Stdio
	Provenance
}

// Root is a configured workspace root. Resolving relative paths and applying
// caller overrides are daemon concerns, not parser behavior.
type Root struct {
	Path string
	Provenance
}

// Config is the source-syntax-independent first-slice configuration model.
type Config struct {
	Root    *Root
	Servers []Server
	Provenance
}

// Load parses a KDL 2 configuration file.
func Load(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	return Parse(path, f)
}

// Parse parses the first-slice Wirecmd KDL 2 configuration shape from r.
func Parse(file string, r io.Reader) (*Config, error) {
	doc, err := kdl.ParseWithOptions(r, kdl.ParseOptions{Version: kdl.ParseVersionV2})
	if err != nil {
		return nil, fmt.Errorf("parse KDL 2: %w", err)
	}
	return parseDocument(file, doc)
}

// ParseString is Parse for an in-memory configuration, mainly useful to
// callers that already hold the source text.
func ParseString(file, source string) (*Config, error) {
	return Parse(file, strings.NewReader(source))
}

func parseDocument(file string, doc *document.Document) (*Config, error) {
	if len(doc.Nodes) != 1 {
		return nil, fmt.Errorf("configuration: expected one wirecmd root node")
	}

	root := doc.Nodes[0]
	if nodeName(root) != "wirecmd" {
		return nil, fmt.Errorf("configuration: expected wirecmd root node")
	}
	if err := plainNode(root, "wirecmd"); err != nil {
		return nil, err
	}
	if len(root.Arguments) != 0 {
		return nil, fmt.Errorf("wirecmd: root does not accept arguments")
	}

	cfg := &Config{Provenance: provenance(file, "wirecmd")}
	seenServers := make(map[string]struct{}, len(root.Children))
	for _, child := range root.Children {
		switch nodeName(child) {
		case "root":
			if cfg.Root != nil {
				return nil, fmt.Errorf("wirecmd.root: duplicate root")
			}
			configRoot, err := parseRoot(file, child)
			if err != nil {
				return nil, err
			}
			cfg.Root = &configRoot
		case "server":
			server, err := parseServer(file, child)
			if err != nil {
				return nil, err
			}
			if _, exists := seenServers[server.Name]; exists {
				return nil, fmt.Errorf("%s: duplicate server", server.Path)
			}
			seenServers[server.Name] = struct{}{}
			cfg.Servers = append(cfg.Servers, server)
		default:
			return nil, fmt.Errorf("wirecmd: unknown child node %q", nodeName(child))
		}
	}
	if len(cfg.Servers) == 0 {
		return nil, fmt.Errorf("wirecmd: expected at least one server")
	}

	return cfg, nil
}

func parseRoot(file string, node *document.Node) (Root, error) {
	const path = "wirecmd.root"
	if err := plainNode(node, path); err != nil {
		return Root{}, err
	}
	if len(node.Arguments) != 1 || len(node.Children) != 0 {
		return Root{}, fmt.Errorf("%s: expected one path", path)
	}
	value, err := literalText(node.Arguments[0], path)
	if err != nil {
		return Root{}, err
	}
	if value == "" {
		return Root{}, fmt.Errorf("%s: path must not be empty", path)
	}
	return Root{Path: value, Provenance: provenance(file, path)}, nil
}

func parseServer(file string, node *document.Node) (Server, error) {
	if err := plainNode(node, "server"); err != nil {
		return Server{}, err
	}
	if len(node.Arguments) != 1 {
		return Server{}, fmt.Errorf("wirecmd.server: expected one name")
	}
	name, err := literalText(node.Arguments[0], "wirecmd.server")
	if err != nil {
		return Server{}, err
	}
	path := serverPath(name)
	server := Server{Name: name, Provenance: provenance(file, path)}

	var haveScope, haveStdio bool
	for _, child := range node.Children {
		switch nodeName(child) {
		case "scope":
			if haveScope {
				return Server{}, fmt.Errorf("%s.scope: duplicate scope", path)
			}
			scope, err := parseScope(child, path)
			if err != nil {
				return Server{}, err
			}
			server.Scope = scope
			haveScope = true
		case "stdio":
			if haveStdio {
				return Server{}, fmt.Errorf("%s.stdio: duplicate stdio", path)
			}
			stdio, err := parseStdio(file, child, path)
			if err != nil {
				return Server{}, err
			}
			server.Stdio = stdio
			haveStdio = true
		default:
			return Server{}, fmt.Errorf("%s: unknown child node %q", path, nodeName(child))
		}
	}
	if !haveScope || !haveStdio {
		return Server{}, fmt.Errorf("%s: scope and stdio are required", path)
	}

	return server, nil
}

func parseScope(node *document.Node, serverPath string) (Scope, error) {
	path := serverPath + ".scope"
	if err := plainNode(node, path); err != nil {
		return "", err
	}
	if len(node.Arguments) != 1 || len(node.Children) != 0 {
		return "", fmt.Errorf("%s: expected one value", path)
	}
	scope, err := literalText(node.Arguments[0], path)
	if err != nil {
		return "", err
	}
	if scope != string(ScopeWorkspace) {
		return "", fmt.Errorf("%s: unsupported scope %q", path, scope)
	}
	return Scope(scope), nil
}

func parseStdio(file string, node *document.Node, serverPath string) (Stdio, error) {
	path := serverPath + ".stdio"
	if err := plainNode(node, path); err != nil {
		return Stdio{}, err
	}
	if len(node.Arguments) != 1 {
		return Stdio{}, fmt.Errorf("%s: expected one executable", path)
	}
	command, err := literalText(node.Arguments[0], path)
	if err != nil {
		return Stdio{}, err
	}

	stdio := Stdio{Command: command, Provenance: provenance(file, path)}
	seenEnv := make(map[string]struct{}, len(node.Children))
	for _, child := range node.Children {
		switch nodeName(child) {
		case "arg":
			arg, err := parseSingleValue(file, child, fmt.Sprintf("%s.arg[%d]", path, len(stdio.Args)))
			if err != nil {
				return Stdio{}, err
			}
			stdio.Args = append(stdio.Args, arg)
		case "env":
			env, err := parseEnvironment(file, child, path)
			if err != nil {
				return Stdio{}, err
			}
			if _, exists := seenEnv[env.Name]; exists {
				return Stdio{}, fmt.Errorf("%s: duplicate environment name", env.Path)
			}
			seenEnv[env.Name] = struct{}{}
			stdio.Env = append(stdio.Env, env)
		default:
			return Stdio{}, fmt.Errorf("%s: unknown child node %q", path, nodeName(child))
		}
	}

	return stdio, nil
}

func parseSingleValue(file string, node *document.Node, path string) (Value, error) {
	if err := plainNode(node, path); err != nil {
		return Value{}, err
	}
	if len(node.Arguments) != 1 || len(node.Children) != 0 {
		return Value{}, fmt.Errorf("%s: expected one value", path)
	}
	return parseValue(node.Arguments[0], provenance(file, path))
}

func parseEnvironment(file string, node *document.Node, stdioPath string) (Environment, error) {
	path := stdioPath + ".env"
	if node.Type != "" || len(node.Arguments) != 0 || node.Properties.Len() != 1 || len(node.Children) != 0 {
		return Environment{}, fmt.Errorf("%s: expected one named value", path)
	}
	for name, rawValue := range node.Properties.Unordered() {
		entryPath := fmt.Sprintf("%s[%q]", path, name)
		value, err := parseValue(rawValue, provenance(file, entryPath))
		if err != nil {
			return Environment{}, err
		}
		return Environment{Name: name, Value: value, Provenance: provenance(file, entryPath)}, nil
	}
	// The length check above guarantees the loop returns. Keep the compiler's
	// control-flow requirement local rather than inventing a fallback shape.
	return Environment{}, fmt.Errorf("%s: expected one named value", path)
}

func parseValue(value *document.Value, p Provenance) (Value, error) {
	text, ok := value.Value.(string)
	if !ok {
		return Value{}, fmt.Errorf("%s: expected a text value", p.Path)
	}

	switch value.Type {
	case "":
		return Value{Kind: ValueLiteral, Text: text, Provenance: p}, nil
	case "secret":
		if !strings.HasPrefix(text, "env://") || len(strings.TrimPrefix(text, "env://")) == 0 {
			return Value{}, fmt.Errorf("%s: secret reference must be env://NAME", p.Path)
		}
		return Value{Kind: ValueSecretReference, Text: text, Provenance: p}, nil
	default:
		return Value{}, fmt.Errorf("%s: unsupported value annotation %q", p.Path, value.Type)
	}
}

func plainNode(node *document.Node, path string) error {
	if node.Type != "" || node.Properties.Exist() {
		return fmt.Errorf("%s: annotations and properties are not supported", path)
	}
	return nil
}

func literalText(value *document.Value, path string) (string, error) {
	if value.Type != "" {
		return "", fmt.Errorf("%s: value annotation %q is not allowed here", path, value.Type)
	}
	text, ok := value.Value.(string)
	if !ok {
		return "", fmt.Errorf("%s: expected a text value", path)
	}
	return text, nil
}

func provenance(file, path string) Provenance {
	return Provenance{File: file, Path: path}
}

func nodeName(node *document.Node) string {
	if node == nil || node.Name == nil {
		return ""
	}
	name, _ := node.Name.Value.(string)
	return name
}

func serverPath(name string) string {
	return fmt.Sprintf("wirecmd.server[%q]", name)
}
