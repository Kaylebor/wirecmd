// Package lspclient implements Wirecmd's narrow, server-neutral LSP client.
package lspclient

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

var (
	ErrStart                    = errors.New("LSP process could not be started")
	ErrCapabilityUnavailable    = errors.New("LSP server does not advertise the requested capability")
	ErrEncodingUnsupported      = errors.New("LSP server selected an unsupported position encoding")
	ErrResultUnsupported        = errors.New("LSP server returned an unsupported location result")
	ErrServerRequestUnsupported = errors.New("LSP server made an unsupported client request")
)

// PositionError reports a caller coordinate that cannot address the document.
type PositionError struct{ Message string }

func (e *PositionError) Error() string { return e.Message }

// Command describes a configured LSP child. Wirecmd never infers its contents.
type Command struct {
	Path   string
	Args   []string
	Env    []string
	Dir    string
	Stderr io.Writer
}

// Point and Range are Wirecmd's normalized one-based output coordinates.
type Point struct {
	Line   uint32 `json:"line"`
	Column uint32 `json:"column"`
}

type Range struct {
	Start Point `json:"start"`
	End   Point `json:"end"`
}

type Location struct {
	Path  string `json:"path"`
	Range Range  `json:"range"`
}

// HoverBlock is one normalized piece of hover content. Code blocks retain the
// language supplied by the server; markdown is intentionally kept literal so
// the caller can choose how to render it.
type HoverBlock struct {
	Kind     string `json:"kind"`
	Text     string `json:"text"`
	Language string `json:"language,omitempty"`
}

// Hover is the SDK-independent representation of one non-null hover result.
type Hover struct {
	Range   *Range       `json:"range,omitempty"`
	Content []HoverBlock `json:"content"`
}

// Symbol is a normalized LSP symbol. Children preserve hierarchical document
// symbol responses; flat SymbolInformation responses remain flat.
type Symbol struct {
	Name           string   `json:"name"`
	Kind           uint32   `json:"kind"`
	KindName       string   `json:"kind_name"`
	Path           string   `json:"path"`
	Range          Range    `json:"range"`
	SelectionRange *Range   `json:"selection_range,omitempty"`
	Detail         *string  `json:"detail,omitempty"`
	ContainerName  *string  `json:"container_name,omitempty"`
	Deprecated     bool     `json:"deprecated,omitempty"`
	Children       []Symbol `json:"children,omitempty"`
}

// Capabilities is the SDK-independent subset of the initialize result that
// Wirecmd uses for navigation and document synchronization.
type Capabilities struct {
	Declaration      bool   `json:"declaration"`
	Definition       bool   `json:"definition"`
	TypeDefinition   bool   `json:"type_definition"`
	Implementation   bool   `json:"implementation"`
	References       bool   `json:"references"`
	Hover            bool   `json:"hover"`
	DocumentSymbols  bool   `json:"document_symbols"`
	WorkspaceSymbols bool   `json:"workspace_symbols"`
	PositionEncoding string `json:"position_encoding"`
	TextDocumentSync string `json:"text_document_sync"`
	OpenClose        bool   `json:"open_close"`
}

// Status is the observed, SDK-independent identity and capability state of a
// connected server. Empty identity fields mean the server omitted them.
type Status struct {
	ServerName    string       `json:"server_name,omitempty"`
	ServerVersion string       `json:"server_version,omitempty"`
	Capabilities  Capabilities `json:"capabilities"`
}

type document struct {
	uri        uri.URI
	content    string
	version    int32
	languageID string
}

// DefinitionTarget is a validated snapshot of a definition request. Preparing
// it before process acquisition prevents invalid local input from starting an
// LSP server.
type DefinitionTarget struct {
	path       string
	uri        uri.URI
	content    string
	position   protocol.Position
	languageID string
}

type rejectingClient struct {
	protocol.UnimplementedClient
	mu          sync.Mutex
	unsupported string
}

func (c *rejectingClient) record(method string) {
	c.mu.Lock()
	if c.unsupported == "" {
		c.unsupported = method
	}
	c.mu.Unlock()
}

func (c *rejectingClient) takeUnsupported() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	method := c.unsupported
	c.unsupported = ""
	return method
}

