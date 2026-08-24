// Package config parses and composes the first-slice Wirecmd configuration.
package config

import (
	"fmt"
	"io"
	"net/url"
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

// Stdio describes one complete local command transport.
type Stdio struct {
	Command           string
	CommandProvenance Provenance
	Args              []Value
	Env               []Environment
	Provenance
}

// HTTP describes one complete modern Streamable HTTP transport.
// Query and header child nodes are intentionally deferred until their typed
// value and secret-destination semantics are implemented.
type HTTP struct {
	Endpoint           string
	EndpointProvenance Provenance
	Provenance
}

// Server is a named complete configured capability source.
type Server struct {
	Name            string
	Scope           Scope
	ScopeProvenance Provenance
	Stdio           Stdio
	HTTP            *HTTP
	Provenance
}

// Root is a configured workspace root. Resolving relative paths and applying
// caller overrides are daemon concerns, not configuration composition.
type Root struct {
	Path string
	Provenance
}

// Source is one partially specified KDL configuration layer. It is valid for
// a source to omit fields that are supplied by a weaker layer.
type Source struct {
	Root    *Root
	Servers []ServerSource
	Provenance
}

// ServerSource is a potentially partial server layer.
type ServerSource struct {
	Name            string
	Scope           *Scope
	ScopeProvenance Provenance
	Stdio           *StdioSource
	HTTP            *HTTPSource
	Provenance
}

// StdioSource is a potentially partial stdio layer. A non-empty Args slice
// replaces inherited arguments; Env entries merge by name.
type StdioSource struct {
	Command           *string
	CommandProvenance Provenance
	Args              []Value
	Env               []Environment
	Provenance
}

// HTTPSource is a potentially partial HTTP transport layer. A present source
// selects HTTP even when it omits the endpoint to inherit one from a weaker
// HTTP layer.
type HTTPSource struct {
	Endpoint           *string
	EndpointProvenance Provenance
	Provenance
}

// Config is the complete source-syntax-independent configuration model
// consumed by execution.
type Config struct {
	Root    *Root
	Servers []Server
}

// Load parses one KDL 2 configuration source file.
func Load(path string) (*Source, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("load configuration %q: %w", path, err)
	}
	defer f.Close()

	source, err := Parse(path, f)
	if err != nil {
		return nil, fmt.Errorf("load configuration %q: %w", path, err)
	}
	return source, nil
}

// LoadEffective loads paths from weakest to strongest, composes their partial
// sources, and validates the resulting complete configuration.
func LoadEffective(paths []string) (*Config, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("configuration: at least one config file is required")
	}

	sources := make([]*Source, 0, len(paths))
	for _, path := range paths {
		source, err := Load(path)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	return Compose(sources...)
}

// Parse parses one possibly partial first-slice Wirecmd KDL 2 source from r.
func Parse(file string, r io.Reader) (*Source, error) {
	doc, err := kdl.ParseWithOptions(r, kdl.ParseOptions{Version: kdl.ParseVersionV2})
	if err != nil {
		return nil, fmt.Errorf("parse KDL 2: %w", err)
	}
	return parseDocument(file, doc)
}

// ParseString is Parse for source text already held in memory.
func ParseString(file, source string) (*Source, error) {
	return Parse(file, strings.NewReader(source))
}

// Compose combines sources from weakest to strongest and validates the
// effective result. Scalars use the strongest present value. Named servers and
// environment entries retain first-introduction order while later sources
// replace matching entries.
func Compose(sources ...*Source) (*Config, error) {
	config := &Config{}
	serverIndex := make(map[string]int)

	for _, source := range sources {
		if source.Root != nil {
			root := *source.Root
			config.Root = &root
		}

		for _, partial := range source.Servers {
			index, exists := serverIndex[partial.Name]
			if !exists {
				index = len(config.Servers)
				serverIndex[partial.Name] = index
				config.Servers = append(config.Servers, Server{
					Name:       partial.Name,
					Provenance: partial.Provenance,
				})
			}

			server := &config.Servers[index]
			server.Provenance = partial.Provenance
			if partial.Scope != nil {
				server.Scope = *partial.Scope
				server.ScopeProvenance = partial.ScopeProvenance
			}
			if partial.Stdio != nil {
				if server.HTTP != nil {
					server.HTTP = nil
					server.Stdio = Stdio{}
				}
				composeStdio(&server.Stdio, partial.Stdio)
			}
			if partial.HTTP != nil {
				if server.Stdio.Provenance != (Provenance{}) {
					server.Stdio = Stdio{}
					server.HTTP = nil
				}
				if server.HTTP == nil {
					server.HTTP = &HTTP{}
				}
				composeHTTP(server.HTTP, partial.HTTP)
			}
		}
	}

	if err := validate(config); err != nil {
		return nil, err
	}
	return config, nil
}

