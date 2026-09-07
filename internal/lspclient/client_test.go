package lspclient

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"go.lsp.dev/jsonrpc2"
	"go.lsp.dev/protocol"
	"go.lsp.dev/uri"
)

func TestPositionAtUsesOneBasedRuneColumnsAndUTF16(t *testing.T) {
	position, err := positionAt([]byte("a😀b\nsecond"), 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if position.Line != 0 || position.Character != 3 {
		t.Fatalf("position = %+v, want line 0 character 3", position)
	}
	for _, test := range []struct{ line, column uint32 }{{0, 1}, {3, 1}, {1, 5}} {
		if _, err := positionAt([]byte("abc\n"), test.line, test.column); err == nil {
			t.Fatalf("positionAt(%d, %d) succeeded", test.line, test.column)
		}
	}
}

func TestNormalizeLocations(t *testing.T) {
	file := uri.File("/workspace/target.go")
	rangeValue := protocol.Range{Start: protocol.Position{Line: 2, Character: 4}, End: protocol.Position{Line: 2, Character: 7}}
	for name, result := range map[string]protocol.DefinitionResult{
		"single":    &protocol.Location{URI: file, Range: rangeValue},
		"locations": protocol.LocationSlice{{URI: file, Range: rangeValue}},
		"links":     protocol.DefinitionLinkSlice{{TargetURI: file, TargetRange: protocol.Range{}, TargetSelectionRange: rangeValue}},
		"null":      nil,
	} {
		t.Run(name, func(t *testing.T) {
			locations, err := normalizeLocations(result)
			if err != nil {
				t.Fatal(err)
			}
			if name == "null" {
				if locations == nil || len(locations) != 0 {
					t.Fatalf("locations = %#v, want non-nil empty", locations)
				}
				return
			}
			if len(locations) != 1 || locations[0].Path != "/workspace/target.go" || locations[0].Range.Start != (Point{Line: 3, Column: 5}) {
				t.Fatalf("locations = %#v", locations)
			}
		})
	}
	httpURI := uri.MustParse("https://example.test/definition")
	if _, err := normalizeLocations(&protocol.Location{URI: httpURI}); !errorsIs(err, ErrResultUnsupported) {
		t.Fatalf("non-file error = %v", err)
	}
}

func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		type unwrapper interface{ Unwrap() error }
		value, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = value.Unwrap()
	}
	return false
}