func (c *rejectingClient) Configuration(context.Context, *protocol.ConfigurationParams) ([]protocol.LSPAny, error) {
	c.record(protocol.MethodWorkspaceConfiguration)
	return nil, jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) RegisterCapability(context.Context, *protocol.RegistrationParams) error {
	c.record(protocol.MethodClientRegisterCapability)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) UnregisterCapability(context.Context, *protocol.UnregistrationParams) error {
	c.record(protocol.MethodClientUnregisterCapability)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) ShowMessageRequest(context.Context, *protocol.ShowMessageRequestParams) (*protocol.MessageActionItem, error) {
	c.record(protocol.MethodWindowShowMessageRequest)
	return nil, jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) ShowDocument(context.Context, *protocol.ShowDocumentParams) (*protocol.ShowDocumentResult, error) {
	c.record(protocol.MethodWindowShowDocument)
	return nil, jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) WorkDoneProgressCreate(context.Context, *protocol.WorkDoneProgressCreateParams) error {
	c.record(protocol.MethodWindowWorkDoneProgressCreate)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) WorkspaceFolders(context.Context) ([]protocol.WorkspaceFolder, error) {
	c.record(protocol.MethodWorkspaceWorkspaceFolders)
	return nil, jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) ApplyEdit(context.Context, *protocol.ApplyWorkspaceEditParams) (*protocol.ApplyWorkspaceEditResult, error) {
	c.record(protocol.MethodWorkspaceApplyEdit)
	return nil, jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) CodeLensRefresh(context.Context) error {
	c.record(protocol.MethodWorkspaceCodeLensRefresh)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) FoldingRangeRefresh(context.Context) error {
	c.record(protocol.MethodWorkspaceFoldingRangeRefresh)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) SemanticTokensRefresh(context.Context) error {
	c.record(protocol.MethodWorkspaceSemanticTokensRefresh)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) InlineValueRefresh(context.Context) error {
	c.record(protocol.MethodWorkspaceInlineValueRefresh)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) InlayHintRefresh(context.Context) error {
	c.record(protocol.MethodWorkspaceInlayHintRefresh)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) DiagnosticRefresh(context.Context) error {
	c.record(protocol.MethodWorkspaceDiagnosticRefresh)
	return jsonrpc2.ErrMethodNotFound
}

func (c *rejectingClient) TextDocumentContentRefresh(context.Context, *protocol.TextDocumentContentRefreshParams) error {
	c.record(protocol.MethodWorkspaceTextDocumentContentRefresh)
	return jsonrpc2.ErrMethodNotFound
}

// Session is one initialized LSP process and its disk-document mirror.
type Session struct {
	mu        sync.Mutex
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	stdout    io.ReadCloser
	conn      jsonrpc2.Conn
	server    protocol.Server
	client    *rejectingClient
	syncKind  protocol.TextDocumentSyncKind
	openClose bool
	documents map[string]*document
	closed    bool
	ctx       context.Context
	status    Status
}

type pipe struct {
	io.Reader
	io.Writer
	close func() error
}

func (p *pipe) Close() error { return p.close() }