func composeHTTP(result *HTTP, partial *HTTPSource) {
	result.Provenance = partial.Provenance
	if partial.Endpoint != nil {
		result.Endpoint = *partial.Endpoint
		result.EndpointProvenance = partial.EndpointProvenance
	}
}

func composeStdio(result *Stdio, partial *StdioSource) {
	result.Provenance = partial.Provenance
	if partial.Command != nil {
		result.Command = *partial.Command
		result.CommandProvenance = partial.CommandProvenance
	}
	if len(partial.Args) != 0 {
		result.Args = append([]Value(nil), partial.Args...)
	}

	envIndex := make(map[string]int, len(result.Env))
	for index, env := range result.Env {
		envIndex[env.Name] = index
	}
	for _, env := range partial.Env {
		if index, exists := envIndex[env.Name]; exists {
			result.Env[index] = env
			continue
		}
		envIndex[env.Name] = len(result.Env)
		result.Env = append(result.Env, env)
	}
}

func validate(config *Config) error {
	if config.Root != nil && config.Root.Path == "" {
		return validationError(config.Root.Provenance, config.Root.Provenance.Path, "path must not be empty")
	}
	if len(config.Servers) == 0 {
		return fmt.Errorf("configuration: expected at least one server")
	}
	for _, server := range config.Servers {
		path := serverPath(server.Name)
		if server.Scope == "" {
			return validationError(server.Provenance, path+".scope", "scope is required")
		}
		if server.Scope != ScopeWorkspace {
			return validationError(server.ScopeProvenance, path+".scope", "unsupported scope %q", server.Scope)
		}
		if server.HTTP != nil {
			if server.HTTP.Endpoint == "" {
				return validationError(server.HTTP.Provenance, path+".http", "endpoint is required")
			}
			if err := validateHTTPEndpoint(server.HTTP.Endpoint); err != nil {
				return validationError(server.HTTP.EndpointProvenance, path+".http", "%v", err)
			}
		} else {
			if server.Stdio.Provenance == (Provenance{}) {
				return validationError(server.Provenance, path+".stdio", "stdio or http is required")
			}
			if server.Stdio.Command == "" {
				return validationError(server.Stdio.Provenance, path+".stdio", "executable is required")
			}
		}
	}
	return nil
}

func validateHTTPEndpoint(endpoint string) error {
	parsed, err := url.Parse(endpoint)
	if err != nil || !parsed.IsAbs() || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return fmt.Errorf("endpoint must be an absolute http or https URL with a host")
	}
	if parsed.User != nil {
		return fmt.Errorf("endpoint must not contain URL user information")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("endpoint must not contain a fragment")
	}
	return nil
}

func validationError(provenance Provenance, path, format string, args ...any) error {
	message := fmt.Sprintf(format, args...)
	if provenance.File == "" {
		return fmt.Errorf("%s: %s", path, message)
	}
	return fmt.Errorf("%s: %s: %s", provenance.File, path, message)
}

func parseDocument(file string, doc *document.Document) (*Source, error) {
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

	source := &Source{Provenance: provenance(file, "wirecmd")}
	seenServers := make(map[string]struct{}, len(root.Children))
	for _, child := range root.Children {
		switch nodeName(child) {
		case "root":
			if source.Root != nil {
				return nil, fmt.Errorf("wirecmd.root: duplicate root")
			}
			configRoot, err := parseRoot(file, child)
			if err != nil {
				return nil, err
			}
			source.Root = &configRoot
		case "server":
			server, err := parseServer(file, child)
			if err != nil {
				return nil, err
			}
			if _, exists := seenServers[server.Name]; exists {
				return nil, fmt.Errorf("%s: duplicate server", server.Path)
			}
			seenServers[server.Name] = struct{}{}
			source.Servers = append(source.Servers, server)
		default:
			return nil, fmt.Errorf("wirecmd: unknown child node %q", nodeName(child))
		}
	}
	return source, nil
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
	return Root{Path: value, Provenance: provenance(file, path)}, nil
}

