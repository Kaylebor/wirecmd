package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/oauthstore"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	keyring "github.com/zalando/go-keyring"
)

type testKeyring struct {
	value string
	err   error
}

func (k *testKeyring) Get(string, string) (string, error) {
	if k.err != nil {
		return "", k.err
	}
	if k.value == "" {
		return "", keyring.ErrNotFound
	}
	return k.value, nil
}
func (k *testKeyring) Set(_, _, value string) error {
	if k.err != nil {
		return k.err
	}
	k.value = value
	return nil
}
func (k *testKeyring) Delete(string, string) error { k.value = ""; return nil }

func useTestOAuthStore(t *testing.T, keyring oauthstore.Keyring) {
	t.Helper()
	previous := newOAuthStore
	stateDir := filepath.Join(t.TempDir(), "state")
	newOAuthStore = func() (*oauthstore.Store, error) {
		return oauthstore.Open(oauthstore.Options{StateDir: stateDir, Keyring: keyring})
	}
	t.Cleanup(func() { newOAuthStore = previous })
}

func TestParseAuthAdmin(t *testing.T) {
	tests := []struct {
		args    []string
		ok      bool
		command string
		server  string
		wantErr string
	}{
		{args: []string{"auth", "login", "remote"}, ok: true, command: "login", server: "remote"},
		{args: []string{"auth", "status", "remote"}, ok: true, command: "status", server: "remote"},
		{args: []string{"auth", "logout", "remote"}, ok: true, command: "logout", server: "remote"},
		{args: []string{"auth", "tool"}, ok: true, wantErr: "auth_admin_arity"},
	}
	for _, test := range tests {
		admin, ok := parseAuthAdmin(test.args, options{})
		if ok != test.ok || admin.command != test.command || admin.server != test.server {
			t.Fatalf("parseAuthAdmin(%v) = %#v, %v", test.args, admin, ok)
		}
		if test.wantErr != "" && (admin.err == nil || admin.err.code != test.wantErr) {
			t.Fatalf("parseAuthAdmin(%v) error = %#v", test.args, admin.err)
		}
	}
	if _, ok := parseAuthAdmin([]string{"auth", "tool"}, options{jsonSet: true}); ok {
		t.Fatal("--json must preserve a server named auth")
	}
}

func TestDirectAuthStatusAndLogoutDoNotContactServer(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	endpoint := "http://127.0.0.1:1/mcp"
	path := writeConfig(t, `wirecmd { server "remote" { scope "workspace"; http "`+endpoint+`" } }`)
	for _, command := range []string{"status", "logout"} {
		code, output, stderr := invoke(t, []string{"--direct", "--config", path, "auth", command, "remote"})
		if code != 0 || stderr != "" {
			t.Fatalf("auth %s: code=%d stderr=%q output=%s", command, code, stderr, output)
		}
		if !strings.Contains(output, `"server":"remote"`) {
			t.Fatalf("auth %s output=%s", command, output)
		}
	}
}

func TestNoninteractiveProtectedHTTPReturnsAuthorizationRequired(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+"http://"+request.Host+`/.well-known/oauth-protected-resource"`)
		writer.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	path := writeConfig(t, `wirecmd { server "remote" { scope "workspace"; http "`+server.URL+`" } }`)
	code, output, _ := invoke(t, []string{"--direct", "--config", path, "remote"})
	if code != exitAuthentication || !strings.Contains(output, `"code":"authorization_required"`) || !strings.Contains(output, `wirecmd auth login`) {
		t.Fatalf("protected HTTP: code=%d output=%s", code, output)
	}
}