// Start launches and initializes exactly the configured LSP command.
func Start(ctx context.Context, command Command, workspace, version string) (*Session, error) {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir, cmd.Env, cmd.Stderr = command.Dir, command.Env, command.Stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("%w: open stdin: %v", ErrStart, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, fmt.Errorf("%w: open stdout: %v", ErrStart, err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("%w: %v", ErrStart, err)
	}
	transport := &pipe{Reader: stdout, Writer: stdin, close: func() error {
		a := stdin.Close()
		b := stdout.Close()
		if a != nil {
			return a
		}
		return b
	}}
	client := &rejectingClient{}
	_, conn, server := protocol.NewClient(ctx, client, jsonrpc2.NewStream(transport))
	rootURI := uri.File(workspace)
	pid := int32(os.Getpid())
	falseValue := false
	trueValue := true
	symbolKinds := []protocol.SymbolKind{
		protocol.SymbolKindFile, protocol.SymbolKindModule, protocol.SymbolKindNamespace,
		protocol.SymbolKindPackage, protocol.SymbolKindClass, protocol.SymbolKindMethod,
		protocol.SymbolKindProperty, protocol.SymbolKindField, protocol.SymbolKindConstructor,
		protocol.SymbolKindEnum, protocol.SymbolKindInterface, protocol.SymbolKindFunction,
		protocol.SymbolKindVariable, protocol.SymbolKindConstant, protocol.SymbolKindString,
		protocol.SymbolKindNumber, protocol.SymbolKindBoolean, protocol.SymbolKindArray,
		protocol.SymbolKindObject, protocol.SymbolKindKey, protocol.SymbolKindNull,
		protocol.SymbolKindEnumMember, protocol.SymbolKindStruct, protocol.SymbolKindEvent,
		protocol.SymbolKindOperator, protocol.SymbolKindTypeParameter,
	}
	params := &protocol.InitializeParams{
		ProcessID:  &pid,
		ClientInfo: protocol.ClientInfo{Name: "wirecmd", Version: protocol.NewOptional(version)},
		RootURI:    &rootURI,
		RootPath:   protocol.NewNullable(workspace),
		WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
			WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{{URI: rootURI, Name: filepath.Base(workspace)}}),
		},
		Capabilities: protocol.ClientCapabilities{
			Workspace: &protocol.WorkspaceClientCapabilities{
				WorkspaceFolders: &trueValue,
				ApplyEdit:        &falseValue,
				Configuration:    &falseValue,
				Symbol: &protocol.WorkspaceSymbolClientCapabilities{
					DynamicRegistration: &falseValue,
					SymbolKind:          &protocol.ClientSymbolKindOptions{ValueSet: symbolKinds},
					TagSupport:          protocol.ClientSymbolTagOptions{ValueSet: []protocol.SymbolTag{protocol.SymbolTagDeprecated}},
				},
			},
			TextDocument: &protocol.TextDocumentClientCapabilities{
				Synchronization: &protocol.TextDocumentSyncClientCapabilities{DynamicRegistration: &falseValue, WillSave: &falseValue, WillSaveWaitUntil: &falseValue, DidSave: &falseValue},
				Hover:           &protocol.HoverClientCapabilities{DynamicRegistration: &falseValue, ContentFormat: []protocol.MarkupKind{protocol.MarkupKindMarkdown, protocol.MarkupKindPlainText}},
				Declaration:     &protocol.DeclarationClientCapabilities{DynamicRegistration: &falseValue, LinkSupport: &trueValue},
				Definition:      &protocol.DefinitionClientCapabilities{DynamicRegistration: &falseValue, LinkSupport: &trueValue},
				TypeDefinition:  &protocol.TypeDefinitionClientCapabilities{DynamicRegistration: &falseValue, LinkSupport: &trueValue},
				Implementation:  &protocol.ImplementationClientCapabilities{DynamicRegistration: &falseValue, LinkSupport: &trueValue},
				References:      &protocol.ReferenceClientCapabilities{DynamicRegistration: &falseValue},
				DocumentSymbol: &protocol.DocumentSymbolClientCapabilities{
					DynamicRegistration:               &falseValue,
					SymbolKind:                        &protocol.ClientSymbolKindOptions{ValueSet: symbolKinds},
					HierarchicalDocumentSymbolSupport: &trueValue,
					TagSupport:                        protocol.ClientSymbolTagOptions{ValueSet: []protocol.SymbolTag{protocol.SymbolTagDeprecated}},
				},
			},
			General: &protocol.GeneralClientCapabilities{PositionEncodings: []protocol.PositionEncodingKind{protocol.PositionEncodingKindUTF16}},
		},
	}
	initialized, err := server.Initialize(ctx, params)
	if err != nil {
		abortProcess(conn, cmd)
		return nil, fmt.Errorf("initialize LSP server: %w", err)
	}
	if err := server.Initialized(ctx, &protocol.InitializedParams{}); err != nil {
		abortProcess(conn, cmd)
		return nil, fmt.Errorf("notify LSP initialized: %w", err)
	}
	encoding := initialized.Capabilities.PositionEncoding
	if encoding != "" && encoding != protocol.PositionEncodingKindUTF16 {
		abortProcess(conn, cmd)
		return nil, fmt.Errorf("%w: %s", ErrEncodingUnsupported, encoding)
	}
	if encoding == "" {
		encoding = protocol.PositionEncodingKindUTF16
	}
	syncKind, openClose := synchronization(initialized.Capabilities.TextDocumentSync)
	status := Status{
		ServerName: initialized.ServerInfo.Name,
		Capabilities: Capabilities{
			Declaration:      declarationSupported(initialized.Capabilities.DeclarationProvider),
			Definition:       definitionSupported(initialized.Capabilities.DefinitionProvider),
			TypeDefinition:   typeDefinitionSupported(initialized.Capabilities.TypeDefinitionProvider),
			Implementation:   implementationSupported(initialized.Capabilities.ImplementationProvider),
			References:       referencesSupported(initialized.Capabilities.ReferencesProvider),
			Hover:            hoverSupported(initialized.Capabilities.HoverProvider),
			DocumentSymbols:  documentSymbolsSupported(initialized.Capabilities.DocumentSymbolProvider),
			WorkspaceSymbols: workspaceSymbolsSupported(initialized.Capabilities.WorkspaceSymbolProvider),
			PositionEncoding: string(encoding),
			TextDocumentSync: syncKindName(syncKind),
			OpenClose:        openClose,
		},
	}
	if value, ok := initialized.ServerInfo.Version.Get(); ok {
		status.ServerVersion = value
	}
	return &Session{cmd: cmd, stdin: stdin, stdout: stdout, conn: conn, server: server, client: client, syncKind: syncKind, openClose: openClose, documents: map[string]*document{}, ctx: ctx, status: status}, nil
}

func abortProcess(conn jsonrpc2.Conn, cmd *exec.Cmd) {
	_ = conn.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
}

func declarationSupported(provider protocol.DeclarationProvider) bool {
	switch value := provider.(type) {
	case protocol.Boolean:
		return bool(value)
	case *protocol.DeclarationOptions:
		return value != nil
	case *protocol.DeclarationRegistrationOptions:
		return value != nil
	default:
		return false
	}
}

func definitionSupported(provider protocol.DefinitionProvider) bool {
	switch value := provider.(type) {
	case protocol.Boolean:
		return bool(value)
	case *protocol.DefinitionOptions:
		return value != nil
	default:
		return false
	}
}

func typeDefinitionSupported(provider protocol.TypeDefinitionProvider) bool {
	switch value := provider.(type) {
	case protocol.Boolean:
		return bool(value)
	case *protocol.TypeDefinitionOptions:
		return value != nil
	case *protocol.TypeDefinitionRegistrationOptions:
		return value != nil
	default:
		return false
	}
}

