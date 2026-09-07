// Package config parses and composes the first-slice Wirecmd configuration.
package config

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/bmatcuk/doublestar/v4"
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

// HTTPField is one named HTTP query or header value. Name retains the spelling
// from the source while composition uses the appropriate case-sensitivity for
// the field collection.
type HTTPField struct {
	Name  string
	Value Value
	Provenance
}

// HTTP describes one complete modern Streamable HTTP transport.
type HTTP struct {
	Endpoint           string
	EndpointProvenance Provenance
	Query              []HTTPField
	Headers            []HTTPField
	OAuth              *OAuth
	Provenance
}

// OAuth describes a complete preregistered OAuth client for an HTTP server.
// When OAuth is nil, execution may use dynamic client registration instead.
type OAuth struct {
	ClientID              string
	ClientIDProvenance    Provenance
	ClientSecret          *Value
	RedirectURI           string
	RedirectURIProvenance Provenance
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
	LSPs    []LSPSource
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

// LSP is a named complete local language-server definition. Its name is a
// configuration and instance identity; routine LSP operations select matching
// effective workspace definitions rather than exposing a name as an argument.
type LSP struct {
	Name                       string
	Scope                      Scope
	ScopeProvenance            Provenance
	ImplementationID           string
	ImplementationIDProvenance Provenance
	Selectors                  []LSPSelector
	Stdio                      Stdio
	Provenance
}

// LSPSource is a potentially partial language-server layer.
type LSPSource struct {
	Name                       string
	Scope                      *Scope
	ScopeProvenance            Provenance
	ImplementationID           *string
	ImplementationIDProvenance Provenance
	Selectors                  []LSPSelector
	Stdio                      *StdioSource
	Provenance
}

// LSPSelector routes files matching Pattern to an LSP using LanguageID for
// text-document synchronization. Pattern is workspace-relative and uses
// doublestar path syntax.
type LSPSelector struct {
	LanguageID           string
	LanguageIDProvenance Provenance
	Pattern              string
	PatternProvenance    Provenance
	Provenance           Provenance
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
	Query              []HTTPField
	Headers            []HTTPField
	OAuth              *OAuthSource
	Provenance
}

// OAuthSource is a potentially partial preregistered OAuth client layer.
// ClientSecret is optional even in a complete OAuth configuration.
type OAuthSource struct {
	ClientID              *string
	ClientIDProvenance    Provenance
	ClientSecret          *Value
	RedirectURI           *string
	RedirectURIProvenance Provenance
	Provenance
}

// Config is the complete source-syntax-independent configuration model
// consumed by execution.
type Config struct {
	Root    *Root
	Servers []Server
	LSPs    []LSP
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

// loadDiscoveredProject rejects symlinks and pathname replacement before
// parsing an automatically discovered workspace source.
func loadDiscoveredProject(path string) (*Source, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NONBLOCK|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("load configuration %q: %w", path, err)
	}
	f := os.NewFile(uintptr(fd), path)
	if f == nil {
		_ = syscall.Close(fd)
		return nil, fmt.Errorf("load configuration %q: invalid file descriptor", path)
	}
	defer f.Close()
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("load configuration %q: %w", path, err)
	}
	openInfo, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("load configuration %q: %w", path, err)
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !pathInfo.Mode().IsRegular() || !openInfo.Mode().IsRegular() || !os.SameFile(pathInfo, openInfo) {
		return nil, fmt.Errorf("load configuration %q: discovered workspace config must remain a regular non-symlink file", path)
	}
	source, err := Parse(path, f)
	if err != nil {
		return nil, fmt.Errorf("load configuration %q: %w", path, err)
	}
	return source, nil
}

// LoadEffective loads paths from weakest to strongest, composes their partial
// sources, and validates the resulting complete configuration.
func LoadEffective(paths []string) (*Config, error) {
	return loadEffective(paths, false)
}

// LoadEffectiveDiscovered validates workspace wirecmd.kdl files without
// changing the behavior of trusted global or explicitly supplied files.
func LoadEffectiveDiscovered(paths []string) (*Config, error) {
	return loadEffective(paths, true)
}

