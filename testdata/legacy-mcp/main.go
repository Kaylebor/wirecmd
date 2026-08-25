// legacy-mcp is a deliberately small historical MCP fixture for Wirecmd tests.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"sync"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const fixtureSDKVersion = "v1.6.1"

var (
	listenAddress  = flag.String("listen", "", "serve stateful Streamable HTTP on this address instead of stdio")
	protocolRecord = flag.String("protocol-record", "", "append observed initialized protocol versions as JSON Lines to this test-only file")
)

type fixtureState struct {
	mu    sync.Mutex
	value *string
}

type setValueArgs struct {
	Value string `json:"value" jsonschema:"value to retain in this process"`
}

type setValueResult struct {
	Value string `json:"value"`
}

type readValueResult struct {
	Exists bool   `json:"exists"`
	Value  string `json:"value"`
}

type deleteValueResult struct {
	Deleted bool `json:"deleted"`
}

func (s *fixtureState) setValue(_ context.Context, _ *mcp.CallToolRequest, input setValueArgs) (*mcp.CallToolResult, setValueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	value := input.Value
	s.value = &value
	return nil, setValueResult{Value: value}, nil
}

func (s *fixtureState) readValue(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, readValueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.value == nil {
		return nil, readValueResult{}, nil
	}
	return nil, readValueResult{Exists: true, Value: *s.value}, nil
}

func (s *fixtureState) deleteValue(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, deleteValueResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	deleted := s.value != nil
	s.value = nil
	return nil, deleteValueResult{Deleted: deleted}, nil
}

func fail(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	return nil, nil, errors.New("intentional legacy fixture tool failure")
}

type protocolEntry struct {
	ProtocolVersion string `json:"protocol_version"`
}

type protocolRecorder struct {
	mu   sync.Mutex
	file *os.File
}

func openProtocolRecorder(path string) (*protocolRecorder, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	return &protocolRecorder{file: file}, nil
}

func (r *protocolRecorder) record(requested string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := json.NewEncoder(r.file).Encode(protocolEntry{
		ProtocolVersion: requested,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "legacy-mcp: write protocol record: %v\n", err)
	}
}

func (r *protocolRecorder) close() error {
	if r == nil {
		return nil
	}
	return r.file.Close()
}

func newServer(recorder *protocolRecorder) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "legacy-mcp-fixture",
		Version: fixtureSDKVersion,
	}, &mcp.ServerOptions{
		InitializedHandler: func(_ context.Context, request *mcp.InitializedRequest) {
			params := request.Session.InitializeParams()
			if params != nil {
				recorder.record(params.ProtocolVersion)
			}
		},
	})

	state := new(fixtureState)
	mcp.AddTool(server, &mcp.Tool{Name: "set_value", Description: "Retain a value for this fixture process."}, state.setValue)
	mcp.AddTool(server, &mcp.Tool{Name: "read_value", Description: "Read the retained process-local value."}, state.readValue)
	mcp.AddTool(server, &mcp.Tool{Name: "delete_value", Description: "Delete the retained process-local value."}, state.deleteValue)
	mcp.AddTool(server, &mcp.Tool{Name: "fail", Description: "Return a deliberate MCP tool error."}, fail)
	return server
}

func main() {
	flag.Parse()

	recorder, err := openProtocolRecorder(*protocolRecord)
	if err != nil {
		fmt.Fprintf(os.Stderr, "legacy-mcp: open protocol record: %v\n", err)
		os.Exit(2)
	}
	defer recorder.close() //nolint:errcheck // diagnostics cannot recover from process shutdown.

	if *listenAddress == "" {
		server := newServer(recorder)
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			fmt.Fprintf(os.Stderr, "legacy-mcp: stdio server: %v\n", err)
		}
		return
	}

	address, err := loopbackTCPAddr(*listenAddress)
	if err != nil {
		fmt.Fprintf(os.Stderr, "legacy-mcp: listen: %v\n", err)
		os.Exit(2)
	}
	listener, err := net.ListenTCP("tcp", address)
	if err != nil {
		fmt.Fprintf(os.Stderr, "legacy-mcp: listen: %v\n", err)
		os.Exit(2)
	}
	defer listener.Close() //nolint:errcheck // the server owns the listener until process shutdown.

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newServer(recorder)
	}, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	fmt.Fprintf(os.Stderr, "legacy-mcp listening: http://%s/mcp\n", listener.Addr())
	if err := http.Serve(listener, mux); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintf(os.Stderr, "legacy-mcp: HTTP server: %v\n", err)
	}
}

func loopbackTCPAddr(address string) (*net.TCPAddr, error) {
	tcpAddr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		return nil, err
	}
	if tcpAddr.IP == nil || !tcpAddr.IP.IsLoopback() {
		return nil, fmt.Errorf("--listen must use a TCP loopback address")
	}
	return tcpAddr, nil
}