func implementationSupported(provider protocol.ImplementationProvider) bool {
	switch value := provider.(type) {
	case protocol.Boolean:
		return bool(value)
	case *protocol.ImplementationOptions:
		return value != nil
	case *protocol.ImplementationRegistrationOptions:
		return value != nil
	default:
		return false
	}
}

func referencesSupported(provider protocol.ReferencesProvider) bool {
	switch value := provider.(type) {
	case protocol.Boolean:
		return bool(value)
	case *protocol.ReferenceOptions:
		return value != nil
	default:
		return false
	}
}

func hoverSupported(provider protocol.HoverProvider) bool {
	switch value := provider.(type) {
	case protocol.Boolean:
		return bool(value)
	case *protocol.HoverOptions:
		return value != nil
	default:
		return false
	}
}

func documentSymbolsSupported(provider protocol.DocumentSymbolProvider) bool {
	switch value := provider.(type) {
	case protocol.Boolean:
		return bool(value)
	case *protocol.DocumentSymbolOptions:
		return value != nil
	default:
		return false
	}
}

func workspaceSymbolsSupported(provider protocol.WorkspaceSymbolProvider) bool {
	switch value := provider.(type) {
	case protocol.Boolean:
		return bool(value)
	case *protocol.WorkspaceSymbolOptions:
		return value != nil
	default:
		return false
	}
}

func syncKindName(kind protocol.TextDocumentSyncKind) string {
	switch kind {
	case protocol.TextDocumentSyncKindFull:
		return "full"
	case protocol.TextDocumentSyncKindIncremental:
		return "incremental"
	default:
		return "none"
	}
}

func synchronization(sync protocol.TextDocumentSync) (protocol.TextDocumentSyncKind, bool) {
	switch value := sync.(type) {
	case protocol.TextDocumentSyncKind:
		return value, value != protocol.TextDocumentSyncKindNone
	case *protocol.TextDocumentSyncOptions:
		if value == nil {
			return protocol.TextDocumentSyncKindNone, false
		}
		kind := protocol.TextDocumentSyncKindNone
		if value.Change != nil {
			kind = *value.Change
		}
		return kind, value.OpenClose != nil && *value.OpenClose
	default:
		return protocol.TextDocumentSyncKindNone, false
	}
}

// PrepareTarget validates and snapshots a one-based disk position without
// starting or consulting an LSP process.
func PrepareTarget(path string, line, column uint32, languageID string) (*DefinitionTarget, error) {
	target, err := PrepareDocument(path, languageID)
	if err != nil {
		return nil, err
	}
	position, err := positionAt([]byte(target.content), line, column)
	if err != nil {
		return nil, err
	}
	target.position = position
	return target, nil
}

// PrepareDocument validates and snapshots a disk document without requiring a
// position. It is used by document-level LSP requests such as document
// symbols.
func PrepareDocument(path, languageID string) (*DefinitionTarget, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read LSP document: %w", err)
	}
	if !utf8.Valid(content) {
		return nil, &PositionError{Message: "LSP document is not valid UTF-8"}
	}
	return &DefinitionTarget{path: path, uri: uri.File(path), content: string(content), languageID: languageID}, nil
}

// Definition resolves a previously validated disk snapshot.
func (s *Session) Definition(ctx context.Context, target *DefinitionTarget) ([]Location, error) {
	return s.navigation(ctx, target, navigationDefinition, false)
}

// Declaration resolves a declaration for a previously validated disk
// snapshot.
func (s *Session) Declaration(ctx context.Context, target *DefinitionTarget) ([]Location, error) {
	return s.navigation(ctx, target, navigationDeclaration, false)
}

// TypeDefinition resolves a type definition for a previously validated disk
// snapshot.
func (s *Session) TypeDefinition(ctx context.Context, target *DefinitionTarget) ([]Location, error) {
	return s.navigation(ctx, target, navigationTypeDefinition, false)
}

// Implementation resolves an implementation for a previously validated disk
// snapshot.
func (s *Session) Implementation(ctx context.Context, target *DefinitionTarget) ([]Location, error) {
	return s.navigation(ctx, target, navigationImplementation, false)
}

// References resolves references for a previously validated disk snapshot.
func (s *Session) References(ctx context.Context, target *DefinitionTarget, includeDeclaration bool) ([]Location, error) {
	return s.navigation(ctx, target, navigationReferences, includeDeclaration)
}

// Hover returns the normalized hover result for a previously validated disk
// snapshot. A null protocol hover is represented by an empty, non-nil slice.
func (s *Session) Hover(ctx context.Context, target *DefinitionTarget) ([]Hover, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("LSP session is closed")
	}
	if !s.status.Capabilities.Hover {
		return nil, fmt.Errorf("%w: hover", ErrCapabilityUnavailable)
	}
	if err := s.synchronize(ctx, target.path, target.uri, target.content, target.languageID); err != nil {
		return nil, err
	}
	result, err := s.server.Hover(ctx, &protocol.HoverParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: target.uri}, Position: target.position,
	}})
	if err != nil {
		if method := s.client.takeUnsupported(); method != "" {
			return nil, fmt.Errorf("%w: %s", ErrServerRequestUnsupported, method)
		}
		return nil, fmt.Errorf("request LSP hover: %w", err)
	}
	s.client.takeUnsupported()
	return normalizeHover(result)
}