func loadEffective(paths []string, discovered bool) (*Config, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("configuration: at least one config file is required")
	}

	sources := make([]*Source, 0, len(paths))
	for _, path := range paths {
		var source *Source
		var err error
		if discovered && filepath.Base(path) == "wirecmd.kdl" {
			source, err = loadDiscoveredProject(path)
		} else {
			source, err = Load(path)
		}
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
	lspIndex := make(map[string]int)

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

		for _, partial := range source.LSPs {
			index, exists := lspIndex[partial.Name]
			if !exists {
				index = len(config.LSPs)
				lspIndex[partial.Name] = index
				config.LSPs = append(config.LSPs, LSP{
					Name:       partial.Name,
					Provenance: partial.Provenance,
				})
			}

			lsp := &config.LSPs[index]
			lsp.Provenance = partial.Provenance
			if partial.Scope != nil {
				lsp.Scope = *partial.Scope
				lsp.ScopeProvenance = partial.ScopeProvenance
			}
			if partial.ImplementationID != nil {
				lsp.ImplementationID = *partial.ImplementationID
				lsp.ImplementationIDProvenance = partial.ImplementationIDProvenance
			}
			if len(partial.Selectors) != 0 {
				lsp.Selectors = append([]LSPSelector(nil), partial.Selectors...)
			}
			if partial.Stdio != nil {
				composeStdio(&lsp.Stdio, partial.Stdio)
			}
		}
	}

	// LSP scope is intentionally workspace-only in this milestone. An omitted
	// scope is therefore a useful shorthand rather than an incomplete value.
	for index := range config.LSPs {
		lsp := &config.LSPs[index]
		if lsp.Scope == "" && lsp.ScopeProvenance == (Provenance{}) {
			lsp.Scope = ScopeWorkspace
			lsp.ScopeProvenance = lsp.Provenance
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
	result.Query = composeHTTPFields(result.Query, partial.Query, false)
	result.Headers = composeHTTPFields(result.Headers, partial.Headers, true)
	if partial.OAuth != nil {
		if result.OAuth == nil {
			result.OAuth = &OAuth{}
		}
		composeOAuth(result.OAuth, partial.OAuth)
	}
}

func composeOAuth(result *OAuth, partial *OAuthSource) {
	result.Provenance = partial.Provenance
	if partial.ClientID != nil {
		result.ClientID = *partial.ClientID
		result.ClientIDProvenance = partial.ClientIDProvenance
	}
	if partial.ClientSecret != nil {
		secret := *partial.ClientSecret
		result.ClientSecret = &secret
	}
	if partial.RedirectURI != nil {
		result.RedirectURI = *partial.RedirectURI
		result.RedirectURIProvenance = partial.RedirectURIProvenance
	}
}

func composeHTTPFields(result, partial []HTTPField, caseInsensitive bool) []HTTPField {
	if len(partial) == 0 {
		return result
	}

	index := make(map[string]int, len(result))
	for i, field := range result {
		index[httpFieldKey(field.Name, caseInsensitive)] = i
	}
	for _, field := range partial {
		key := httpFieldKey(field.Name, caseInsensitive)
		if i, exists := index[key]; exists {
			// Stronger sources replace the field in place so composition remains
			// stable for callers that depend on declaration order.
			result[i] = field
			continue
		}
		index[key] = len(result)
		result = append(result, field)
	}
	return result
}

func httpFieldKey(name string, caseInsensitive bool) string {
	if caseInsensitive {
		return strings.ToLower(name)
	}
	return name
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
	if len(config.Servers) == 0 && len(config.LSPs) == 0 {
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
			if err := validateHTTPHeaders(server.HTTP.Headers); err != nil {
				return validationError(err.Provenance, err.Path, "%s", err.Message)
			}
			if server.HTTP.OAuth != nil {
				if hasHTTPHeader(server.HTTP.Headers, "authorization") {
					return validationError(server.HTTP.OAuth.Provenance, path+".http.oauth", "oauth cannot be combined with an Authorization header")
				}
				if server.HTTP.OAuth.ClientID == "" {
					return validationError(server.HTTP.OAuth.Provenance, path+".http.oauth.client-id", "client-id is required")
				}
				if server.HTTP.OAuth.RedirectURI == "" {
					return validationError(server.HTTP.OAuth.Provenance, path+".http.oauth.redirect-uri", "redirect-uri is required")
				}
				if err := validateOAuthRedirectURI(server.HTTP.OAuth.RedirectURI); err != nil {
					return validationError(server.HTTP.OAuth.RedirectURIProvenance, path+".http.oauth.redirect-uri", "%v", err)
				}
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
	for _, lsp := range config.LSPs {
		path := lspPath(lsp.Name)
		if lsp.Scope != ScopeWorkspace {
			return validationError(lsp.ScopeProvenance, path+".scope", "unsupported scope %q", lsp.Scope)
		}
		if lsp.ImplementationIDProvenance != (Provenance{}) && lsp.ImplementationID == "" {
			return validationError(lsp.ImplementationIDProvenance, path+".implementation-id", "implementation-id must not be empty")
		}
		if len(lsp.Selectors) == 0 {
			return validationError(lsp.Provenance, path+".selector", "at least one selector is required")
		}
		for index, selector := range lsp.Selectors {
			selectorPath := fmt.Sprintf("%s.selector[%d]", path, index)
			if selector.LanguageID == "" {
				return validationError(selector.LanguageIDProvenance, selectorPath+".language-id", "language-id is required")
			}
			if selector.Pattern == "" {
				return validationError(selector.PatternProvenance, selectorPath+".pattern", "pattern must not be empty")
			}
			if !doublestar.ValidatePattern(selector.Pattern) {
				return validationError(selector.PatternProvenance, selectorPath+".pattern", "invalid pattern %q", selector.Pattern)
			}
		}
		if lsp.Stdio.Provenance == (Provenance{}) {
			return validationError(lsp.Provenance, path+".stdio", "stdio is required")
		}
		if lsp.Stdio.Command == "" {
			return validationError(lsp.Stdio.Provenance, path+".stdio", "executable is required")
		}
	}
	return nil
}

func hasHTTPHeader(headers []HTTPField, name string) bool {
	for _, header := range headers {
		if strings.EqualFold(header.Name, name) {
			return true
		}
	}
	return false
}

type httpHeaderValidationError struct {
	Provenance
	Message string
}

func validateHTTPHeaders(headers []HTTPField) *httpHeaderValidationError {
	for _, header := range headers {
		if !validHTTPHeaderName(header.Name) {
			return &httpHeaderValidationError{
				Provenance: header.Provenance,
				Message:    fmt.Sprintf("invalid header name %q", header.Name),
			}
		}
		if reservedHTTPHeaderName(header.Name) {
			return &httpHeaderValidationError{
				Provenance: header.Provenance,
				Message:    fmt.Sprintf("header name %q is reserved", header.Name),
			}
		}
	}
	return nil
}

// validHTTPHeaderName accepts the RFC 7230 token grammar used for HTTP field
// names. Header values are intentionally not checked here: they are resolved
// and validated at the request boundary, after secret lookup.
func validHTTPHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			continue
		}
		switch c {
		case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
			continue
		default:
			return false
		}
	}
	return true
}