func parseServer(file string, node *document.Node) (ServerSource, error) {
	if err := plainNode(node, "server"); err != nil {
		return ServerSource{}, err
	}
	if len(node.Arguments) != 1 {
		return ServerSource{}, fmt.Errorf("wirecmd.server: expected one name")
	}
	name, err := literalText(node.Arguments[0], "wirecmd.server")
	if err != nil {
		return ServerSource{}, err
	}
	path := serverPath(name)
	server := ServerSource{Name: name, Provenance: provenance(file, path)}

	for _, child := range node.Children {
		switch nodeName(child) {
		case "scope":
			if server.Scope != nil {
				return ServerSource{}, fmt.Errorf("%s.scope: duplicate scope", path)
			}
			scope, err := parseScope(child, path)
			if err != nil {
				return ServerSource{}, err
			}
			server.Scope = &scope
			server.ScopeProvenance = provenance(file, path+".scope")
		case "stdio":
			if server.Stdio != nil {
				return ServerSource{}, fmt.Errorf("%s.stdio: duplicate stdio", path)
			}
			if server.HTTP != nil {
				return ServerSource{}, fmt.Errorf("%s: stdio and http are mutually exclusive", path)
			}
			stdio, err := parseStdio(file, child, path)
			if err != nil {
				return ServerSource{}, err
			}
			server.Stdio = &stdio
		case "http":
			if server.HTTP != nil {
				return ServerSource{}, fmt.Errorf("%s.http: duplicate http", path)
			}
			if server.Stdio != nil {
				return ServerSource{}, fmt.Errorf("%s: stdio and http are mutually exclusive", path)
			}
			http, err := parseHTTP(file, child, path)
			if err != nil {
				return ServerSource{}, err
			}
			server.HTTP = &http
		default:
			return ServerSource{}, fmt.Errorf("%s: unknown child node %q", path, nodeName(child))
		}
	}
	return server, nil
}

func parseHTTP(file string, node *document.Node, serverPath string) (HTTPSource, error) {
	path := serverPath + ".http"
	if err := plainNode(node, path); err != nil {
		return HTTPSource{}, err
	}
	if len(node.Arguments) > 1 {
		return HTTPSource{}, fmt.Errorf("%s: expected at most one endpoint", path)
	}
	if len(node.Children) != 0 {
		return HTTPSource{}, fmt.Errorf("%s: child nodes are not supported yet", path)
	}
	source := HTTPSource{Provenance: provenance(file, path)}
	if len(node.Arguments) == 1 {
		endpoint, err := literalText(node.Arguments[0], path)
		if err != nil {
			return HTTPSource{}, err
		}
		source.Endpoint = &endpoint
		source.EndpointProvenance = provenance(file, path)
	}
	return source, nil
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
	return Scope(scope), nil
}

func parseStdio(file string, node *document.Node, serverPath string) (StdioSource, error) {
	path := serverPath + ".stdio"
	if err := plainNode(node, path); err != nil {
		return StdioSource{}, err
	}
	if len(node.Arguments) > 1 {
		return StdioSource{}, fmt.Errorf("%s: expected at most one executable", path)
	}

	stdio := StdioSource{Provenance: provenance(file, path)}
	if len(node.Arguments) == 1 {
		command, err := literalText(node.Arguments[0], path)
		if err != nil {
			return StdioSource{}, err
		}
		stdio.Command = &command
		stdio.CommandProvenance = provenance(file, path)
	}

	seenEnv := make(map[string]struct{}, len(node.Children))
	for _, child := range node.Children {
		switch nodeName(child) {
		case "arg":
			arg, err := parseSingleValue(file, child, fmt.Sprintf("%s.arg[%d]", path, len(stdio.Args)))
			if err != nil {
				return StdioSource{}, err
			}
			stdio.Args = append(stdio.Args, arg)
		case "env":
			env, err := parseEnvironment(file, child, path)
			if err != nil {
				return StdioSource{}, err
			}
			if _, exists := seenEnv[env.Name]; exists {
				return StdioSource{}, fmt.Errorf("%s: duplicate environment name", env.Path)
			}
			seenEnv[env.Name] = struct{}{}
			stdio.Env = append(stdio.Env, env)
		default:
			return StdioSource{}, fmt.Errorf("%s: unknown child node %q", path, nodeName(child))
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