// DocumentSymbols returns normalized symbols for a previously validated disk
// snapshot. It preserves hierarchical and flat protocol response forms.
func (s *Session) DocumentSymbols(ctx context.Context, target *DefinitionTarget) ([]Symbol, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("LSP session is closed")
	}
	if !s.status.Capabilities.DocumentSymbols {
		return nil, fmt.Errorf("%w: document symbols", ErrCapabilityUnavailable)
	}
	if err := s.synchronize(ctx, target.path, target.uri, target.content, target.languageID); err != nil {
		return nil, err
	}
	result, err := s.server.DocumentSymbol(ctx, &protocol.DocumentSymbolParams{
		TextDocument: protocol.TextDocumentIdentifier{URI: target.uri},
	})
	if err != nil {
		if method := s.client.takeUnsupported(); method != "" {
			return nil, fmt.Errorf("%w: %s", ErrServerRequestUnsupported, method)
		}
		return nil, fmt.Errorf("request LSP document symbols: %w", err)
	}
	s.client.takeUnsupported()
	symbols, err := normalizeDocumentSymbols(result)
	if err != nil {
		return nil, err
	}
	setSymbolPaths(symbols, target.path)
	return symbols, nil
}

// WorkspaceSymbols searches the server's current workspace index. It does not
// open or synchronize a document and passes query through unchanged.
func (s *Session) WorkspaceSymbols(ctx context.Context, query string) ([]Symbol, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("LSP session is closed")
	}
	if !s.status.Capabilities.WorkspaceSymbols {
		return nil, fmt.Errorf("%w: workspace symbols", ErrCapabilityUnavailable)
	}
	result, err := s.server.Symbols(ctx, &protocol.WorkspaceSymbolParams{Query: query})
	if err != nil {
		if method := s.client.takeUnsupported(); method != "" {
			return nil, fmt.Errorf("%w: %s", ErrServerRequestUnsupported, method)
		}
		return nil, fmt.Errorf("request LSP workspace symbols: %w", err)
	}
	s.client.takeUnsupported()
	return normalizeWorkspaceSymbols(result)
}

type navigationKind uint8

const (
	navigationDeclaration navigationKind = iota
	navigationDefinition
	navigationTypeDefinition
	navigationImplementation
	navigationReferences
)

func (s *Session) navigation(ctx context.Context, target *DefinitionTarget, kind navigationKind, includeDeclaration bool) ([]Location, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("LSP session is closed")
	}
	if !s.supports(kind) {
		return nil, fmt.Errorf("%w: %s", ErrCapabilityUnavailable, operationName(kind))
	}
	if err := s.synchronize(ctx, target.path, target.uri, target.content, target.languageID); err != nil {
		return nil, err
	}
	params := protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: target.uri}, Position: target.position}
	var result any
	var err error
	switch kind {
	case navigationDeclaration:
		result, err = s.server.Declaration(ctx, &protocol.DeclarationParams{TextDocumentPositionParams: params})
	case navigationDefinition:
		result, err = s.server.Definition(ctx, &protocol.DefinitionParams{TextDocumentPositionParams: params})
	case navigationTypeDefinition:
		result, err = s.server.TypeDefinition(ctx, &protocol.TypeDefinitionParams{TextDocumentPositionParams: params})
	case navigationImplementation:
		result, err = s.server.Implementation(ctx, &protocol.ImplementationParams{TextDocumentPositionParams: params})
	case navigationReferences:
		result, err = s.server.References(ctx, &protocol.ReferenceParams{TextDocumentPositionParams: params, Context: protocol.ReferenceContext{IncludeDeclaration: includeDeclaration}})
	}
	if err != nil {
		if method := s.client.takeUnsupported(); method != "" {
			return nil, fmt.Errorf("%w: %s", ErrServerRequestUnsupported, method)
		}
		return nil, fmt.Errorf("request LSP %s: %w", operationName(kind), err)
	}
	s.client.takeUnsupported()
	return normalizeLocations(result)
}

func operationName(kind navigationKind) string {
	switch kind {
	case navigationDeclaration:
		return "declaration"
	case navigationDefinition:
		return "definition"
	case navigationTypeDefinition:
		return "type definition"
	case navigationImplementation:
		return "implementation"
	case navigationReferences:
		return "references"
	default:
		return "navigation"
	}
}

func (s *Session) supports(kind navigationKind) bool {
	switch kind {
	case navigationDeclaration:
		return s.status.Capabilities.Declaration
	case navigationDefinition:
		return s.status.Capabilities.Definition
	case navigationTypeDefinition:
		return s.status.Capabilities.TypeDefinition
	case navigationImplementation:
		return s.status.Capabilities.Implementation
	case navigationReferences:
		return s.status.Capabilities.References
	default:
		return false
	}
}