func TestUnavailableKeyringDoesNotBlockPublicHTTP(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{err: errors.New("no secret service")})
	fixture := newHTTPFixture(t)
	path := writeConfig(t, `wirecmd { server "remote" { scope "workspace"; http "`+fixture.Server.URL+`" } }`)
	code, output, stderr := invoke(t, []string{"--direct", "--config", path, "remote"})
	if code != 0 {
		t.Fatalf("public HTTP: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestSDKOAuthDCRPersistsAndReusesToken(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	upstream := newHTTPFixture(t)
	upstreamURL, _ := url.Parse(upstream.Server.URL)
	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	var baseURL string
	var authorizedRequests atomic.Int32
	var tokenRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/.well-known/oauth-protected-resource":
			writeTestJSON(writer, map[string]any{"resource": baseURL + "/mcp", "authorization_servers": []string{baseURL}})
		case "/.well-known/oauth-authorization-server":
			writeTestJSON(writer, map[string]any{"issuer": baseURL, "authorization_endpoint": baseURL + "/authorize", "token_endpoint": baseURL + "/token", "registration_endpoint": baseURL + "/register", "code_challenge_methods_supported": []string{"S256"}, "response_types_supported": []string{"code"}, "grant_types_supported": []string{"authorization_code", "refresh_token"}, "token_endpoint_auth_methods_supported": []string{"none"}, "authorization_response_iss_parameter_supported": true})
		case "/register":
			writeTestJSON(writer, map[string]any{"client_id": "wirecmd-test", "token_endpoint_auth_method": "none"})
		case "/authorize":
			redirect := request.URL.Query().Get("redirect_uri")
			state := request.URL.Query().Get("state")
			http.Redirect(writer, request, redirect+"?code=test-code&state="+url.QueryEscape(state)+"&iss="+url.QueryEscape(baseURL), http.StatusFound)
		case "/token":
			tokenRequests.Add(1)
			writeTestJSON(writer, map[string]any{"access_token": "oauth-access", "token_type": "Bearer", "refresh_token": "oauth-refresh", "expires_in": 3600})
		case "/mcp":
			if request.Header.Get("Authorization") != "Bearer oauth-access" {
				writer.Header().Set("WWW-Authenticate", `Bearer resource_metadata="`+baseURL+`/.well-known/oauth-protected-resource"`)
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			authorizedRequests.Add(1)
			proxy.ServeHTTP(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()
	baseURL = server.URL
	httpConfig := config.HTTP{Endpoint: baseURL + "/mcp"}
	target, _, appErr := makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
	if appErr != nil {
		t.Fatal(appErr)
	}
	target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, true, false, func(raw string) {
		go func() {
			response, err := http.Get(raw)
			if err == nil {
				_ = response.Body.Close()
			}
		}()
	})
	if appErr != nil {
		t.Fatal(appErr)
	}
	redactor := newRedactor(nil, io.Discard)
	tools, appErr := directTools(context.Background(), target, redactor)
	target.oauthRun.Close()
	if appErr != nil || len(tools) == 0 || authorizedRequests.Load() == 0 {
		t.Fatalf("tools=%#v error=%#v authorized=%d", tools, appErr, authorizedRequests.Load())
	}
	identity, _ := oauthCredentialIdentity(httpConfig, baseURL+"/mcp", "")
	store, _ := newOAuthStore()
	record, found, err := store.Load(identity)
	if err != nil || !found {
		t.Fatalf("load persisted OAuth record: found=%v err=%v", found, err)
	}
	var persisted oauthRecordPayload
	if err := json.Unmarshal(record.Payload, &persisted); err != nil {
		t.Fatal(err)
	}
	persisted.Token.Expiry = time.Now().Add(-time.Hour)
	record.Payload, _ = json.Marshal(persisted)
	if _, err := store.Save(identity, record); err != nil {
		t.Fatal(err)
	}
	beforeRefresh := tokenRequests.Load()

	// A new runtime refreshes the encrypted token without another browser event.
	target, _, _ = makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
	events := 0
	target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, false, false, func(string) { events++ })
	if appErr != nil {
		t.Fatal(appErr)
	}
	tools, appErr = directTools(context.Background(), target, redactor)
	target.oauthRun.Close()
	if appErr != nil || len(tools) == 0 || events != 0 || tokenRequests.Load() <= beforeRefresh {
		t.Fatalf("restored tools=%#v error=%#v events=%d token_requests=%d", tools, appErr, events, tokenRequests.Load())
	}
}

func writeTestJSON(writer http.ResponseWriter, value any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(value)
}

func TestDaemonClientConsumesAuthorizationEvent(t *testing.T) {
	clientSide, serverSide := net.Pipe()
	defer clientSide.Close()
	defer serverSide.Close()
	seen := ""
	client := &daemonClient{connection: clientSide, decoder: newJSONDecoder(clientSide), encoder: newJSONEncoder(clientSide), stopCancel: func() bool { return true }, event: func(raw string) { seen = raw }}
	go func() {
		decoder := newJSONDecoder(serverSide)
		var request daemonRequest
		_ = decoder.Decode(&request)
		encoder := newJSONEncoder(serverSide)
		_ = encoder.Encode(daemonReply{Type: "authorization_url", URL: "https://example.test/authorize"})
		_ = encoder.Encode(resultReply(map[string]any{"ok": true}, nil))
	}()
	reply, appErr := client.request(daemonRequest{Auth: "login"})
	if appErr != nil || seen != "https://example.test/authorize" || len(reply.Result) == 0 {
		t.Fatalf("event=%q reply=%#v err=%#v", seen, reply, appErr)
	}
}

func TestAuthorizationURLDiagnosticsRedactEncodedSecrets(t *testing.T) {
	previous := openAuthorizationURL
	opened := ""
	openAuthorizationURL = func(raw string) error { opened = raw; return nil }
	t.Cleanup(func() { openAuthorizationURL = previous })
	secret := "a/b c"
	raw := "https://login.example/authorize?resource=" + url.QueryEscape("https://mcp.example/mcp?token="+secret)
	var stderr strings.Builder
	browserEventHandler(&stderr, newRedactor([]string{secret}, io.Discard))(raw)
	if opened != raw {
		t.Fatal("browser did not receive the exact authorization URL")
	}
	if strings.Contains(stderr.String(), secret) || strings.Contains(stderr.String(), url.QueryEscape(secret)) || strings.Contains(stderr.String(), url.PathEscape(secret)) {
		t.Fatalf("authorization diagnostics leaked secret: %q", stderr.String())
	}
}

func TestDaemonOAuthFlowLockPrecedesFixedCallbackBind(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://" + probe.Addr().String() + "/callback"
	_ = probe.Close()
	httpConfig := config.HTTP{Endpoint: "https://mcp.example/mcp", OAuth: &config.OAuth{ClientID: "client", RedirectURI: redirect}}
	d := &daemon{authFlows: map[string]struct{}{}}
	first, appErr := newOAuthRuntime(httpConfig, httpConfig.Endpoint, "", true, true, func(string) {})
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer first.Close()
	second, appErr := newOAuthRuntime(httpConfig, httpConfig.Endpoint, "", true, true, func(string) {})
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer second.Close()
	for _, runtime := range []*oauthRuntime{first, second} {
		runtime.onFlowStart = func() error { return d.beginOAuthFlow(runtime) }
		runtime.onFlowEnd = func() { d.endOAuthFlow(runtime) }
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := first.fetchAuthorizationCode(ctx, &mcpauth.AuthorizationArgs{URL: "https://login.example/authorize"})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		d.mu.Lock()
		active := len(d.authFlows)
		d.mu.Unlock()
		if active == 1 {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if _, err := second.fetchAuthorizationCode(context.Background(), &mcpauth.AuthorizationArgs{URL: "https://login.example/authorize"}); !errors.Is(err, errAuthorizationInProgress) {
		t.Fatalf("second authorization error = %v", err)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("first authorization did not cancel")
	}
}

func TestRetireOAuthCredentialLeavesUnrelatedAliasAlive(t *testing.T) {
	d := &daemon{pools: map[string]*poolEntry{}, retiring: map[*retainedInstance]struct{}{}, stderr: io.Discard}
	first := &retainedInstance{active: 1, oauthRun: &oauthRuntime{identity: []byte("first")}}
	second := &retainedInstance{active: 1, oauthRun: &oauthRuntime{identity: []byte("second")}}
	d.pools["remote\x00workspace-one"] = &poolEntry{instance: first}
	d.pools["remote\x00workspace-two"] = &poolEntry{instance: second}
	d.retireOAuthCredential(first.oauthRun.flowKey())
	if !first.retiring || second.retiring {
		t.Fatalf("retirement first=%v second=%v", first.retiring, second.retiring)
	}
	if _, ok := d.pools["remote\x00workspace-two"]; !ok {
		t.Fatal("unrelated same-alias pool was removed")
	}
}

func newJSONDecoder(reader io.Reader) *json.Decoder {
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	return decoder
}
func newJSONEncoder(writer io.Writer) *json.Encoder { return json.NewEncoder(writer) }
