package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Kaylebor/wirecmd/internal/buildinfo"
)

func TestDaemonRetainsSessionAndReloads(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	d := startTestDaemon(t)
	configPath := helperConfig(t, "", "")

	code, output, stderr := invoke(t, []string{"--config", configPath, "--json", `{"value":"retained"}`, "helper", "remember"})
	if code != exitOK || stderr != "" {
		t.Fatalf("daemon remember: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"--config", configPath, "helper", "recall"})
	if code != exitOK || stderr != "" || callValue(t, output) != "retained" {
		t.Fatalf("daemon recall: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, _ = invoke(t, []string{"--config", configPath, "helper", "failure"})
	if code != exitUpstreamTool || decodeOutput(t, output)["result"].(map[string]any)["messages"].([]any)[0] != "tool failure" {
		t.Fatalf("daemon tool error contract: code=%d output=%s", code, output)
	}

	code, output, stderr = invoke(t, []string{"daemon", "status"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["daemon"].(map[string]any)["active_instances"].(json.Number).String() != "1" {
		t.Fatalf("daemon status: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"daemon", "reload"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["reload"].(map[string]any)["instances_retired"].(json.Number).String() != "1" {
		t.Fatalf("daemon reload: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, _ = invoke(t, []string{"--config", configPath, "helper", "recall"})
	if code != exitOK || callValue(t, output) != "" {
		t.Fatalf("reloaded recall: code=%d output=%s", code, output)
	}

	// Direct mode remains explicitly fresh.
	code, output, _ = invoke(t, []string{"--direct", "--config", configPath, "helper", "recall"})
	if code != exitOK || callValue(t, output) != "" {
		t.Fatalf("direct recall: code=%d output=%s", code, output)
	}
	_ = d
}

func TestDaemonResourcesMatchDirectAndRetainSession(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	d := startTestDaemon(t)
	configPath := helperConfig(t, "", "")
	directArgs := []string{"--direct", "--config", configPath, "mcp", "helper", "resources"}
	directCode, directOutput, directStderr := invokeRaw(t, directArgs)
	if directCode != exitOK || directStderr != "" {
		t.Fatalf("direct resources: code=%d stderr=%q output=%s", directCode, directStderr, directOutput)
	}
	code, output, stderr := invokeRaw(t, []string{"--config", configPath, "mcp", "helper", "resources"})
	if code != exitOK || stderr != "" || !reflect.DeepEqual(decodeOutput(t, output), decodeOutput(t, directOutput)) {
		t.Fatalf("daemon resources: code=%d stderr=%q output=%s want=%s", code, stderr, output, directOutput)
	}
	code, output, stderr = invokeRaw(t, []string{"--config", configPath, "mcp", "helper", "resource", "test://a"})
	if code != exitOK || stderr != "" || len(decodeOutput(t, output)["contents"].([]any)) != 2 {
		t.Fatalf("daemon resource read: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invokeRaw(t, []string{"--config", configPath, "mcp", "helper", "resource", "test://presigned?signature=resource-presigned-query"})
	if code != exitOK || stderr != "" || strings.Contains(output, "resource-presigned-query") || strings.Contains(output, "content-presigned-query") {
		t.Fatalf("daemon presigned resource read: code=%d stderr=%q output=%s", code, stderr, output)
	}
	presigned := decodeOutput(t, output)
	if presigned["uri"] != "test://presigned?signature=[REDACTED]" || presigned["contents"].([]any)[0].(map[string]any)["uri"] != "test://content?signature=[REDACTED]" {
		t.Fatalf("daemon presigned resource output=%s", output)
	}
	directCode, directOutput, directStderr = invokeRaw(t, []string{"--direct", "--config", configPath, "mcp", "helper", "resource", "test://content-echo?token=request-secret"})
	if directCode != exitOK || directStderr != "" || strings.Contains(directOutput, "request-secret") || strings.Contains(directOutput, "response-secret") {
		t.Fatalf("direct content echo: code=%d stderr=%q output=%s", directCode, directStderr, directOutput)
	}
	code, output, stderr = invokeRaw(t, []string{"--config", configPath, "mcp", "helper", "resource", "test://content-echo?token=request-secret"})
	if code != exitOK || stderr != "" || strings.Contains(output, "request-secret") || strings.Contains(output, "response-secret") || !reflect.DeepEqual(decodeOutput(t, output), decodeOutput(t, directOutput)) {
		t.Fatalf("daemon content echo: code=%d stderr=%q output=%s want=%s", code, stderr, output, directOutput)
	}
	code, output, stderr = invokeRaw(t, []string{"--config", configPath, "mcp", "helper", "resource", "test://page?page=1"})
	if code != exitOK || stderr != "" {
		t.Fatalf("daemon page resource read: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invokeRaw(t, []string{"--config", configPath, "--json", `{"message":"Report response-secret has 100 rows"}`, "mcp", "helper", "tool", "a_tool"})
	if code != exitOK || stderr != "" || !strings.Contains(output, "Report response-secret has 100 rows") {
		t.Fatalf("retained redactor altered ordinary payload: code=%d stderr=%q output=%s", code, stderr, output)
	}
	directCode, directOutput, directStderr = invokeRaw(t, []string{"--direct", "--config", configPath, "mcp", "helper", "resource", "test://failure?token=issued-secret"})
	if directCode != exitProtocol || directStderr != "" || strings.Contains(directOutput, "issued-secret") {
		t.Fatalf("direct resource error: code=%d stderr=%q output=%s", directCode, directStderr, directOutput)
	}
	code, output, stderr = invokeRaw(t, []string{"--config", configPath, "mcp", "helper", "resource", "test://failure?token=issued-secret"})
	if code != exitProtocol || stderr != "" || strings.Contains(output, "issued-secret") || !reflect.DeepEqual(decodeOutput(t, output), decodeOutput(t, directOutput)) {
		t.Fatalf("daemon resource error: code=%d stderr=%q output=%s want=%s", code, stderr, output, directOutput)
	}
	directCode, directOutput, directStderr = invokeRaw(t, []string{"--direct", "--config", configPath, "mcp", "helper", "resource", "test://input"})
	if directCode != exitUserAction || directStderr != "" || decodeOutput(t, directOutput)["error"].(map[string]any)["code"] != "input_required" {
		t.Fatalf("direct input-required resource: code=%d stderr=%q output=%s", directCode, directStderr, directOutput)
	}
	code, output, stderr = invokeRaw(t, []string{"--config", configPath, "mcp", "helper", "resource", "test://input"})
	if code != exitUserAction || stderr != "" || !reflect.DeepEqual(decodeOutput(t, output), decodeOutput(t, directOutput)) {
		t.Fatalf("daemon input-required resource: code=%d stderr=%q output=%s want=%s", code, stderr, output, directOutput)
	}
	d.mu.Lock()
	active := len(d.pools)
	d.mu.Unlock()
	if active != 1 {
		t.Fatalf("daemon retained resource session pools=%d, want 1", active)
	}
}

func TestDaemonResourceReadRedactsAcrossContents(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("WIRECMD_RESOURCE_CROSS_CONTENT", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	_ = startTestDaemon(t)
	configPath := helperConfig(t, "", "")
	args := []string{"--direct", "--config", configPath, "mcp", "helper", "resource", "test://content-cross?token=request-cross-secret"}
	directCode, directOutput, directStderr := invokeRaw(t, args)
	for _, secret := range []string{"request-cross-secret", "cross-one-secret", "cross-two-secret"} {
		if strings.Contains(directOutput, secret) || strings.Contains(directStderr, secret) {
			t.Fatalf("direct cross-content output leaked %q: stderr=%q output=%s", secret, directStderr, directOutput)
		}
	}
	if directCode != exitOK || directStderr != "" {
		t.Fatalf("direct cross-content read: code=%d stderr=%q output=%s", directCode, directStderr, directOutput)
	}
	contents := decodeOutput(t, directOutput)["contents"].([]any)
	if len(contents) != 2 || contents[0].(map[string]any)["text"] != "text [REDACTED]" {
		t.Fatalf("direct cross-content text: %#v", contents)
	}
	blob, err := base64.StdEncoding.DecodeString(contents[1].(map[string]any)["blob"].(string))
	if err != nil || string(blob) != "blob [REDACTED]" {
		t.Fatalf("direct cross-content blob: %#v, %v", contents, err)
	}
	code, output, stderr := invokeRaw(t, []string{"--config", configPath, "mcp", "helper", "resource", "test://content-cross?token=request-cross-secret"})
	if code != exitOK || stderr != "" || !reflect.DeepEqual(decodeOutput(t, output), decodeOutput(t, directOutput)) {
		t.Fatalf("daemon cross-content read: code=%d stderr=%q output=%s want=%s", code, stderr, output, directOutput)
	}
	code, output, stderr = invokeRaw(t, []string{"--config", configPath, "--json", `{"message":"cross-one-secret remains ordinary tool data"}`, "mcp", "helper", "tool", "a_tool"})
	if code != exitOK || stderr != "" || !strings.Contains(output, "cross-one-secret remains ordinary tool data") {
		t.Fatalf("cross-content redactor altered later payload: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestResourceListPageFailureRedactsPriorEntries(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	t.Setenv("WIRECMD_RESOURCE_LIST_PAGE_FAILURE", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	_ = startTestDaemon(t)
	configPath := helperConfig(t, "", "")
	for _, test := range []struct {
		operation string
		secret    string
		code      string
	}{
		{operation: "resources", secret: "prior-page-resource-secret", code: "resource_list_failed"},
		{operation: "resource-templates", secret: "prior-page-template-secret", code: "resource_template_list_failed"},
	} {
		t.Run(test.operation, func(t *testing.T) {
			directCode, directOutput, directStderr := invokeRaw(t, []string{"--direct", "--config", configPath, "mcp", "helper", test.operation})
			if directCode != exitProtocol || directStderr != "" || strings.Contains(directOutput, test.secret) || decodeOutput(t, directOutput)["error"].(map[string]any)["code"] != test.code {
				t.Fatalf("direct %s page failure: code=%d stderr=%q output=%s", test.operation, directCode, directStderr, directOutput)
			}
			code, output, stderr := invokeRaw(t, []string{"--config", configPath, "mcp", "helper", test.operation})
			if code != exitProtocol || stderr != "" || strings.Contains(output, test.secret) || !reflect.DeepEqual(decodeOutput(t, output), decodeOutput(t, directOutput)) {
				t.Fatalf("daemon %s page failure: code=%d stderr=%q output=%s want=%s", test.operation, code, stderr, output, directOutput)
			}
		})
	}
}

func TestResourceListingRedactionScopesAllEntriesWithoutRetainingSecrets(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	const secret = "entry_uri_secret"
	t.Setenv("WIRECMD_RESOURCE_ENTRY_SECRET", secret)
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	_ = startTestDaemon(t)
	configPath := helperConfig(t, "", "")

	for _, operation := range []string{"resources", "resource-templates"} {
		directCode, directOutput, directStderr := invokeRaw(t, []string{"--direct", "--config", configPath, "mcp", "helper", operation})
		if directCode != exitOK || directStderr != "" {
			t.Fatalf("direct %s: code=%d stderr=%q output=%s", operation, directCode, directStderr, directOutput)
		}
		code, output, stderr := invokeRaw(t, []string{"--config", configPath, "mcp", "helper", operation})
		if code != exitOK || stderr != "" || !reflect.DeepEqual(decodeOutput(t, output), decodeOutput(t, directOutput)) {
			t.Fatalf("daemon %s: code=%d stderr=%q output=%s want=%s", operation, code, stderr, output, directOutput)
		}
		decoded := decodeOutput(t, output)
		if operation == "resources" {
			found := 0
			for _, raw := range decoded["resources"].([]any) {
				entry := raw.(map[string]any)
				if entry["uri"] != "test://entry-secret?token=[REDACTED]" && entry["uri"] != "test://entry-clean" {
					continue
				}
				found++
				for _, field := range []string{"name", "title", "description", "mime_type"} {
					if entry[field] != "[REDACTED]" {
						t.Fatalf("resource %q leaked URI-derived secret in %s: %#v", entry["uri"], field, entry)
					}
				}
			}
			if found != 2 {
				t.Fatalf("resource listing did not include both scoped entries: %#v", decoded["resources"])
			}
		} else {
			foundExpression, foundScheme, foundUserinfo, foundSplit, foundValueless, foundExpandedValueless, foundOrdinary := false, false, false, false, false, false, false
			for _, raw := range decoded["resource_templates"].([]any) {
				entry := raw.(map[string]any)
				if entry["uri_template"] == "test://a/{id}" {
					foundOrdinary = true
					continue
				}
				if entry["uri_template"] != "test://template-clean/{entry_uri_secret}" && entry["uri_template"] != "{scheme}://template-scheme?token=[REDACTED]" && entry["uri_template"] != "https://{user}:{password}@template-userinfo?token=[REDACTED]" && entry["uri_template"] != "https://[REDACTED]{var}[REDACTED]:[REDACTED]{var}[REDACTED]@template-split?token=[REDACTED]{var}[REDACTED]#[REDACTED]{var}[REDACTED]" && entry["uri_template"] != "test://template-valueless?[REDACTED]" && entry["uri_template"] != "test://template-expanded{?id}&[REDACTED]" {
					continue
				}
				if entry["uri_template"] == "test://template-clean/{entry_uri_secret}" {
					foundExpression = true
				} else if entry["uri_template"] == "{scheme}://template-scheme?token=[REDACTED]" {
					foundScheme = true
				} else if entry["uri_template"] == "https://{user}:{password}@template-userinfo?token=[REDACTED]" {
					foundUserinfo = true
				} else if entry["uri_template"] == "test://template-valueless?[REDACTED]" {
					foundValueless = true
				} else if entry["uri_template"] == "test://template-expanded{?id}&[REDACTED]" {
					foundExpandedValueless = true
				} else {
					foundSplit = true
				}
				for _, field := range []string{"name", "title", "description", "mime_type"} {
					want := "[REDACTED]"
					if foundSplit && entry["uri_template"] == "https://[REDACTED]{var}[REDACTED]:[REDACTED]{var}[REDACTED]@template-split?token=[REDACTED]{var}[REDACTED]#[REDACTED]{var}[REDACTED]" {
						want = "[REDACTED]{var}[REDACTED]"
					}
					if entry[field] != want {
						t.Fatalf("template entry leaked URI-derived secret in %s: %#v", field, entry)
					}
				}
			}
			if !foundExpression || !foundScheme || !foundUserinfo || !foundSplit || !foundValueless || !foundExpandedValueless || !foundOrdinary {
				t.Fatalf("template listing lost an expression, userinfo, split literal, valueless parameter, scheme, or ordinary entry: %#v", decoded["resource_templates"])
			}
		}
	}
	code, output, stderr := invokeRaw(t, []string{"--config", configPath, "--json", `{"message":"entry_uri_secret remains ordinary tool data"}`, "mcp", "helper", "tool", "a_tool"})
	if code != exitOK || stderr != "" || !strings.Contains(output, "entry_uri_secret remains ordinary tool data") {
		t.Fatalf("listing redactor altered later payload: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestClientDaemonHelloUsesBuildVersion(t *testing.T) {
	hello := clientDaemonHello()
	if hello.Type != "hello" || hello.Protocol != daemonProtocol || hello.Version != buildinfo.Version() {
		t.Fatalf("client daemon hello = %#v", hello)
	}
}

func TestDaemonFocusedHelpAndProjectedCall(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	configPath := helperConfig(t, "", "")

	directCode, directOutput, directStderr := invoke(t, []string{"--direct", "--config", configPath, "--help", "helper", "projected"})
	if directCode != exitOK || directStderr != "" {
		t.Fatalf("direct tool help: code=%d stderr=%q output=%s", directCode, directStderr, directOutput)
	}
	code, output, stderr := invoke(t, []string{"--config", configPath, "--help", "helper", "projected"})
	if code != exitOK || stderr != "" || !strings.Contains(output, "--query") || strings.HasPrefix(output, "{") || output != directOutput {
		t.Fatalf("daemon tool help: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"--config", configPath, "helper", "projected", "--help"})
	if code != exitOK || stderr != "" || output != directOutput {
		t.Fatalf("daemon suffix tool help: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"--config", configPath, "helper", "projected", "--query", "ignored", "-h"})
	if code != exitOK || stderr != "" || output != directOutput {
		t.Fatalf("daemon suffix tool help after arguments: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"--config", configPath, "helper", "projected_help", "--help"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["help"] != true {
		t.Fatalf("daemon explicit tool help argument: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"--config", configPath, "helper", "projected", "--query", "daemon", "--enabled=false", "--", `{"tool_name":"one","toolName":"two"}`})
	if code != exitOK || stderr != "" {
		t.Fatalf("daemon projected call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	data := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["query"] != "daemon" || data["enabled"] != false || data["tool_name"] != "one" || data["toolName"] != "two" {
		t.Fatalf("daemon projected data = %#v", data)
	}
}

func TestDaemonStreamableHTTPContracts(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	fixture := newHTTPFixture(t)
	config := httpConfig(t, fixture.URL)

	directCode, directOutput, directStderr := invoke(t, []string{"--direct", "--config", config, "--help", "remote", "projected"})
	if directCode != exitOK || directStderr != "" {
		t.Fatalf("direct HTTP help: code=%d stderr=%q output=%s", directCode, directStderr, directOutput)
	}
	code, output, stderr := invoke(t, []string{"--config", config, "--help", "remote", "projected"})
	if code != exitOK || stderr != "" || output != directOutput {
		t.Fatalf("daemon HTTP help: code=%d stderr=%q output=%s", code, stderr, output)
	}

	code, output, stderr = invoke(t, []string{"--config", config, "remote", "projected", "--query", "daemon", "--enabled=false", "--", `{"tool_name":"one","toolName":"two"}`})
	if code != exitOK || stderr != "" {
		t.Fatalf("daemon HTTP projected call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	data := decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)
	if data["query"] != "daemon" || data["enabled"] != false || data["tool_name"] != "one" || data["toolName"] != "two" {
		t.Fatalf("daemon HTTP data = %#v", data)
	}

	code, output, stderr = invoke(t, []string{"--config", config, "remote", `{"tool":"a_tool","arguments":{"name":"Ada"}}`})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["name"] != "Ada" {
		t.Fatalf("daemon HTTP exact call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"daemon", "status"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["daemon"].(map[string]any)["active_instances"].(json.Number).String() != "1" {
		t.Fatalf("HTTP daemon status: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestDaemonStreamableHTTPCredentialsSelectInstances(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	fixture := newHTTPFixture(t)
	config := httpValuesConfig(t, fixture.URL)
	t.Setenv("WIRECMD_HTTP_KEY", "shared-key")
	t.Setenv("WIRECMD_HTTP_TOKEN", "first-token")

	for i := 0; i < 2; i++ {
		code, output, stderr := invoke(t, []string{"--config", config, "remote"})
		if code != exitOK || stderr != "" {
			t.Fatalf("same credentials call %d: code=%d stderr=%q output=%s", i, code, stderr, output)
		}
	}
	code, output, stderr := invoke(t, []string{"daemon", "status"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["daemon"].(map[string]any)["active_instances"].(json.Number).String() != "1" {
		t.Fatalf("same credentials status: code=%d stderr=%q output=%s", code, stderr, output)
	}

	t.Setenv("WIRECMD_HTTP_TOKEN", "second-token")
	code, output, stderr = invoke(t, []string{"--config", config, "remote"})
	if code != exitOK || stderr != "" {
		t.Fatalf("different credentials call: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, stderr = invoke(t, []string{"daemon", "status"})
	if code != exitOK || stderr != "" || decodeOutput(t, output)["daemon"].(map[string]any)["active_instances"].(json.Number).String() != "2" {
		t.Fatalf("different credentials status: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestDaemonStreamableHTTPColdCallPrimesOnce(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	fixture := newHTTPFixture(t)
	config := httpConfig(t, fixture.URL)
	code, output, stderr := invoke(t, []string{"--config", config, "--help", "remote", "a_tool"})
	if code != exitOK || stderr != "" {
		t.Fatalf("daemon HTTP help: code=%d stderr=%q output=%s", code, stderr, output)
	}
	partialToolLists := fixture.methodCount("tools/list")

	primingRequests := 0
	for i := 0; i < 2; i++ {
		code, output, stderr = invoke(t, []string{"--config", config, "remote", `{"tool":"header_tool","arguments":{"region":"EU"}}`})
		if code != exitOK || stderr != "" {
			t.Fatalf("daemon HTTP call %d: code=%d stderr=%q output=%s", i+1, code, stderr, output)
		}
		if i == 0 {
			primingRequests = fixture.methodCount("tools/list")
			if primingRequests <= partialToolLists {
				t.Fatalf("cold daemon call did not complete SDK cache priming: before=%d after=%d", partialToolLists, primingRequests)
			}
		}
	}
	if got := fixture.methodCount("tools/list"); got != primingRequests {
		t.Fatalf("tools/list requests after retained call = %d, want unchanged count %d", got, primingRequests)
	}
}

func TestDaemonMarksClosedHTTPInstanceBroken(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	fixture := newHTTPFixture(t)
	config := httpConfig(t, fixture.URL)
	code, output, _ := invoke(t, []string{"--config", config, "remote", "a_tool"})
	if code != exitOK {
		t.Fatalf("initial HTTP call: code=%d output=%s", code, output)
	}
	fixture.Close()
	code, output, _ = invoke(t, []string{"--config", config, "remote", "a_tool"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "connection_closed" {
		t.Fatalf("closed HTTP call: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"--config", config, "remote", "a_tool"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "instance_unavailable" {
		t.Fatalf("broken HTTP instance: code=%d output=%s", code, output)
	}
}

func TestDaemonCancellationReleasesHTTPInstance(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	fixture := newHTTPFixture(t)
	config := httpConfig(t, fixture.URL)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		done <- Run(ctx, namespacedTestArgs([]string{"--config", config, "remote", "block"}), strings.NewReader(""), &stdout, &stderr)
	}()
	select {
	case <-fixture.blockStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP block tool did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code != exitTransport {
			t.Fatalf("canceled HTTP daemon caller code=%d, want transport failure", code)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled HTTP daemon client remained blocked")
	}
	waitForBrokenInstance(t)
	code, output, _ := invoke(t, []string{"--config", config, "remote", "a_tool"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "instance_unavailable" {
		t.Fatalf("canceled HTTP session must remain honestly unavailable: code=%d output=%s", code, output)
	}
}

func TestDaemonCancellationDuringHTTPToolDiscoveryMarksInstanceBroken(t *testing.T) {
	assertDaemonCanceledHTTPToolListBreaksInstance(t, []string{"remote"})
}

func TestDaemonCancellationDuringHTTPFocusedHelpMarksInstanceBroken(t *testing.T) {
	assertDaemonCanceledHTTPToolListBreaksInstance(t, []string{"--help", "remote", "projected"})
}

func TestDaemonCancellationDuringHTTPProjectedSchemaMarksInstanceBroken(t *testing.T) {
	assertDaemonCanceledHTTPToolListBreaksInstance(t, []string{"remote", "projected", "--query", "Ada"})
}

func TestDaemonCanceledAfterSuccessfulSDKOperationKeepsHTTPInstanceHealthy(t *testing.T) {
	instance := &retainedInstance{breakOnRequestCancel: true}
	(&daemon{}).noteSDKOperation(instance, nil)
	if instance.broken {
		t.Fatal("cancellation after a successful SDK operation must not retire a healthy HTTP instance")
	}
}

func TestDaemonCanceledAfterLocalPostSDKErrorsKeepsHTTPInstanceHealthy(t *testing.T) {
	for _, appErr := range []*appError{
		protocolError("tool_not_found", "tool was not advertised", "list tools"),
		protocolError("tool_schema_invalid", "tool schema is invalid", "use exact JSON"),
		protocolError("unsupported_result", "result content is unsupported", "inspect upstream output"),
		{category: "user_action", code: "input_required", message: "input required", action: "continue interactively", exitCode: exitUserAction},
		{category: "upstream_tool", code: "tool_reported_error", message: "tool failed", action: "correct arguments", exitCode: exitUpstreamTool},
	} {
		instance := &retainedInstance{breakOnRequestCancel: true}
		(&daemon{}).noteSDKOperation(instance, appErr)
		if instance.broken {
			t.Fatalf("local post-SDK error %q must not retire a healthy HTTP instance", appErr.code)
		}
	}
}

func assertDaemonCanceledHTTPToolListBreaksInstance(t *testing.T, operation []string) {
	t.Helper()
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	fixture := newHTTPFixtureWithBlockedToolList(t, true)
	config := httpConfig(t, fixture.URL)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan int, 1)
	args := append([]string{"--config", config}, operation...)
	go func() {
		var stdout, stderr bytes.Buffer
		done <- Run(ctx, namespacedTestArgs(args), strings.NewReader(""), &stdout, &stderr)
	}()
	select {
	case <-fixture.toolListStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP tool listing did not start")
	}
	cancel()
	select {
	case code := <-done:
		if code != exitTransport {
			t.Fatalf("canceled HTTP operation code=%d, want transport failure", code)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled HTTP operation remained blocked")
	}
	fixture.releaseToolList()
	waitForBrokenInstance(t)
	code, output, _ := invoke(t, []string{"--config", config, "remote", "a_tool"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "instance_unavailable" {
		t.Fatalf("canceled HTTP session must remain honestly unavailable: code=%d output=%s", code, output)
	}
}

func TestDaemonCanceledQueuedHTTPRequestDoesNotBreakInstance(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	fixture := newHTTPFixture(t)
	config := httpConfig(t, fixture.URL)
	activeDone := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		activeDone <- Run(context.Background(), namespacedTestArgs([]string{"--config", config, "remote", "hold"}), strings.NewReader(""), &stdout, &stderr)
	}()
	select {
	case <-fixture.holdStarted:
	case <-time.After(time.Second):
		t.Fatal("HTTP hold tool did not start")
	}
	queuedCtx, cancel := context.WithCancel(context.Background())
	queuedDone := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		queuedDone <- Run(queuedCtx, namespacedTestArgs([]string{"--config", config, "remote", "a_tool"}), strings.NewReader(""), &stdout, &stderr)
	}()
	cancel()
	select {
	case code := <-queuedDone:
		if code != exitTransport {
			t.Fatalf("queued canceled caller code=%d, want transport failure", code)
		}
	case <-time.After(time.Second):
		t.Fatal("queued canceled caller remained blocked on IPC")
	}
	fixture.releaseHold()
	select {
	case code := <-activeDone:
		if code != exitOK {
			t.Fatalf("active HTTP hold code=%d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("active HTTP hold did not complete")
	}
	code, output, _ := invoke(t, []string{"daemon", "status"})
	if code != exitOK || decodeOutput(t, output)["daemon"].(map[string]any)["broken_instances"].(json.Number).String() != "0" {
		t.Fatalf("queued cancellation broke healthy instance: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"--config", config, "remote", "a_tool"})
	if code != exitOK || !strings.Contains(output, `"tool":"a_tool"`) {
		t.Fatalf("healthy instance was not reusable after queued cancellation: code=%d output=%s", code, output)
	}
}

func TestDaemonRedactsHTTPQueryInConnectionDiagnostics(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	nonMCP := newAbruptCloseHTTPServer(t)
	endpoint := nonMCP.URL + "/mcp?access_token=daemon-query-secret"
	config := httpConfig(t, endpoint)
	code, output, stderr := invoke(t, []string{"--config", config, "remote"})
	if code != exitTransport || strings.Contains(output, "access_token") || strings.Contains(output, "daemon-query-secret") || strings.Contains(stderr, "access_token") || strings.Contains(stderr, "daemon-query-secret") {
		t.Fatalf("daemon HTTP query disclosure: code=%d stdout=%q stderr=%q", code, output, stderr)
	}
	if !strings.Contains(output, "?[REDACTED]") {
		t.Fatalf("daemon HTTP endpoint was not usefully sanitized: %s", output)
	}
}

func waitForBrokenInstance(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for {
		code, output, _ := invoke(t, []string{"daemon", "status"})
		if code == exitOK {
			daemon := decodeOutput(t, output)["daemon"].(map[string]any)
			if daemon["broken_instances"].(json.Number).String() == "1" {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not mark canceled HTTP session broken: code=%d output=%s", code, output)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDaemonProjectedErrorsRedactSecretsAndUsePrivateSchema(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	const secret = "daemon-projected-secret"
	t.Setenv("WIRECMD_DAEMON_PROJECTED_SECRET", secret)
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	config := helperConfig(t, "", `env SECRET=(secret)"env://WIRECMD_DAEMON_PROJECTED_SECRET"`)
	flag, ok := projectedFlag(secret)
	if !ok {
		t.Fatal("fixture secret must form a projected flag")
	}

	for _, args := range [][]string{
		{"--config", config, "helper", "semantic_secret", "--" + flag + "-unknown", "value"},
		{"--config", config, "helper", "semantic_secret", "--" + flag, "value", "--", `{"daemon-projected-secret":"other"}`},
	} {
		code, output, stderr := invoke(t, args)
		if code != exitInvocation || strings.Contains(output, secret) || strings.Contains(stderr, secret) || !strings.Contains(output, "[REDACTED]") {
			t.Fatalf("daemon projected secret error: code=%d stderr=%q output=%s", code, stderr, output)
		}
	}
	code, output, stderr := invoke(t, []string{"--config", config, "helper", "semantic_secret", "--" + flag, "value"})
	if code != exitOK || stderr != "" || strings.Contains(output, secret) || decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["matched"] != true {
		t.Fatalf("daemon private schema call: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestDaemonConfigMismatchAndSecretIsolation(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	directory := t.TempDir()
	path := filepath.Join(directory, "wirecmd.kdl")
	writeSource(t, path, helperSource(`env SECRET=(secret)"env://WIRECMD_DAEMON_SECRET"`))
	t.Setenv("WIRECMD_DAEMON_SECRET", "initial-secret")
	code, output, stderr := invoke(t, []string{"--config", path, "helper", "secret"})
	if code != exitOK || stderr != "" || strings.Contains(output, "initial-secret") {
		t.Fatalf("initial daemon secret: code=%d stderr=%q output=%s", code, stderr, output)
	}

	// The client sees the updated file, but the daemon does not apply it until
	// reload and must not silently execute a changed selected server.
	writeSource(t, path, helperSource(`arg "changed"`))
	code, output, stderr = invoke(t, []string{"--config", path, "helper"})
	if code != exitConfiguration || !strings.Contains(stderr, "daemon reload") || decodeOutput(t, output)["error"].(map[string]any)["code"] != "config_mismatch" {
		t.Fatalf("changed execution mismatch: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestDaemonRejectsMovedRelativeRootWithoutReload(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	baseDirectory := t.TempDir()
	localDirectory := t.TempDir()
	base := filepath.Join(baseDirectory, "base.kdl")
	local := filepath.Join(localDirectory, "local.kdl")
	writeSource(t, base, helperSourceWithRoot("."))
	writeSource(t, local, "wirecmd {}\n")
	canonicalBaseDirectory, err := filepath.EvalSymlinks(baseDirectory)
	if err != nil {
		t.Fatal(err)
	}

	code, output, stderr := invoke(t, []string{"--config", base, "--config", local, "helper", "working_directory"})
	if code != exitOK || stderr != "" || callCWD(t, output) != canonicalBaseDirectory {
		t.Fatalf("initial root: code=%d stderr=%q output=%s", code, stderr, output)
	}

	// Preserve the ordered paths while moving the identical relative root to a
	// different declaration file. Its effective absolute workspace changes.
	writeSource(t, base, helperSource(""))
	writeSource(t, local, "wirecmd {\nroot \".\"\n}\n")
	code, output, stderr = invoke(t, []string{"--config", base, "--config", local, "helper", "working_directory"})
	if code != exitConfiguration || !strings.Contains(stderr, "daemon reload") || decodeOutput(t, output)["error"].(map[string]any)["code"] != "config_mismatch" {
		t.Fatalf("moved root must not execute cached workspace: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestDaemonCoalescesConcurrentStartup(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	configPath := helperConfig(t, "", "")
	const callers = 8
	errs := make(chan string, callers)
	var callersWG sync.WaitGroup
	for range callers {
		callersWG.Add(1)
		go func() {
			defer callersWG.Done()
			code, output, stderr := invoke(t, []string{"--config", configPath, "helper"})
			if code != exitOK || stderr != "" || !strings.Contains(output, `"tools"`) {
				errs <- fmt.Sprintf("code=%d stderr=%q output=%s", code, stderr, output)
			}
		}()
	}
	callersWG.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
	code, output, _ := invoke(t, []string{"daemon", "status"})
	if code != exitOK || decodeOutput(t, output)["daemon"].(map[string]any)["active_instances"].(json.Number).String() != "1" {
		t.Fatalf("concurrent status: code=%d output=%s", code, output)
	}
}

func TestDaemonCoalescedStartupSurvivesInitiatorCancellation(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	d := startTestDaemon(t)
	started := filepath.Join(t.TempDir(), "started")
	children := filepath.Join(t.TempDir(), "children")
	configPath := helperConfig(t, "", "env WIRECMD_HELPER_STARTED_FILE="+strconv.Quote(started)+"\nenv WIRECMD_START_DELAY=\"500ms\"\nenv WIRECMD_CHILD_COUNT_FILE="+strconv.Quote(children))

	firstCtx, cancelFirst := context.WithCancel(context.Background())
	firstDone := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		firstDone <- Run(firstCtx, namespacedTestArgs([]string{"--config", configPath, "helper", "a_tool"}), strings.NewReader(""), &stdout, &stderr)
	}()
	waitForFile(t, started)

	type invocation struct {
		code   int
		output string
		stderr string
	}
	secondDone := make(chan invocation, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), namespacedTestArgs([]string{"--config", configPath, "helper", "a_tool"}), strings.NewReader(""), &stdout, &stderr)
		secondDone <- invocation{code: code, output: stdout.String(), stderr: stderr.String()}
	}()
	waitForDaemonConnections(t, d, 2)
	cancelFirst()

	select {
	case code := <-firstDone:
		if code != exitTransport {
			t.Fatalf("canceled startup caller code=%d, want transport failure", code)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled startup caller remained blocked")
	}
	select {
	case result := <-secondDone:
		if result.code != exitOK || result.stderr != "" || !strings.Contains(result.output, `"tool":"a_tool"`) {
			t.Fatalf("coalesced caller: code=%d stderr=%q output=%s", result.code, result.stderr, result.output)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("coalesced caller did not receive the shared retained instance")
	}
	contents, err := os.ReadFile(children)
	if err != nil {
		t.Fatalf("read child count: %v", err)
	}
	if pids := strings.Fields(string(contents)); len(pids) != 1 {
		t.Fatalf("retained child starts = %q, want exactly one", contents)
	}
}

func TestDaemonClientCancellationCancelsUpstreamOperation(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	started := filepath.Join(t.TempDir(), "started")
	canceled := filepath.Join(t.TempDir(), "canceled")
	configPath := helperConfig(t, "", "env WIRECMD_BLOCK_STARTED_FILE="+strconv.Quote(started)+"\nenv WIRECMD_BLOCK_CANCELED_FILE="+strconv.Quote(canceled))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		done <- Run(ctx, namespacedTestArgs([]string{"--config", configPath, "helper", "block"}), strings.NewReader(""), &stdout, &stderr)
	}()
	waitForFile(t, started)
	cancel()
	select {
	case code := <-done:
		if code != exitTransport {
			t.Fatalf("canceled daemon client code=%d, want transport failure", code)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled daemon client remained blocked on IPC")
	}
	waitForFile(t, canceled)

	// The block call used the retained instance. A following call can only
	// acquire its mutex if upstream cancellation completed that call.
	done = make(chan int, 1)
	go func() {
		var stdout, stderr bytes.Buffer
		code := Run(context.Background(), namespacedTestArgs([]string{"--config", configPath, "helper", "a_tool"}), strings.NewReader(""), &stdout, &stderr)
		if code != exitOK || stderr.Len() != 0 || !strings.Contains(stdout.String(), `"tool":"a_tool"`) {
			code = -1
		}
		done <- code
	}()
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatal("subsequent daemon call did not use a usable retained instance")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled daemon operation retained the instance mutex")
	}
}

// TestOfficialMemoryFixture is an opt-in qualification against the pinned
// SDK's example binary. CI does not download or build that external fixture;
// the recorded developer command supplies WIRECMD_OFFICIAL_MEMORY_BINARY.
func TestOfficialMemoryFixture(t *testing.T) {
	binary := os.Getenv("WIRECMD_OFFICIAL_MEMORY_BINARY")
	if binary == "" {
		t.Skip("set WIRECMD_OFFICIAL_MEMORY_BINARY to qualify the official SDK memory example")
	}
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	startTestDaemon(t)
	configPath := filepath.Join(t.TempDir(), "memory.kdl")
	writeSource(t, configPath, "wirecmd {\nmcp \"memory\" {\nscope \"workspace\"\nstdio "+strconv.Quote(binary)+"\n}\n}\n")
	entity := `{"entities":[{"name":"WirecmdFixture","entityType":"test","observations":["retained"]}]}`
	code, output, stderr := invoke(t, []string{"--config", configPath, "--json", entity, "memory", "create_entities"})
	if code != exitOK || stderr != "" {
		t.Fatalf("create entities: code=%d stderr=%q output=%s", code, stderr, output)
	}
	code, output, _ = invoke(t, []string{"--config", configPath, "memory", "read_graph"})
	if code != exitOK || !strings.Contains(output, "WirecmdFixture") {
		t.Fatalf("retained graph: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"--direct", "--config", configPath, "memory", "read_graph"})
	if code != exitOK || strings.Contains(output, "WirecmdFixture") {
		t.Fatalf("fresh direct graph: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"daemon", "reload"})
	if code != exitOK {
		t.Fatalf("reload: code=%d output=%s", code, output)
	}
	code, output, _ = invoke(t, []string{"--config", configPath, "memory", "read_graph"})
	if code != exitOK || strings.Contains(output, "WirecmdFixture") {
		t.Fatalf("fresh graph after reload: code=%d output=%s", code, output)
	}
}

func TestDaemonAdminGrammarAndRuntimeSafety(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	code, output, _ := invoke(t, []string{"daemon", "status"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "daemon_unavailable" {
		t.Fatalf("offline status: code=%d output=%s", code, output)
	}
	configPath := helperConfig(t, "", "")
	code, output, _ = invoke(t, []string{"--config", configPath, "daemon", "status"})
	if code != exitInvocation || decodeOutput(t, output)["error"].(map[string]any)["code"] != "daemon_admin_flags" {
		t.Fatalf("admin flags: code=%d output=%s", code, output)
	}

	unsafe := filepath.Join(t.TempDir(), "unsafe")
	if err := os.Mkdir(unsafe, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("XDG_RUNTIME_DIR", unsafe)
	code, output, _ = invoke(t, []string{"daemon", "status"})
	if code != exitTransport || decodeOutput(t, output)["error"].(map[string]any)["code"] != "runtime_dir_unsafe" {
		t.Fatalf("unsafe runtime: code=%d output=%s", code, output)
	}
}

func TestRuntimePathsValidateExplicitRuntimeDirectory(t *testing.T) {
	valid := t.TempDir()
	if err := os.Chmod(valid, 0o700); err != nil {
		t.Fatal(err)
	}
	unsafe := filepath.Join(t.TempDir(), "unsafe")
	if err := os.Mkdir(unsafe, 0o755); err != nil {
		t.Fatal(err)
	}
	symlink := filepath.Join(t.TempDir(), "runtime-link")
	if err := os.Symlink(valid, symlink); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing")

	for _, test := range []struct {
		name string
		path string
		code string
		want string
	}{
		{name: "empty", path: "", code: "runtime_dir_unavailable"},
		{name: "relative", path: "runtime", code: "runtime_dir_unavailable"},
		{name: "missing", path: missing, code: "runtime_dir_unavailable"},
		{name: "unsafe", path: unsafe, code: "runtime_dir_unsafe"},
		{name: "symlink", path: symlink, want: filepath.Join(symlink, "wirecmd")},
		{name: "valid", path: valid, want: filepath.Join(valid, "wirecmd")},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("XDG_RUNTIME_DIR", test.path)
			directory, _, _, appErr := runtimePaths()
			if test.code != "" {
				if appErr == nil || appErr.code != test.code {
					t.Fatalf("runtimePaths() error = %#v, want code %q", appErr, test.code)
				}
				return
			}
			if appErr != nil || directory != test.want {
				t.Fatalf("runtimePaths() = %q, %#v", directory, appErr)
			}
		})
	}
}

func TestDaemonSocketSafetyAndStaleRecovery(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	directory, socket, _, appErr := openRuntimeDirectory()
	if appErr != nil {
		t.Fatalf("runtime directory: %#v", appErr)
	}
	fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Bind(fd, &syscall.SockaddrUnix{Name: socket}); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Close(fd); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		t.Fatal(err)
	}
	d, appErr := newDaemon(io.Discard)
	if appErr != nil {
		t.Fatalf("stale socket recovery: %#v", appErr)
	}
	if _, secondErr := newDaemon(io.Discard); secondErr == nil || secondErr.code != "daemon_already_running" {
		t.Fatalf("singleton lock: %#v", secondErr)
	}
	d.close()

	if err := os.WriteFile(socket, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if d, unsafeErr := newDaemon(io.Discard); d != nil || unsafeErr == nil || unsafeErr.code != "daemon_socket_unsafe" {
		t.Fatalf("non-socket refusal: daemon=%v error=%#v", d, unsafeErr)
	}
	if info, err := os.Stat(socket); err != nil || !info.Mode().IsRegular() {
		t.Fatalf("non-socket was changed: info=%v error=%v", info, err)
	}
	if info, err := os.Stat(directory); err != nil || info.Mode().Perm() != 0o700 {
		t.Fatalf("runtime directory permissions: info=%v error=%v", info, err)
	}
}

func TestDaemonHandshakeCancellation(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	_, socket, _, appErr := openRuntimeDirectory()
	if appErr != nil {
		t.Fatalf("runtime directory: %#v", appErr)
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			close(accepted)
			defer connection.Close()
			_, _ = io.Copy(io.Discard, connection)
		}
	}()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *appError, 1)
	go func() {
		_, appErr := openDaemonClient(ctx)
		done <- appErr
	}()
	select {
	case <-accepted:
	case <-time.After(time.Second):
		t.Fatal("test listener did not accept daemon handshake")
	}
	cancel()
	select {
	case appErr := <-done:
		if appErr == nil || appErr.code != "daemon_unavailable" {
			t.Fatalf("handshake cancellation error = %#v", appErr)
		}
	case <-time.After(time.Second):
		t.Fatal("stalled daemon handshake ignored canceled context")
	}
}

func TestDaemonShutdownClosesHeldPeerConnections(t *testing.T) {
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	d := startTestDaemon(t)
	_, socket, _, appErr := runtimePaths()
	if appErr != nil {
		t.Fatalf("runtime paths: %#v", appErr)
	}
	preHello, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer preHello.Close()
	postHello, err := net.Dial("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	defer postHello.Close()
	if err := json.NewEncoder(postHello).Encode(daemonHello{Type: "hello", Protocol: daemonProtocol, Version: "test"}); err != nil {
		t.Fatal(err)
	}
	var reply daemonHelloReply
	if err := json.NewDecoder(postHello).Decode(&reply); err != nil || reply.Type != "hello_ok" {
		t.Fatalf("post-hello reply = %#v error=%v", reply, err)
	}
	waitForDaemonConnections(t, d, 2)

	done := make(chan struct{})
	go func() {
		d.close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("daemon shutdown blocked on a held peer connection")
	}
}

func TestForegroundDaemonCancellationClosesRetainedChildAndSocket(t *testing.T) {
	t.Setenv("GO_WIRECMD_HELPER", "1")
	runtime := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	pidFile := filepath.Join(t.TempDir(), "helper.pid")
	configPath := helperConfig(t, "", "env WIRECMD_CHILD_PID_FILE="+strconv.Quote(pidFile))

	ctx, cancel := context.WithCancel(context.Background())
	readiness, daemonOutput := io.Pipe()
	done := make(chan int, 1)
	go func() {
		done <- Run(ctx, []string{"daemon", "run"}, strings.NewReader(""), daemonOutput, io.Discard)
		_ = daemonOutput.Close()
	}()
	line, err := bufio.NewReader(readiness).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	ready := decodeOutput(t, line)
	if ready["ok"] != true || ready["daemon"].(map[string]any)["status"] != "running" {
		t.Fatalf("daemon readiness = %s", line)
	}
	if code, output, stderr := invoke(t, []string{"--config", configPath, "helper"}); code != exitOK || stderr != "" || !strings.Contains(output, `"tools"`) {
		t.Fatalf("retained helper start: code=%d stderr=%q output=%s", code, stderr, output)
	}
	childPID := waitForPIDFile(t, pidFile)
	_, socket, _, appErr := runtimePaths()
	if appErr != nil {
		t.Fatalf("runtime paths: %#v", appErr)
	}

	cancel() // The main command wires SIGTERM through this same context path.
	select {
	case code := <-done:
		if code != exitOK {
			t.Fatalf("foreground daemon exit code=%d", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("foreground daemon did not exit after cancellation")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if err := syscall.Kill(childPID, 0); err == syscall.ESRCH {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := syscall.Kill(childPID, 0); err != syscall.ESRCH {
		t.Fatalf("retained child %d survived foreground daemon shutdown: %v", childPID, err)
	}
	if _, err := os.Lstat(socket); !os.IsNotExist(err) {
		t.Fatalf("daemon socket remains after shutdown: %v", err)
	}
}

func startTestDaemon(t *testing.T) *daemon {
	t.Helper()
	var stderr bytes.Buffer
	d, appErr := newDaemon(&stderr)
	if appErr != nil {
		t.Fatalf("new daemon: %#v", appErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- d.serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		d.close()
		select {
		case err := <-done:
			if err != nil && err != context.Canceled {
				t.Errorf("daemon serve: %v; stderr=%s", err, stderr.String())
			}
		case <-time.After(2 * time.Second):
			t.Error("daemon did not stop")
		}
	})
	return d
}

func helperSource(extra string) string {
	return "wirecmd {\nmcp \"helper\" {\nscope \"workspace\"\nstdio " + strconv.Quote(os.Args[0]) + " {\narg \"-test.run=TestHelperProcess\"\narg \"--\"\n" + extra + "\n}\n}\n}\n"
}

func helperSourceWithRoot(root string) string {
	return "wirecmd {\nroot " + strconv.Quote(root) + "\nmcp \"helper\" {\nscope \"workspace\"\nstdio " + strconv.Quote(os.Args[0]) + " {\narg \"-test.run=TestHelperProcess\"\narg \"--\"\n}\n}\n}\n"
}

func writeSource(t *testing.T, path, source string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
}

func callValue(t *testing.T, output string) string {
	t.Helper()
	return decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["value"].(string)
}

func callCWD(t *testing.T, output string) string {
	t.Helper()
	return decodeOutput(t, output)["result"].(map[string]any)["data"].(map[string]any)["cwd"].(string)
}

func waitForDaemonConnections(t *testing.T, d *daemon, minimum int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		count := len(d.connections)
		d.mu.Unlock()
		if count >= minimum {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("daemon accepted fewer than %d held connections", minimum)
}

func waitForPIDFile(t *testing.T, path string) int {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if content, err := os.ReadFile(path); err == nil {
			pid, parseErr := strconv.Atoi(string(content))
			if parseErr != nil {
				t.Fatalf("parse child pid: %v", parseErr)
			}
			return pid
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for retained child PID")
	return 0
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", path)
}