// Status returns the negotiated server identity and capabilities. It does not
// contact the server.
func (s *Session) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Capabilities returns the negotiated SDK-independent capability summary.
func (s *Session) Capabilities() Capabilities {
	return s.Status().Capabilities
}

func (s *Session) synchronize(ctx context.Context, path string, docURI uri.URI, content, languageID string) error {
	previous := s.documents[path]
	if previous == nil {
		doc := &document{uri: docURI, content: content, version: 1, languageID: languageID}
		s.documents[path] = doc
		if s.openClose {
			return s.server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: docURI, LanguageID: protocol.LanguageKind(languageID), Version: doc.version, Text: content}})
		}
		return nil
	}
	if previous.languageID != languageID && s.openClose {
		if err := s.server.DidClose(ctx, &protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: docURI}}); err != nil {
			return fmt.Errorf("close changed LSP document: %w", err)
		}
		previous.content = content
		previous.version = 1
		previous.languageID = languageID
		return s.server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: docURI, LanguageID: protocol.LanguageKind(languageID), Version: previous.version, Text: content}})
	}
	if previous.content == content || s.syncKind == protocol.TextDocumentSyncKindNone {
		previous.content = content
		previous.languageID = languageID
		return nil
	}
	previous.version++
	var change protocol.TextDocumentContentChangeEvent
	if s.syncKind == protocol.TextDocumentSyncKindIncremental {
		change = &protocol.TextDocumentContentChangePartial{Range: wholeDocumentRange(previous.content), Text: content}
	} else {
		change = &protocol.TextDocumentContentChangeWholeDocument{Text: content}
	}
	if err := s.server.DidChange(ctx, &protocol.DidChangeTextDocumentParams{TextDocument: protocol.VersionedTextDocumentIdentifier{TextDocumentIdentifier: protocol.TextDocumentIdentifier{URI: docURI}, Version: previous.version}, ContentChanges: []protocol.TextDocumentContentChangeEvent{change}}); err != nil {
		return fmt.Errorf("synchronize LSP document: %w", err)
	}
	previous.content = content
	previous.languageID = languageID
	return nil
}

func positionAt(content []byte, line, column uint32) (protocol.Position, error) {
	if line == 0 || column == 0 {
		return protocol.Position{}, &PositionError{Message: "line and column must be at least 1"}
	}
	lines := bytes.Split(content, []byte("\n"))
	if int(line) > len(lines) {
		return protocol.Position{}, &PositionError{Message: fmt.Sprintf("line %d is outside the document", line)}
	}
	lineText := bytes.TrimSuffix(lines[line-1], []byte("\r"))
	runes := []rune(string(lineText))
	if int(column-1) > len(runes) {
		return protocol.Position{}, &PositionError{Message: fmt.Sprintf("column %d is outside line %d", column, line)}
	}
	units := 0
	for _, r := range runes[:column-1] {
		if r <= 0xffff {
			units++
		} else {
			units += len(utf16.Encode([]rune{r}))
		}
	}
	return protocol.Position{Line: line - 1, Character: uint32(units)}, nil
}

func wholeDocumentRange(content string) protocol.Range {
	lines := bytes.Split([]byte(content), []byte("\n"))
	last := bytes.TrimSuffix(lines[len(lines)-1], []byte("\r"))
	units := 0
	for len(last) > 0 {
		r, n := utf8.DecodeRune(last)
		last = last[n:]
		if r <= 0xffff {
			units++
		} else {
			units += 2
		}
	}
	return protocol.Range{Start: protocol.Position{}, End: protocol.Position{Line: uint32(len(lines) - 1), Character: uint32(units)}}
}

func normalizeLocations(result any) ([]Location, error) {
	locations := []Location{}
	appendLocation := func(rawURI uri.URI, rawRange protocol.Range) error {
		if rawURI.Scheme() != "file" {
			return fmt.Errorf("%w: URI scheme %q", ErrResultUnsupported, rawURI.Scheme())
		}
		locations = append(locations, Location{Path: rawURI.FsPath(), Range: normalizeRange(rawRange)})
		return nil
	}
	switch value := result.(type) {
	case nil:
	case *protocol.Location:
		if value != nil {
			if err := appendLocation(value.URI, value.Range); err != nil {
				return nil, err
			}
		}
	case protocol.LocationSlice:
		for _, location := range value {
			if err := appendLocation(location.URI, location.Range); err != nil {
				return nil, err
			}
		}
	case []protocol.Location:
		for _, location := range value {
			if err := appendLocation(location.URI, location.Range); err != nil {
				return nil, err
			}
		}
	case protocol.DefinitionLinkSlice:
		for _, link := range value {
			if err := appendLocation(link.TargetURI, link.TargetSelectionRange); err != nil {
				return nil, err
			}
		}
	case protocol.DeclarationLinkSlice:
		for _, link := range value {
			if err := appendLocation(link.TargetURI, link.TargetSelectionRange); err != nil {
				return nil, err
			}
		}
	default:
		return nil, fmt.Errorf("%w: %T", ErrResultUnsupported, result)
	}
	return locations, nil
}

