package lspclient

import (
	"context"
	"io"
	"os"
	"path/filepath"
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
	session, err := Start(context.Background(), command, "go", workspace, "test")
	if err != nil {
		t.Fatal(err)
	}
	target, err := PrepareDefinition(path, 1, 3)
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
	target, err = PrepareDefinition(path, 1, 2)
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
	for _, test := range []struct {
		mode string
		want error
	}{{"no-definition", ErrCapabilityUnavailable}, {"utf8", ErrEncodingUnsupported}} {
		t.Run(test.mode, func(t *testing.T) {
			workspace := t.TempDir()
			_, err := Start(context.Background(), helperCommand(test.mode), "plain", workspace, "test")
			if !errorsIs(err, test.want) {
				t.Fatalf("Start() error = %v, want %v", err, test.want)
			}
		})
	}
}

func TestUnsupportedServerRequestIsReportedWhenDefinitionFails(t *testing.T) {
	workspace := t.TempDir()
	path := filepath.Join(workspace, "input.go")
	if err := os.WriteFile(path, []byte("call()\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	session, err := Start(context.Background(), helperCommand("unsupported-request"), "plain", workspace, "test")
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	target, err := PrepareDefinition(path, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Definition(context.Background(), target); !errorsIs(err, ErrServerRequestUnsupported) {
		t.Fatalf("Definition() error = %v, want %v", err, ErrServerRequestUnsupported)
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
	server := &fakeServer{mode: mode, documents: map[uri.URI]string{}}
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
	client    protocol.Client
	ctx       context.Context
}

func (s *fakeServer) Initialize(context.Context, *protocol.InitializeParams) (*protocol.InitializeResult, error) {
	capabilities := protocol.ServerCapabilities{DefinitionProvider: protocol.Boolean(s.mode != "no-definition")}
	if s.mode == "utf8" {
		capabilities.PositionEncoding = protocol.PositionEncodingKindUTF8
	}
	if s.mode == "incremental" {
		kind := protocol.TextDocumentSyncKindIncremental
		open := true
		capabilities.TextDocumentSync = &protocol.TextDocumentSyncOptions{OpenClose: &open, Change: &kind}
	}
	return &protocol.InitializeResult{Capabilities: capabilities}, nil
}

func (*fakeServer) Initialized(context.Context, *protocol.InitializedParams) error { return nil }
func (*fakeServer) Shutdown(context.Context) error                                 { return nil }
func (*fakeServer) Exit(context.Context) error                                     { return nil }

func (s *fakeServer) DidOpen(_ context.Context, params *protocol.DidOpenTextDocumentParams) error {
	s.documents[params.TextDocument.URI] = params.TextDocument.Text
	return nil
}

func (s *fakeServer) DidChange(_ context.Context, params *protocol.DidChangeTextDocumentParams) error {
	for _, change := range params.ContentChanges {
		switch value := change.(type) {
		case *protocol.TextDocumentContentChangeWholeDocument:
			s.documents[params.TextDocument.URI] = value.Text
		case *protocol.TextDocumentContentChangePartial:
			s.documents[params.TextDocument.URI] = value.Text
		}
	}
	return nil
}

func (*fakeServer) DidClose(context.Context, *protocol.DidCloseTextDocumentParams) error { return nil }

func (s *fakeServer) Definition(_ context.Context, params *protocol.DefinitionParams) (protocol.DefinitionResult, error) {
	if s.mode == "unsupported-request" {
		_, _ = s.client.Configuration(s.ctx, &protocol.ConfigurationParams{})
		return nil, io.ErrUnexpectedEOF
	}
	content := s.documents[params.TextDocument.URI]
	if content == "" {
		return nil, nil
	}
	if content == "a😀b\n" && params.Position.Character != 3 {
		return nil, io.ErrUnexpectedEOF
	}
	target := uri.File(params.TextDocument.URI.FsPath() + ".definition")
	return protocol.LocationSlice{{URI: target, Range: protocol.Range{Start: protocol.Position{Line: 1, Character: 2}, End: protocol.Position{Line: 1, Character: 4}}}}, nil
}