func TestSessionLifecycleAndDiskRefresh(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(path, []byte("a😀b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := helperCommand("incremental")
	session, err := Start(context.Background(), command, workspace, "test")
	if err != nil {
		t.Fatal(err)
	}
	target, err := PrepareTarget(path, 1, 3, "go")
	if err != nil {
		t.Fatal(err)
	}
	locations, err := session.Definition(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if len(locations) != 1 || locations[0].Path != path+".definition" {
		t.Fatalf("locations = %#v", locations)
	}
	if err := os.WriteFile(path, []byte("changed\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	target, err = PrepareTarget(path, 1, 2, "go")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Definition(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	if err := session.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}
}

func TestStartRejectsCapabilitiesAndEncoding(t *testing.T) {
	t.Run("no-definition", func(t *testing.T) {
		workspace := t.TempDir()
		path := filepath.Join(workspace, "input.go")
		if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		session, err := Start(context.Background(), helperCommand("no-definition"), workspace, "test")
		if err != nil {
			t.Fatal(err)
		}
		defer session.Close()
		target, err := PrepareTarget(path, 1, 1, "plain")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.Definition(context.Background(), target); !errorsIs(err, ErrCapabilityUnavailable) {
			t.Fatalf("Definition() error = %v, want %v", err, ErrCapabilityUnavailable)
		}
	})
	t.Run("utf8", func(t *testing.T) {
		workspace := t.TempDir()
		_, err := Start(context.Background(), helperCommand("utf8"), workspace, "test")
		if !errorsIs(err, ErrEncodingUnsupported) {
			t.Fatalf("Start() error = %v, want %v", err, ErrEncodingUnsupported)
		}
	})
}

func TestUnsupportedServerRequestIsReportedWhenDefinitionFails(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(path, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := Start(context.Background(), helperCommand("unsupported-request"), workspace, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	target, err := PrepareTarget(path, 1, 1, "plain")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Definition(context.Background(), target); !errorsIs(err, ErrServerRequestUnsupported) {
		t.Fatalf("Definition() error = %v, want %v", err, ErrServerRequestUnsupported)
	}
}

func TestNavigationOperationsAndStatus(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(path, []byte("symbol\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := Start(context.Background(), helperCommand("all"), workspace, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	status := session.Status()
	if status.ServerName != "fake" || status.ServerVersion != "1.0" {
		t.Fatalf("status identity = %+v", status)
	}
	if status.Capabilities.PositionEncoding != "utf-16" || status.Capabilities.TextDocumentSync != "incremental" || !status.Capabilities.OpenClose {
		t.Fatalf("status synchronization = %+v", status.Capabilities)
	}
	if !status.Capabilities.Declaration || !status.Capabilities.Definition || !status.Capabilities.TypeDefinition || !status.Capabilities.Implementation || !status.Capabilities.References {
		t.Fatalf("status capabilities = %+v", status.Capabilities)
	}
	target, err := PrepareTarget(path, 1, 1, "go")
	if err != nil {
		t.Fatal(err)
	}
	for name, call := range map[string]func() ([]Location, error){
		"declaration":     func() ([]Location, error) { return session.Declaration(context.Background(), target) },
		"definition":      func() ([]Location, error) { return session.Definition(context.Background(), target) },
		"type-definition": func() ([]Location, error) { return session.TypeDefinition(context.Background(), target) },
		"implementation":  func() ([]Location, error) { return session.Implementation(context.Background(), target) },
		"references":      func() ([]Location, error) { return session.References(context.Background(), target, true) },
	} {
		t.Run(name, func(t *testing.T) {
			locations, err := call()
			if err != nil {
				t.Fatal(err)
			}
			if len(locations) != 1 || locations[0].Path != path+"."+name {
				t.Fatalf("locations = %#v", locations)
			}
		})
	}
}

func TestDocumentLanguageIDIsSelectedAtSynchronizationTime(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "input.txt")
	if err := os.WriteFile(path, []byte("symbol\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := Start(context.Background(), helperCommand("language"), workspace, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, test := range []struct {
		language string
		wantPath string
	}{{"go", path + ".go"}, {"rust", path + ".rust"}} {
		target, err := PrepareTarget(path, 1, 1, test.language)
		if err != nil {
			t.Fatal(err)
		}
		locations, err := session.Definition(context.Background(), target)
		if err != nil {
			t.Fatal(err)
		}
		if len(locations) != 1 || locations[0].Path != test.wantPath {
			t.Fatalf("language %q locations = %#v", test.language, locations)
		}
	}
}

func TestNormalizeNavigationResultVariants(t *testing.T) {
	file := uri.File("/workspace/target.go")
	rangeValue := protocol.Range{Start: protocol.Position{Line: 2, Character: 4}, End: protocol.Position{Line: 2, Character: 7}}
	declarationLink := protocol.DeclarationLink{TargetURI: file, TargetSelectionRange: rangeValue}
	for name, result := range map[string]any{
		"declaration-links": protocol.DeclarationLinkSlice{declarationLink},
		"references":        []protocol.Location{{URI: file, Range: rangeValue}},
	} {
		t.Run(name, func(t *testing.T) {
			locations, err := normalizeLocations(result)
			if err != nil {
				t.Fatal(err)
			}
			if len(locations) != 1 || locations[0].Path != "/workspace/target.go" || locations[0].Range.Start != (Point{Line: 3, Column: 5}) {
				t.Fatalf("locations = %#v", locations)
			}
		})
	}
}

func helperCommand(mode string) Command {
	return Command{Path: os.Args[0], Args: []string{"-test.run=TestLSPHelperProcess"}, Env: append(os.Environ(), "WIRECMD_LSP_HELPER="+mode), Stderr: io.Discard}
}

func TestLSPHelperProcess(t *testing.T) {
	mode := os.Getenv("WIRECMD_LSP_HELPER")
	if mode == "" {
		return
	}
	server := &fakeServer{mode: mode, documents: map[uri.URI]string{}, languages: map[uri.URI]string{}}
	server.changed = sync.NewCond(&server.mu)
	stream := jsonrpc2.NewStream(&stdioRWC{Reader: os.Stdin, Writer: os.Stdout})
	ctx, conn, client := protocol.NewServer(context.Background(), server, stream)
	server.client = client
	server.ctx = ctx
	<-conn.Done()
}

type stdioRWC struct {
	io.Reader
	io.Writer
}

func (*stdioRWC) Close() error { return nil }

type fakeServer struct {
	protocol.UnimplementedServer
	mode      string
	documents map[uri.URI]string
	languages map[uri.URI]string
	client    protocol.Client
	ctx       context.Context
	mu        sync.Mutex
	changed   *sync.Cond
}

func (s *fakeServer) Initialize(context.Context, *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	all := s.mode == "all"
	capabilities := protocol.ServerCapabilities{DefinitionProvider: protocol.Boolean(s.mode != "no-definition")}
	if all {
		capabilities.DeclarationProvider = protocol.Boolean(true)
		capabilities.TypeDefinitionProvider = protocol.Boolean(true)
		capabilities.ImplementationProvider = protocol.Boolean(true)
		capabilities.ReferencesProvider = protocol.Boolean(true)
		kind := protocol.TextDocumentSyncKindIncremental
		open := true
		capabilities.TextDocumentSync = &protocol.TextDocumentSyncOptions{OpenClose: &open, Change: &kind}
	}
	if s.mode == "language" {
		kind := protocol.TextDocumentSyncKindIncremental
		open := true
		capabilities.TextDocumentSync = &protocol.TextDocumentSyncOptions{OpenClose: &open, Change: &kind}
	}
	if s.mode == "utf8" {
		capabilities.PositionEncoding = protocol.PositionEncodingKindUTF8
	}
	if s.mode == "incremental" {
		kind := protocol.TextDocumentSyncKindIncremental
		open := true
		capabilities.TextDocumentSync = &protocol.TextDocumentSyncOptions{OpenClose: &open, Change: &kind}
	}
	return &protocol.InitializeResult{Capabilities: capabilities, ServerInfo: protocol.ServerInfo{Name: "fake", Version: protocol.NewOptional("1.0")}}, nil
}

func (*fakeServer) Initialized(context.Context, *protocol.InitializedParams) error { return nil }
func (*fakeServer) Shutdown(context.Context) error                                 { return nil }
func (*fakeServer) Exit(context.Context) error                                     { return nil }

func (s *fakeServer) DidOpen(_ context.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.documents[params.TextDocument.URI] = params.TextDocument.Text
	s.languages[params.TextDocument.URI] = string(params.TextDocument.LanguageID)
	s.changed.Broadcast()
	return nil
}

func (s *fakeServer) DidChange(_ context.Context, params *protocol.DidChangeTextDocumentParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, change := range params.ContentChanges {
		switch value := change.(type) {
		case *protocol.TextDocumentContentChangeWholeDocument:
			s.documents[params.TextDocument.URI] = value.Text
		case *protocol.TextDocumentContentChangePartial:
			s.documents[params.TextDocument.URI] = value.Text
		}
	}
	s.changed.Broadcast()
	return nil
}

func (*fakeServer) DidClose(context.Context, *protocol.DidCloseTextDocumentParams) error { return nil }

func (s *fakeServer) Definition(_ context.Context, params *protocol.DefinitionParams) (protocol.DefinitionResult, error) {
	if s.mode == "unsupported-request" {
		_, _ = s.client.Configuration(s.ctx, &protocol.ConfigurationParams{})
		return nil, io.ErrUnexpectedEOF
	}
	s.mu.Lock()
	if s.mode == "incremental" {
		want := "a😀b\n"
		if params.Position.Character == 1 {
			want = "changed\n"
		}
		for s.documents[params.TextDocument.URI] != want {
			s.changed.Wait()
		}
	} else if s.mode == "language" || s.mode == "all" {
		for s.documents[params.TextDocument.URI] == "" {
			s.changed.Wait()
		}
	}
	content := s.documents[params.TextDocument.URI]
	language := s.languages[params.TextDocument.URI]
	s.mu.Unlock()
	if content == "" {
		return nil, nil
	}
	if content == "a😀b\n" && params.Position.Character != 3 {
		return nil, io.ErrUnexpectedEOF
	}
	suffix := ".definition"
	if s.mode == "language" {
		suffix = "." + language
	}
	target := uri.File(params.TextDocument.URI.FsPath() + suffix)
	return protocol.LocationSlice{{URI: target, Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 2}, End: protocol.Position{Line: 1, Character: 4}}}}, nil
}

func (s *fakeServer) Declaration(_ context.Context, params *protocol.DeclarationParams) (protocol.DeclarationResult, error) {
	return protocol.LocationSlice{{URI: uri.File(params.TextDocument.URI.FsPath() + ".declaration")}}, nil
}

func (s *fakeServer) TypeDefinition(_ context.Context, params *protocol.TypeDefinitionParams) (protocol.DefinitionResult, error) {
	return protocol.LocationSlice{{URI: uri.File(params.TextDocument.URI.FsPath() + ".type-definition")}}, nil
}

func (s *fakeServer) Implementation(_ context.Context, params *protocol.ImplementationParams) (protocol.DefinitionResult, error) {
	return protocol.LocationSlice{{URI: uri.File(params.TextDocument.URI.FsPath() + ".implementation")}}, nil
}

func (s *fakeServer) References(_ context.Context, params *protocol.ReferenceParams) ([]protocol.Location, error) {
	suffix := ".references"
	if !params.Context.IncludeDeclaration {
		suffix += ".excluding-declaration"
	}
	return []protocol.Location{{URI: uri.File(params.TextDocument.URI.FsPath() + suffix)}}, nil
}