func normalizeHover(result *protocol.Hover) ([]Hover, error) {
	hovers := []Hover{}
	if result == nil {
		return hovers, nil
	}
	content, err := normalizeHoverContents(result.Contents)
	if err != nil {
		return nil, err
	}
	value := Hover{Content: content}
	if result.Range != nil {
		rangeValue := normalizeRange(*result.Range)
		value.Range = &rangeValue
	}
	hovers = append(hovers, value)
	return hovers, nil
}

func normalizeHoverContents(contents protocol.HoverContents) ([]HoverBlock, error) {
	blocks := []HoverBlock{}
	appendMarkup := func(kind, text string) {
		blocks = append(blocks, HoverBlock{Kind: kind, Text: text})
	}
	switch value := contents.(type) {
	case nil:
		return blocks, nil
	case *protocol.MarkupContent:
		if value == nil {
			return blocks, nil
		}
		switch value.Kind {
		case protocol.MarkupKindPlainText:
			appendMarkup("plaintext", value.Value)
		case protocol.MarkupKindMarkdown:
			appendMarkup("markdown", value.Value)
		default:
			return nil, fmt.Errorf("%w: hover markup kind %q", ErrResultUnsupported, value.Kind)
		}
	case protocol.String:
		// The deprecated MarkedString string form is Markdown by definition;
		// only MarkupContent with kind plaintext is plain text.
		appendMarkup("markdown", string(value))
	case *protocol.MarkedStringWithLanguage:
		if value == nil {
			return blocks, nil
		}
		blocks = append(blocks, HoverBlock{Kind: "code", Text: value.Value, Language: value.Language})
	case protocol.MarkedStringSlice:
		for _, item := range value {
			switch itemValue := item.(type) {
			case protocol.String:
				appendMarkup("markdown", string(itemValue))
			case *protocol.MarkedStringWithLanguage:
				if itemValue == nil {
					return nil, fmt.Errorf("%w: nil hover marked string", ErrResultUnsupported)
				}
				blocks = append(blocks, HoverBlock{Kind: "code", Text: itemValue.Value, Language: itemValue.Language})
			default:
				return nil, fmt.Errorf("%w: hover marked string %T", ErrResultUnsupported, item)
			}
		}
	default:
		return nil, fmt.Errorf("%w: hover contents %T", ErrResultUnsupported, contents)
	}
	return blocks, nil
}

func normalizeDocumentSymbols(result protocol.DocumentSymbolResult) ([]Symbol, error) {
	symbols := []Symbol{}
	switch value := result.(type) {
	case nil:
		return symbols, nil
	case protocol.DocumentSymbolSlice:
		for _, symbol := range value {
			normalized, err := normalizeDocumentSymbol(symbol)
			if err != nil {
				return nil, err
			}
			symbols = append(symbols, normalized)
		}
	case protocol.SymbolInformationSlice:
		for _, symbol := range value {
			normalized, err := normalizeSymbolInformation(symbol)
			if err != nil {
				return nil, err
			}
			symbols = append(symbols, normalized)
		}
	default:
		return nil, fmt.Errorf("%w: document symbols %T", ErrResultUnsupported, result)
	}
	return symbols, nil
}

func normalizeWorkspaceSymbols(result protocol.WorkspaceSymbolResult) ([]Symbol, error) {
	symbols := []Symbol{}
	switch value := result.(type) {
	case nil:
		return symbols, nil
	case protocol.SymbolInformationSlice:
		for _, symbol := range value {
			normalized, err := normalizeSymbolInformation(symbol)
			if err != nil {
				return nil, err
			}
			symbols = append(symbols, normalized)
		}
	case protocol.WorkspaceSymbolSlice:
		for _, symbol := range value {
			normalized, err := normalizeWorkspaceSymbol(symbol)
			if err != nil {
				return nil, err
			}
			symbols = append(symbols, normalized)
		}
	default:
		return nil, fmt.Errorf("%w: workspace symbols %T", ErrResultUnsupported, result)
	}
	return symbols, nil
}

func normalizeDocumentSymbol(value protocol.DocumentSymbol) (Symbol, error) {
	result := Symbol{
		Name:       value.Name,
		Kind:       uint32(value.Kind),
		KindName:   symbolKindName(value.Kind),
		Range:      normalizeRange(value.Range),
		Deprecated: symbolDeprecated(value.Tags, value.Deprecated),
	}
	result.SelectionRange = normalizedRangePointer(value.SelectionRange)
	result.Detail = value.Detail
	for _, child := range value.Children {
		normalized, err := normalizeDocumentSymbol(child)
		if err != nil {
			return Symbol{}, err
		}
		result.Children = append(result.Children, normalized)
	}
	return result, nil
}

func setSymbolPaths(symbols []Symbol, path string) {
	for index := range symbols {
		if symbols[index].Path == "" {
			symbols[index].Path = path
		}
		setSymbolPaths(symbols[index].Children, path)
	}
}

