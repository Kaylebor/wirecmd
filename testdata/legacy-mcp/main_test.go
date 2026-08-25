package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestProtocolRecorder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protocol.jsonl")
	recorder, err := openProtocolRecorder(path)
	if err != nil {
		t.Fatalf("open protocol recorder: %v", err)
	}
	recorder.record("2026-07-28")
	if err := recorder.close(); err != nil {
		t.Fatalf("close protocol recorder: %v", err)
	}

	entry := readProtocolEntry(t, path)
	if entry != (protocolEntry{ProtocolVersion: "2026-07-28"}) {
		t.Fatalf("protocol record = %#v", entry)
	}
}

func TestLoopbackTCPAddr(t *testing.T) {
	for _, address := range []string{"127.0.0.1:0", "[::1]:0"} {
		t.Run(address, func(t *testing.T) {
			address, err := loopbackTCPAddr(address)
			if err != nil {
				t.Fatalf("loopbackTCPAddr: %v", err)
			}
			if !address.IP.IsLoopback() {
				t.Fatalf("resolved non-loopback address %v", address)
			}
		})
	}
	for _, address := range []string{":0", "0.0.0.0:0", "[::]:0", "192.0.2.1:0"} {
		t.Run(address, func(t *testing.T) {
			if _, err := loopbackTCPAddr(address); err == nil {
				t.Fatal("non-loopback address was accepted")
			}
		})
	}
}

func TestServerRecordsInitializedProtocol(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protocol.jsonl")
	recorder, err := openProtocolRecorder(path)
	if err != nil {
		t.Fatalf("open protocol recorder: %v", err)
	}
	defer recorder.close() //nolint:errcheck // test teardown.

	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newServer(recorder)
	}, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()

	session := connectFixture(t, httpServer.URL)
	defer session.Close() //nolint:errcheck // test teardown.

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if data, err := os.ReadFile(path); err == nil && len(data) != 0 {
			var entry protocolEntry
			if err := json.Unmarshal(data, &entry); err != nil {
				t.Fatalf("decode protocol record: %v", err)
			}
			if entry != (protocolEntry{ProtocolVersion: "2025-11-25"}) {
				t.Fatalf("server protocol record = %#v", entry)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("server did not record the initialized protocol")
}

func readProtocolEntry(t *testing.T, path string) protocolEntry {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read protocol record: %v", err)
	}
	var entry protocolEntry
	if err := json.Unmarshal(data, &entry); err != nil {
		t.Fatalf("decode protocol record: %v", err)
	}
	return entry
}

func TestStatefulStreamableHTTPTools(t *testing.T) {
	httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newServer(nil)
	}, &mcp.StreamableHTTPOptions{JSONResponse: true}))
	defer httpServer.Close()

	first := connectFixture(t, httpServer.URL)
	defer first.Close() //nolint:errcheck // test teardown.

	set, err := first.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "set_value",
		Arguments: map[string]any{"value": "retained"},
	})
	if err != nil || set.IsError {
		t.Fatalf("set_value: result=%#v err=%v", set, err)
	}
	read, err := first.CallTool(context.Background(), &mcp.CallToolParams{Name: "read_value"})
	if err != nil || read.IsError {
		t.Fatalf("read_value: result=%#v err=%v", read, err)
	}
	if got := structuredBool(t, read, "exists"); !got {
		t.Fatal("read_value in the first session did not retain the value")
	}

	second := connectFixture(t, httpServer.URL)
	defer second.Close() //nolint:errcheck // test teardown.
	otherRead, err := second.CallTool(context.Background(), &mcp.CallToolParams{Name: "read_value"})
	if err != nil || otherRead.IsError {
		t.Fatalf("second read_value: result=%#v err=%v", otherRead, err)
	}
	if got := structuredBool(t, otherRead, "exists"); got {
		t.Fatal("a fresh HTTP session inherited another session's value")
	}

	deleted, err := first.CallTool(context.Background(), &mcp.CallToolParams{Name: "delete_value"})
	if err != nil || deleted.IsError {
		t.Fatalf("delete_value: result=%#v err=%v", deleted, err)
	}
	failed, err := first.CallTool(context.Background(), &mcp.CallToolParams{Name: "fail"})
	if err != nil {
		t.Fatalf("fail transport error: %v", err)
	}
	if !failed.IsError {
		t.Fatal("fail result did not report a tool error")
	}
}

func connectFixture(t *testing.T, endpoint string) *mcp.ClientSession {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "fixture-test", Version: "test"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return session
}

func structuredBool(t *testing.T, result *mcp.CallToolResult, field string) bool {
	t.Helper()
	data, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured result: %v", err)
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		t.Fatalf("decode structured result: %v", err)
	}
	value, ok := values[field].(bool)
	if !ok {
		t.Fatalf("structured result %s is missing %q", data, field)
	}
	return value
}
