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
	ErrCapabilityUnavailable    = errors.New("LSP server does not advertise definition support")
	ErrEncodingUnsupported      = errors.New("LSP server selected an unsupported position encoding")
	ErrResultUnsupported        = errors.New("LSP server returned an unsupported definition result")
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

type document struct {
	uri     uri.URI
	content string
	version int32
}

// DefinitionTarget is a validated snapshot of a definition request. Preparing
// it before process acquisition prevents invalid local input from starting an
// LSP server.
type DefinitionTarget struct {
	path     string
	uri      uri.URI
	content  string
	position protocol.Position
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
	mu         sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	conn       jsonrpc2.Conn
	server     protocol.Server
	client     *rejectingClient
	languageID string
	syncKind   protocol.TextDocumentSyncKind
	openClose  bool
	documents  map[string]*document
	closed     bool
	ctx        context.Context
}

type pipe struct {
	io.Reader
	io.Writer
	close func() error
}

func (p *pipe) Close() error { return p.close() }

// Start launches and initializes exactly the configured LSP command.
func Start(ctx context.Context, command Command, languageID, workspace, version string) (*Session, error) {
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
	params := &protocol.InitializeParams{
		ProcessID:  &pid,
		ClientInfo: protocol.ClientInfo{Name: "wirecmd", Version: protocol.NewOptional(version)},
		RootURI:    &rootURI,
		RootPath:   protocol.NewNullable(workspace),
		WorkspaceFoldersInitializeParams: protocol.WorkspaceFoldersInitializeParams{
			WorkspaceFolders: protocol.NewNullable([]protocol.WorkspaceFolder{{URI: rootURI, Name: filepath.Base(workspace)}}),
		},
		Capabilities: protocol.ClientCapabilities{
			Workspace: &protocol.WorkspaceClientCapabilities{WorkspaceFolders: &trueValue, ApplyEdit: &falseValue, Configuration: &falseValue},
			TextDocument: &protocol.TextDocumentClientCapabilities{
				Synchronization: &protocol.TextDocumentSyncClientCapabilities{DynamicRegistration: &falseValue, WillSave: &falseValue, WillSaveWaitUntil: &falseValue, DidSave: &falseValue},
				Definition:      &protocol.DefinitionClientCapabilities{DynamicRegistration: &falseValue, LinkSupport: &trueValue},
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
	if !definitionSupported(initialized.Capabilities.DefinitionProvider) {
		_ = server.Shutdown(ctx)
		_ = server.Exit(ctx)
		abortProcess(conn, cmd)
		return nil, ErrCapabilityUnavailable
	}
	syncKind, openClose := synchronization(initialized.Capabilities.TextDocumentSync)
	return &Session{cmd: cmd, stdin: stdin, stdout: stdout, conn: conn, server: server, client: client, languageID: languageID, syncKind: syncKind, openClose: openClose, documents: map[string]*document{}, ctx: ctx}, nil
}

func abortProcess(conn jsonrpc2.Conn, cmd *exec.Cmd) {
	_ = conn.Close()
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	_ = cmd.Wait()
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

// PrepareDefinition validates and snapshots a one-based disk position without
// starting or consulting an LSP process.
func PrepareDefinition(path string, line, column uint32) (*DefinitionTarget, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read LSP document: %w", err)
	}
	if !utf8.Valid(content) {
		return nil, &PositionError{Message: "LSP document is not valid UTF-8"}
	}
	position, err := positionAt(content, line, column)
	if err != nil {
		return nil, err
	}
	return &DefinitionTarget{path: path, uri: uri.File(path), content: string(content), position: position}, nil
}

// Definition resolves a previously validated disk snapshot.
func (s *Session) Definition(ctx context.Context, target *DefinitionTarget) ([]Location, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("LSP session is closed")
	}
	if err := s.synchronize(ctx, target.path, target.uri, target.content); err != nil {
		return nil, err
	}
	result, err := s.server.Definition(ctx, &protocol.DefinitionParams{TextDocumentPositionParams: protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: target.uri}, Position: target.position}})
	if err != nil {
		if method := s.client.takeUnsupported(); method != "" {
			return nil, fmt.Errorf("%w: %s", ErrServerRequestUnsupported, method)
		}
		return nil, fmt.Errorf("request LSP definition: %w", err)
	}
	s.client.takeUnsupported()
	return normalizeLocations(result)
}

func (s *Session) synchronize(ctx context.Context, path string, docURI uri.URI, content string) error {
	previous := s.documents[path]
	if previous == nil {
		doc := &document{uri: docURI, content: content, version: 1}
		s.documents[path] = doc
		if s.openClose {
			return s.server.DidOpen(ctx, &protocol.DidOpenTextDocumentParams{TextDocument: protocol.TextDocumentItem{URI: docURI, LanguageID: protocol.LanguageKind(s.languageID), Version: doc.version, Text: content}})
		}
		return nil
	}
	if previous.content == content || s.syncKind == protocol.TextDocumentSyncKindNone {
		previous.content = content
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

func normalizeLocations(result protocol.DefinitionResult) ([]Location, error) {
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
	case protocol.DefinitionLinkSlice:
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