func normalizeSymbolInformation(value protocol.SymbolInformation) (Symbol, error) {
	path, normalizedRange, err := normalizeLocation(value.Location)
	if err != nil {
		return Symbol{}, err
	}
	return Symbol{
		Name:          value.Name,
		Kind:          uint32(value.Kind),
		KindName:      symbolKindName(value.Kind),
		Path:          path,
		Range:         normalizedRange,
		ContainerName: value.ContainerName,
		Deprecated:    symbolDeprecated(value.Tags, value.Deprecated),
	}, nil
}

func normalizeWorkspaceSymbol(value protocol.WorkspaceSymbol) (Symbol, error) {
	var location protocol.Location
	switch raw := value.Location.(type) {
	case *protocol.Location:
		if raw == nil {
			return Symbol{}, fmt.Errorf("%w: workspace symbol has no location", ErrResultUnsupported)
		}
		location = *raw
	case *protocol.LocationUriOnly:
		return Symbol{}, fmt.Errorf("%w: workspace symbol location has no range", ErrResultUnsupported)
	case nil:
		return Symbol{}, fmt.Errorf("%w: workspace symbol has no location", ErrResultUnsupported)
	default:
		return Symbol{}, fmt.Errorf("%w: workspace symbol location %T", ErrResultUnsupported, value.Location)
	}
	path, normalizedRange, err := normalizeLocation(location)
	if err != nil {
		return Symbol{}, err
	}
	return Symbol{
		Name:          value.Name,
		Kind:          uint32(value.Kind),
		KindName:      symbolKindName(value.Kind),
		Path:          path,
		Range:         normalizedRange,
		ContainerName: value.ContainerName,
		Deprecated:    symbolDeprecated(value.Tags, nil),
	}, nil
}

func normalizeLocation(value protocol.Location) (string, Range, error) {
	if value.URI.Scheme() != "file" {
		return "", Range{}, fmt.Errorf("%w: URI scheme %q", ErrResultUnsupported, value.URI.Scheme())
	}
	return value.URI.FsPath(), normalizeRange(value.Range), nil
}

func normalizedRangePointer(value protocol.Range) *Range {
	normalized := normalizeRange(value)
	return &normalized
}

func symbolDeprecated(tags []protocol.SymbolTag, value *bool) bool {
	if value != nil && *value {
		return true
	}
	for _, tag := range tags {
		if tag == protocol.SymbolTagDeprecated {
			return true
		}
	}
	return false
}

func symbolKindName(kind protocol.SymbolKind) string {
	switch kind {
	case protocol.SymbolKindFile:
		return "file"
	case protocol.SymbolKindModule:
		return "module"
	case protocol.SymbolKindNamespace:
		return "namespace"
	case protocol.SymbolKindPackage:
		return "package"
	case protocol.SymbolKindClass:
		return "class"
	case protocol.SymbolKindMethod:
		return "method"
	case protocol.SymbolKindProperty:
		return "property"
	case protocol.SymbolKindField:
		return "field"
	case protocol.SymbolKindConstructor:
		return "constructor"
	case protocol.SymbolKindEnum:
		return "enum"
	case protocol.SymbolKindInterface:
		return "interface"
	case protocol.SymbolKindFunction:
		return "function"
	case protocol.SymbolKindVariable:
		return "variable"
	case protocol.SymbolKindConstant:
		return "constant"
	case protocol.SymbolKindString:
		return "string"
	case protocol.SymbolKindNumber:
		return "number"
	case protocol.SymbolKindBoolean:
		return "boolean"
	case protocol.SymbolKindArray:
		return "array"
	case protocol.SymbolKindObject:
		return "object"
	case protocol.SymbolKindKey:
		return "key"
	case protocol.SymbolKindNull:
		return "null"
	case protocol.SymbolKindEnumMember:
		return "enum_member"
	case protocol.SymbolKindStruct:
		return "struct"
	case protocol.SymbolKindEvent:
		return "event"
	case protocol.SymbolKindOperator:
		return "operator"
	case protocol.SymbolKindTypeParameter:
		return "type_parameter"
	default:
		return "unknown"
	}
}

func normalizeRange(value protocol.Range) Range {
	return Range{Start: Point{Line: value.Start.Line + 1, Column: value.Start.Character + 1}, End: Point{Line: value.End.Line + 1, Column: value.End.Character + 1}}
}

// Close performs the LSP shutdown sequence and reaps the child.
func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 2*time.Second)
	defer cancel()
	for _, doc := range s.documents {
		if s.openClose {
			_ = s.server.DidClose(closeCtx, &protocol.DidCloseTextDocumentParams{TextDocument: protocol.TextDocumentIdentifier{URI: doc.uri}})
		}
	}
	_ = s.server.Shutdown(closeCtx)
	_ = s.server.Exit(closeCtx)
	err := s.conn.Close()
	waited := make(chan struct{})
	go func() {
		_ = s.cmd.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(2 * time.Second):
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		<-waited
	}
	if err != nil {
		return err
	}
	return nil
}

// Broken reports whether the protocol connection terminated unexpectedly.
func (s *Session) Broken() error {
	select {
	case <-s.conn.Done():
		if err := s.conn.Err(); err != nil {
			return err
		}
		return errors.New("LSP connection closed")
	default:
		return nil
	}
}