func reservedHTTPHeaderName(name string) bool {
	name = strings.ToLower(name)
	switch name {
	case "host", "content-length", "content-type", "accept", "connection", "transfer-encoding", "trailer", "upgrade", "proxy-connection", "mcp-protocol-version", "mcp-session-id", "mcp-method", "mcp-name", "last-event-id":
		return true
	default:
		return strings.HasPrefix(name, "mcp-param-")
	}
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

func validateOAuthRedirectURI(raw string) error {
	parsed, err := url.Parse(raw)
	if err != nil || !parsed.IsAbs() || parsed.Scheme != "http" || parsed.Host == "" {
		return fmt.Errorf("redirect-uri must be an absolute http loopback URL")
	}
	if parsed.User != nil {
		return fmt.Errorf("redirect-uri must not contain URL user information")
	}
	if parsed.Fragment != "" {
		return fmt.Errorf("redirect-uri must not contain a fragment")
	}
	host := strings.Trim(parsed.Hostname(), "[]")
	if host != "127.0.0.1" && host != "::1" && !strings.EqualFold(host, "localhost") {
		return fmt.Errorf("redirect-uri host must be a loopback address")
	}
	port := parsed.Port()
	if port == "" {
		return fmt.Errorf("redirect-uri must include an explicit port")
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 1 || portNumber > 65535 {
		return fmt.Errorf("redirect-uri port must be between 1 and 65535")
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
	seenLSPs := make(map[string]struct{}, len(root.Children))
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
		case "mcp":
			server, err := parseServer(file, child)
			if err != nil {
				return nil, err
			}
			if _, exists := seenServers[server.Name]; exists {
				return nil, fmt.Errorf("%s: duplicate mcp", server.Path)
			}
			seenServers[server.Name] = struct{}{}
			source.Servers = append(source.Servers, server)
		case "lsp":
			lsp, err := parseLSP(file, child)
			if err != nil {
				return nil, err
			}
			if _, exists := seenLSPs[lsp.Name]; exists {
				return nil, fmt.Errorf("%s: duplicate lsp", lsp.Path)
			}
			seenLSPs[lsp.Name] = struct{}{}
			source.LSPs = append(source.LSPs, lsp)
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
	if err := plainNode(node, "mcp"); err != nil {
		return ServerSource{}, err
	}
	if len(node.Arguments) != 1 {
		return ServerSource{}, fmt.Errorf("wirecmd.mcp: expected one name")
	}
	name, err := literalText(node.Arguments[0], "wirecmd.mcp")
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

func parseLSP(file string, node *document.Node) (LSPSource, error) {
	if err := plainNode(node, "lsp"); err != nil {
		return LSPSource{}, err
	}
	if len(node.Arguments) != 1 {
		return LSPSource{}, fmt.Errorf("wirecmd.lsp: expected one name")
	}
	name, err := literalText(node.Arguments[0], "wirecmd.lsp")
	if err != nil {
		return LSPSource{}, err
	}
	path := lspPath(name)
	lsp := LSPSource{Name: name, Provenance: provenance(file, path)}

	for _, child := range node.Children {
		switch nodeName(child) {
		case "scope":
			if lsp.Scope != nil {
				return LSPSource{}, fmt.Errorf("%s.scope: duplicate scope", path)
			}
			scope, err := parseScope(child, path)
			if err != nil {
				return LSPSource{}, err
			}
			lsp.Scope = &scope
			lsp.ScopeProvenance = provenance(file, path+".scope")
		case "implementation-id":
			if lsp.ImplementationID != nil {
				return LSPSource{}, fmt.Errorf("%s.implementation-id: duplicate implementation-id", path)
			}
			implementationID, err := parseLiteral(child, path+".implementation-id")
			if err != nil {
				return LSPSource{}, err
			}
			lsp.ImplementationID = &implementationID
			lsp.ImplementationIDProvenance = provenance(file, path+".implementation-id")
		case "selector":
			selector, err := parseLSPSelector(file, child, path, len(lsp.Selectors))
			if err != nil {
				return LSPSource{}, err
			}
			lsp.Selectors = append(lsp.Selectors, selector)
		case "stdio":
			if lsp.Stdio != nil {
				return LSPSource{}, fmt.Errorf("%s.stdio: duplicate stdio", path)
			}
			stdio, err := parseStdio(file, child, path)
			if err != nil {
				return LSPSource{}, err
			}
			lsp.Stdio = &stdio
		default:
			return LSPSource{}, fmt.Errorf("%s: unknown child node %q", path, nodeName(child))
		}
	}
	return lsp, nil
}

func parseLSPSelector(file string, node *document.Node, lspPath string, index int) (LSPSelector, error) {
	path := fmt.Sprintf("%s.selector[%d]", lspPath, index)
	if node.Type != "" || len(node.Arguments) != 0 || node.Properties.Len() == 0 || len(node.Children) != 0 {
		return LSPSelector{}, fmt.Errorf("%s: expected language-id and optional pattern properties", path)
	}

	selector := LSPSelector{Pattern: "**/*", Provenance: provenance(file, path)}
	seenLanguageID := false
	seenPattern := false
	for name, rawValue := range node.Properties.Unordered() {
		switch name {
		case "language-id":
			if seenLanguageID {
				return LSPSelector{}, fmt.Errorf("%s.language-id: duplicate language-id", path)
			}
			value, err := literalText(rawValue, path+".language-id")
			if err != nil {
				return LSPSelector{}, err
			}
			selector.LanguageID = value
			selector.LanguageIDProvenance = provenance(file, path+".language-id")
			seenLanguageID = true
		case "pattern":
			if seenPattern {
				return LSPSelector{}, fmt.Errorf("%s.pattern: duplicate pattern", path)
			}
			value, err := literalText(rawValue, path+".pattern")
			if err != nil {
				return LSPSelector{}, err
			}
			selector.Pattern = value
			selector.PatternProvenance = provenance(file, path+".pattern")
			seenPattern = true
		default:
			return LSPSelector{}, fmt.Errorf("%s: unknown property %q", path, name)
		}
	}
	if !seenLanguageID {
		return LSPSelector{}, fmt.Errorf("%s.language-id: language-id is required", path)
	}
	if !seenPattern {
		// The catch-all is a deliberate derived value. Point its provenance at
		// the selector node so callers can explain where the effective selector
		// came from without pretending the default was written in KDL.
		selector.PatternProvenance = selector.Provenance
	}
	return selector, nil
}

func parseHTTP(file string, node *document.Node, serverPath string) (HTTPSource, error) {
	path := serverPath + ".http"
	if err := plainNode(node, path); err != nil {
		return HTTPSource{}, err
	}
	if len(node.Arguments) > 1 {
		return HTTPSource{}, fmt.Errorf("%s: expected at most one endpoint", path)
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
	seenQuery := make(map[string]struct{})
	seenHeader := make(map[string]struct{})
	for _, child := range node.Children {
		switch nodeName(child) {
		case "query":
			field, err := parseHTTPField(file, child, path, false)
			if err != nil {
				return HTTPSource{}, err
			}
			if _, exists := seenQuery[field.Name]; exists {
				return HTTPSource{}, fmt.Errorf("%s: duplicate query name", field.Path)
			}
			seenQuery[field.Name] = struct{}{}
			source.Query = append(source.Query, field)
		case "header":
			field, err := parseHTTPField(file, child, path, true)
			if err != nil {
				return HTTPSource{}, err
			}
			key := httpFieldKey(field.Name, true)
			if _, exists := seenHeader[key]; exists {
				return HTTPSource{}, fmt.Errorf("%s: duplicate header name", field.Path)
			}
			seenHeader[key] = struct{}{}
			source.Headers = append(source.Headers, field)
		case "oauth":
			if source.OAuth != nil {
				return HTTPSource{}, fmt.Errorf("%s.oauth: duplicate oauth", path)
			}
			oauth, err := parseOAuth(file, child, path)
			if err != nil {
				return HTTPSource{}, err
			}
			source.OAuth = &oauth
		default:
			return HTTPSource{}, fmt.Errorf("%s: unknown child node %q", path, nodeName(child))
		}
	}
	return source, nil
}

func parseOAuth(file string, node *document.Node, httpPath string) (OAuthSource, error) {
	path := httpPath + ".oauth"
	if err := plainNode(node, path); err != nil {
		return OAuthSource{}, err
	}
	if len(node.Arguments) != 0 {
		return OAuthSource{}, fmt.Errorf("%s: does not accept arguments", path)
	}

	source := OAuthSource{Provenance: provenance(file, path)}
	for _, child := range node.Children {
		switch nodeName(child) {
		case "client-id":
			if source.ClientID != nil {
				return OAuthSource{}, fmt.Errorf("%s.client-id: duplicate client-id", path)
			}
			value, err := parseLiteral(child, path+".client-id")
			if err != nil {
				return OAuthSource{}, err
			}
			source.ClientID = &value
			source.ClientIDProvenance = provenance(file, path+".client-id")
		case "client-secret":
			if source.ClientSecret != nil {
				return OAuthSource{}, fmt.Errorf("%s.client-secret: duplicate client-secret", path)
			}
			value, err := parseSingleValue(file, child, path+".client-secret")
			if err != nil {
				return OAuthSource{}, err
			}
			source.ClientSecret = &value
		case "redirect-uri":
			if source.RedirectURI != nil {
				return OAuthSource{}, fmt.Errorf("%s.redirect-uri: duplicate redirect-uri", path)
			}
			value, err := parseLiteral(child, path+".redirect-uri")
			if err != nil {
				return OAuthSource{}, err
			}
			source.RedirectURI = &value
			source.RedirectURIProvenance = provenance(file, path+".redirect-uri")
		default:
			return OAuthSource{}, fmt.Errorf("%s: unknown child node %q", path, nodeName(child))
		}
	}
	return source, nil
}

func parseLiteral(node *document.Node, path string) (string, error) {
	if err := plainNode(node, path); err != nil {
		return "", err
	}
	if len(node.Arguments) != 1 || len(node.Children) != 0 {
		return "", fmt.Errorf("%s: expected one value", path)
	}
	return literalText(node.Arguments[0], path)
}

func parseHTTPField(file string, node *document.Node, httpPath string, header bool) (HTTPField, error) {
	kind := "query"
	if header {
		kind = "header"
	}
	path := httpPath + "." + kind
	if node.Type != "" || len(node.Arguments) != 0 || node.Properties.Len() != 1 || len(node.Children) != 0 {
		return HTTPField{}, fmt.Errorf("%s: expected one named value", path)
	}
	for name, rawValue := range node.Properties.Unordered() {
		entryPath := fmt.Sprintf("%s[%q]", path, name)
		value, err := parseValue(rawValue, provenance(file, entryPath))
		if err != nil {
			return HTTPField{}, err
		}
		return HTTPField{Name: name, Value: value, Provenance: provenance(file, entryPath)}, nil
	}
	return HTTPField{}, fmt.Errorf("%s: expected one named value", path)
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
	return fmt.Sprintf("wirecmd.mcp[%q]", name)
}

func lspPath(name string) string {
	return fmt.Sprintf("wirecmd.lsp[%q]", name)
}
