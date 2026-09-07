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
	"os"
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
	value   string
	err     error
	gets    atomic.Int32
	sets    atomic.Int32
	deletes atomic.Int32
}

func (k *testKeyring) Get(string, string) (string, error) {
	k.gets.Add(1)
	if k.err != nil {
		return "", k.err
	}
	if k.value == "" {
		return "", keyring.ErrNotFound
	}
	return k.value, nil
}
func (k *testKeyring) Set(_, _, value string) error {
	k.sets.Add(1)
	if k.err != nil {
		return k.err
	}
	k.value = value
	return nil
}
func (k *testKeyring) Delete(string, string) error {
	k.deletes.Add(1)
	k.value = ""
	return nil
}

func useTestOAuthStore(t *testing.T, keyring oauthstore.Keyring) string {
	t.Helper()
	previous := newOAuthStore
	stateDir := filepath.Join(t.TempDir(), "state")
	newOAuthStore = func() (*oauthstore.Store, error) {
		return oauthstore.Open(oauthstore.Options{StateDir: stateDir, Keyring: keyring})
	}
	t.Cleanup(func() { newOAuthStore = previous })
	return stateDir
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
	path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+endpoint+`" } }`)
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
	path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+server.URL+`" } }`)
	code, output, _ := invoke(t, []string{"--direct", "--config", path, "remote"})
	if code != exitAuthentication || !strings.Contains(output, `"code":"authorization_required"`) || !strings.Contains(output, `wirecmd auth login`) {
		t.Fatalf("protected HTTP: code=%d output=%s", code, output)
	}
}

func TestUnavailableKeyringDoesNotBlockPublicHTTP(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{err: errors.New("no secret service")})
	fixture := newHTTPFixture(t)
	path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.Server.URL+`" } }`)
	code, output, stderr := invoke(t, []string{"--direct", "--config", path, "remote"})
	if code != 0 {
		t.Fatalf("public HTTP: code=%d stderr=%q output=%s", code, stderr, output)
	}
}

func TestUnavailableKeyringOnProtectedHTTPRequiresUserAction(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{err: errors.New("no secret service")})
	fixture := newOAuthFixture(t, oauthFixtureOptions{})
	path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.Server.URL+`/mcp" } }`)
	code, output, _ := invoke(t, []string{"--direct", "--config", path, "remote"})
	if code != exitUserAction || !strings.Contains(output, `"code":"credential_store_unavailable"`) {
		t.Fatalf("protected keyring failure: code=%d output=%s", code, output)
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

func TestOAuthFixtureDCRValidatesSDKRequests(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	fixture := newOAuthFixture(t, oauthFixtureOptions{IssuerInCallback: true})
	httpConfig := config.HTTP{Endpoint: fixture.Server.URL + "/mcp"}
	target, _, appErr := makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
	if appErr != nil {
		t.Fatal(appErr)
	}
	target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, true, false, completeFixtureAuthorization)
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer target.oauthRun.Close()
	tools, appErr := directTools(context.Background(), target, newRedactor(nil, io.Discard))
	if appErr != nil || len(tools) == 0 || fixture.authorizedMCP.Load() == 0 || fixture.registrations.Load() != 1 {
		t.Fatalf("tools=%#v error=%#v authorized=%d registrations=%d", tools, appErr, fixture.authorizedMCP.Load(), fixture.registrations.Load())
	}
	registered, pkce, resource, redirect, client, secret := fixture.assertions()
	if !registered || !pkce || !resource || !redirect || !client || !secret {
		t.Fatalf("fixture assertions registration=%v pkce=%v resource=%v redirect=%v client=%v secret=%v", registered, pkce, resource, redirect, client, secret)
	}
}

func TestOAuthFixturePreregisteredClients(t *testing.T) {
	for _, test := range []struct {
		name   string
		secret string
	}{
		{name: "public"},
		{name: "confidential", secret: "fixture-client-secret"},
	} {
		t.Run(test.name, func(t *testing.T) {
			useTestOAuthStore(t, &testKeyring{})
			fixture := newOAuthFixture(t, oauthFixtureOptions{ClientID: "fixture-client", ClientSecret: test.secret, IssuerInCallback: true})
			probe, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			redirect := "http://" + probe.Addr().String() + "/callback"
			_ = probe.Close()
			httpConfig := config.HTTP{Endpoint: fixture.Server.URL + "/mcp", OAuth: &config.OAuth{ClientID: "fixture-client", RedirectURI: redirect}}
			if test.secret != "" {
				httpConfig.OAuth.ClientSecret = &config.Value{Kind: config.ValueSecretReference, Text: "env://FIXTURE_CLIENT_SECRET"}
			}
			lookup := func(name string) (string, bool) { return test.secret, name == "FIXTURE_CLIENT_SECRET" }
			target, _, appErr := makeHTTPTarget(httpConfig, lookup)
			if appErr != nil {
				t.Fatal(appErr)
			}
			target, _, appErr = attachOAuth(target, "remote", httpConfig, lookup, true, false, completeFixtureAuthorization)
			if appErr != nil {
				t.Fatal(appErr)
			}
			defer target.oauthRun.Close()
			tools, appErr := directTools(context.Background(), target, newRedactor([]string{test.secret}, io.Discard))
			if appErr != nil || len(tools) == 0 || fixture.registrations.Load() != 0 {
				t.Fatalf("tools=%#v error=%#v registrations=%d", tools, appErr, fixture.registrations.Load())
			}
			_, pkce, resource, redirectOK, client, secret := fixture.assertions()
			if !pkce || !resource || !redirectOK || !client || !secret {
				t.Fatalf("fixture assertions pkce=%v resource=%v redirect=%v client=%v secret=%v", pkce, resource, redirectOK, client, secret)
			}
		})
	}
}

func TestOAuthFixtureProviderDenialAndRefreshFailureMapToAuthentication(t *testing.T) {
	t.Run("provider denial", func(t *testing.T) {
		useTestOAuthStore(t, &testKeyring{})
		fixture := newOAuthFixture(t, oauthFixtureOptions{Deny: true, IssuerInCallback: true})
		httpConfig := config.HTTP{Endpoint: fixture.Server.URL + "/mcp"}
		target, _, appErr := makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
		if appErr != nil {
			t.Fatal(appErr)
		}
		target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, true, false, completeFixtureAuthorization)
		if appErr != nil {
			t.Fatal(appErr)
		}
		defer target.oauthRun.Close()
		_, appErr = directTools(context.Background(), target, newRedactor(nil, io.Discard))
		if appErr == nil || appErr.category != "authentication" || appErr.code != "authorization_failed" {
			t.Fatalf("denial error=%#v", appErr)
		}
	})

	t.Run("refresh invalid grant", func(t *testing.T) {
		useTestOAuthStore(t, &testKeyring{})
		fixture := newOAuthFixture(t, oauthFixtureOptions{IssuerInCallback: true})
		httpConfig := config.HTTP{Endpoint: fixture.Server.URL + "/mcp"}
		target, _, appErr := makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
		if appErr != nil {
			t.Fatal(appErr)
		}
		target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, true, false, completeFixtureAuthorization)
		if appErr != nil {
			t.Fatal(appErr)
		}
		if _, appErr := directTools(context.Background(), target, newRedactor(nil, io.Discard)); appErr != nil {
			t.Fatal(appErr)
		}
		target.oauthRun.Close()
		identity, err := oauthCredentialIdentity(httpConfig, fixture.Server.URL+"/mcp", "")
		if err != nil {
			t.Fatal(err)
		}
		store, err := newOAuthStore()
		if err != nil {
			t.Fatal(err)
		}
		record, found, err := store.Load(identity)
		if err != nil || !found {
			t.Fatalf("load record found=%v err=%v", found, err)
		}
		var payload oauthRecordPayload
		if err := json.Unmarshal(record.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		payload.Token.Expiry = time.Now().Add(-time.Hour)
		record.Payload, _ = json.Marshal(payload)
		if _, err := store.Save(identity, record); err != nil {
			t.Fatal(err)
		}
		fixture.options.RefreshFails = true
		target, _, appErr = makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
		if appErr != nil {
			t.Fatal(appErr)
		}
		target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, false, false, nil)
		if appErr != nil {
			t.Fatal(appErr)
		}
		defer target.oauthRun.Close()
		_, appErr = directTools(context.Background(), target, newRedactor(nil, io.Discard))
		if appErr == nil || appErr.category != "authentication" || appErr.code != "authorization_failed" || fixture.refreshRequests.Load() == 0 {
			t.Fatalf("refresh error=%#v refreshes=%d", appErr, fixture.refreshRequests.Load())
		}
	})
}

func TestOAuthFixtureLegacyMetadataFallback(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	fixture := newOAuthFixture(t, oauthFixtureOptions{LegacyASMetadata: true})
	httpConfig := config.HTTP{Endpoint: fixture.Server.URL + "/mcp"}
	target, _, appErr := makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
	if appErr != nil {
		t.Fatal(appErr)
	}
	target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, true, false, completeFixtureAuthorization)
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer target.oauthRun.Close()
	tools, appErr := directTools(context.Background(), target, newRedactor(nil, io.Discard))
	if appErr != nil || len(tools) == 0 || fixture.authorizationMeta.Load() == 0 {
		t.Fatalf("tools=%#v error=%#v metadata=%d", tools, appErr, fixture.authorizationMeta.Load())
	}
}

func TestOAuthFixtureOriginAndLegacyFallbackWithoutMetadataHint(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	fixture := newOAuthFixture(t, oauthFixtureOptions{OmitMetadataHint: true, PRMNotFound: true, LegacyASMetadata: true})
	httpConfig := config.HTTP{Endpoint: fixture.Server.URL + "/mcp"}
	target, _, appErr := makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
	if appErr != nil {
		t.Fatal(appErr)
	}
	target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, true, false, completeFixtureAuthorization)
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer target.oauthRun.Close()
	tools, appErr := directTools(context.Background(), target, newRedactor(nil, io.Discard))
	if appErr != nil || len(tools) == 0 || fixture.protectedMetadata.Load() < 2 || fixture.authorizationMeta.Load() == 0 {
		t.Fatalf("tools=%#v error=%#v prm=%d metadata=%d", tools, appErr, fixture.protectedMetadata.Load(), fixture.authorizationMeta.Load())
	}
}

func TestOAuthFixtureStateAndIssuerFailuresMapToProtocolError(t *testing.T) {
	for _, test := range []struct {
		name    string
		options oauthFixtureOptions
	}{
		{name: "wrong state", options: oauthFixtureOptions{StateMismatch: true}},
		{name: "missing required issuer", options: oauthFixtureOptions{RequireIssuer: true}},
		{name: "mismatched issuer", options: oauthFixtureOptions{RequireIssuer: true, IssuerInCallback: true, CallbackIssuer: "https://wrong-issuer.example"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			useTestOAuthStore(t, &testKeyring{})
			fixture := newOAuthFixture(t, test.options)
			httpConfig := config.HTTP{Endpoint: fixture.Server.URL + "/mcp"}
			target, _, appErr := makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
			if appErr != nil {
				t.Fatal(appErr)
			}
			target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, true, false, completeFixtureAuthorization)
			if appErr != nil {
				t.Fatal(appErr)
			}
			defer target.oauthRun.Close()
			_, appErr = directTools(context.Background(), target, newRedactor(nil, io.Discard))
			if appErr == nil || appErr.category != "upstream_protocol" || appErr.code != "oauth_protocol_failed" {
				t.Fatalf("state/issuer error=%#v", appErr)
			}
		})
	}
}

func TestOAuthRuntimeStoreContentionPrecedesCallbackBinding(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://" + probe.Addr().String() + "/callback"
	_ = probe.Close()
	httpConfig := config.HTTP{Endpoint: "https://mcp.example/mcp", OAuth: &config.OAuth{ClientID: "fixture-client", RedirectURI: redirect}}
	first, appErr := newOAuthRuntime(httpConfig, httpConfig.Endpoint, "", true, true, nil)
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer first.Close()
	second, appErr := newOAuthRuntime(httpConfig, httpConfig.Endpoint, "", true, true, nil)
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer second.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := first.fetchAuthorizationCode(ctx, &mcpauth.AuthorizationArgs{URL: "https://login.example/authorize"})
		done <- err
	}()
	deadline := time.Now().Add(time.Second)
	firstStarted := false
	for time.Now().Before(deadline) {
		first.mu.Lock()
		firstStarted = first.flowActive && first.serverStarted
		first.mu.Unlock()
		if firstStarted {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !firstStarted {
		cancel()
		t.Fatal("first runtime did not acquire authorization lock and bind callback")
	}
	if _, err := second.fetchAuthorizationCode(context.Background(), &mcpauth.AuthorizationArgs{URL: "https://login.example/authorize"}); !errors.Is(err, errAuthorizationInProgress) {
		cancel()
		t.Fatalf("second runtime error=%v, want authorization_in_progress", err)
	}
	second.mu.Lock()
	secondBound := second.serverStarted || second.listener != nil
	second.mu.Unlock()
	if secondBound {
		cancel()
		t.Fatal("contending runtime bound a callback listener before failing")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("first runtime error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("first runtime did not stop after cancellation")
	}
}

func TestStaticAuthorizationAndServerListingDoNotUseKeyring(t *testing.T) {
	for _, test := range []struct {
		name   string
		config string
		args   []string
	}{
		{name: "static authorization", config: ` { header Authorization="Bearer fixture-static" }`, args: []string{"remote"}},
		{name: "server listing", config: ``, args: nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			keyring := &testKeyring{err: errors.New("keyring must not be used")}
			useTestOAuthStore(t, keyring)
			fixture := newHTTPFixture(t)
			path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.Server.URL+`"`+test.config+` } }`)
			args := append([]string{"--direct", "--config", path}, test.args...)
			code, output, stderr := invoke(t, args)
			if code != exitOK {
				t.Fatalf("code=%d output=%q stderr=%q", code, output, stderr)
			}
			if keyring.gets.Load() != 0 || keyring.sets.Load() != 0 || keyring.deletes.Load() != 0 {
				t.Fatalf("keyring accesses get=%d set=%d delete=%d", keyring.gets.Load(), keyring.sets.Load(), keyring.deletes.Load())
			}
		})
	}
}

func TestOAuthFixtureMalformedMetadataMapsToProtocolFailure(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	fixture := newOAuthFixture(t, oauthFixtureOptions{MalformedPRM: true})
	httpConfig := config.HTTP{Endpoint: fixture.Server.URL + "/mcp"}
	target, _, appErr := makeHTTPTarget(httpConfig, func(string) (string, bool) { return "", false })
	if appErr != nil {
		t.Fatal(appErr)
	}
	target, _, appErr = attachOAuth(target, "remote", httpConfig, func(string) (string, bool) { return "", false }, true, false, completeFixtureAuthorization)
	if appErr != nil {
		t.Fatal(appErr)
	}
	defer target.oauthRun.Close()
	_, appErr = directTools(context.Background(), target, newRedactor(nil, io.Discard))
	if appErr == nil || appErr.category != "upstream_protocol" || appErr.code != "oauth_protocol_failed" {
		t.Fatalf("malformed metadata error=%#v", appErr)
	}
}

func TestOAuthCallbackRejectsBadRequestsAndClosesOnCancellation(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://" + probe.Addr().String() + "/callback"
	_ = probe.Close()
	httpConfig := config.HTTP{Endpoint: "https://mcp.example/mcp", OAuth: &config.OAuth{ClientID: "fixture-client", RedirectURI: redirect}}
	runtime, appErr := newOAuthRuntime(httpConfig, httpConfig.Endpoint, "", true, true, nil)
	if appErr != nil {
		t.Fatal(appErr)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := runtime.fetchAuthorizationCode(ctx, &mcpauth.AuthorizationArgs{URL: "https://login.example/authorize"})
		done <- err
	}()
	var callback string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		runtime.mu.Lock()
		started, raw := runtime.serverStarted, runtime.redirectURL
		runtime.mu.Unlock()
		if started {
			callback = raw
			break
		}
		time.Sleep(time.Millisecond)
	}
	if callback == "" {
		runtime.Close()
		t.Fatal("callback listener did not start")
	}
	for _, test := range []struct {
		name   string
		method string
		url    string
		status int
	}{
		{name: "wrong method", method: http.MethodPost, url: callback, status: http.StatusNotFound},
		{name: "wrong path", method: http.MethodGet, url: strings.TrimSuffix(callback, "/callback") + "/wrong", status: http.StatusNotFound},
		{name: "missing callback values", method: http.MethodGet, url: callback, status: http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := http.NewRequest(test.method, test.url, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			_ = response.Body.Close()
			if response.StatusCode != test.status {
				t.Fatalf("status=%d, want %d", response.StatusCode, test.status)
			}
		})
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("fetch error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("authorization fetch did not observe cancellation")
	}
	runtime.Close()
	request, err := http.NewRequest(http.MethodGet, callback, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := http.DefaultClient.Do(request); err == nil {
		t.Fatal("callback listener remained reachable after Close")
	}
}

func TestOAuthFixtureRunDoesNotExposeCredentialValues(t *testing.T) {
	stateDir := useTestOAuthStore(t, &testKeyring{})
	secret := "fixture-client-secret"
	fixture := newOAuthFixture(t, oauthFixtureOptions{ClientID: "fixture-client", ClientSecret: secret, IssuerInCallback: true})
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	redirect := "http://" + probe.Addr().String() + "/callback"
	_ = probe.Close()
	path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.Server.URL+`/mcp" { oauth { client-id "fixture-client"; client-secret (secret)"env://WIRECMD_FIXTURE_CLIENT_SECRET"; redirect-uri "`+redirect+`" } } } }`)
	t.Setenv("WIRECMD_FIXTURE_CLIENT_SECRET", secret)
	previousTerminal, previousOpen := isInteractiveTerminal, openAuthorizationURL
	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	openAuthorizationURL = func(raw string) error { completeFixtureAuthorization(raw); return nil }
	t.Cleanup(func() {
		isInteractiveTerminal, openAuthorizationURL = previousTerminal, previousOpen
	})
	code, stdout, stderr := invoke(t, []string{"--direct", "--config", path, "remote"})
	if code != exitOK {
		t.Fatalf("Run code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}
	issuedCode, issuedState := fixture.sensitiveValues()
	if issuedCode == "" || issuedState == "" {
		t.Fatal("fixture did not capture the exercised authorization code and state")
	}
	for _, value := range []string{secret, "fixture-access-token", "fixture-refresh-token", issuedCode} {
		if strings.Contains(stdout, value) || strings.Contains(stderr, value) {
			t.Fatalf("public output exposed a credential value")
		}
	}
	if strings.Contains(stdout, issuedState) {
		t.Fatal("stdout exposed the OAuth state")
	}
	err = filepath.WalkDir(stateDir, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, value := range []string{secret, "fixture-access-token", "fixture-refresh-token", issuedCode, issuedState, fixture.Server.URL, "fixture-client"} {
			if strings.Contains(entry.Name(), value) || strings.Contains(string(contents), value) {
				t.Fatalf("encrypted state file exposed a credential value")
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOAuthFixtureExplicitLoginReusesRegistrationAndReplacesCredential(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	fixture := newOAuthFixture(t, oauthFixtureOptions{IssuerInCallback: true})
	path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.Server.URL+`/mcp" } }`)
	previousTerminal, previousOpen := isInteractiveTerminal, openAuthorizationURL
	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	openAuthorizationURL = func(raw string) error { completeFixtureAuthorization(raw); return nil }
	t.Cleanup(func() {
		isInteractiveTerminal, openAuthorizationURL = previousTerminal, previousOpen
	})

	identity, err := oauthCredentialIdentity(config.HTTP{Endpoint: fixture.Server.URL + "/mcp"}, fixture.Server.URL+"/mcp", "")
	if err != nil {
		t.Fatal(err)
	}
	store, err := newOAuthStore()
	if err != nil {
		t.Fatal(err)
	}
	var firstGeneration string
	for attempt := 0; attempt < 2; attempt++ {
		code, stdout, _ := invoke(t, []string{"--direct", "--config", path, "auth", "login", "remote"})
		if code != exitOK || !strings.Contains(stdout, `"status":"authenticated"`) {
			t.Fatalf("login %d: code=%d stdout=%s", attempt+1, code, stdout)
		}
		record, found, err := store.Load(identity)
		if err != nil || !found {
			t.Fatalf("login %d record: found=%v err=%v", attempt+1, found, err)
		}
		if attempt == 0 {
			firstGeneration = record.Generation
		} else if record.Generation == firstGeneration {
			t.Fatal("explicit login did not replace the credential generation")
		}
	}
	if got := fixture.registrations.Load(); got != 1 {
		t.Fatalf("dynamic registrations=%d, want one reused registration", got)
	}
	code, stdout, _ := invoke(t, []string{"--direct", "--config", path, "auth", "logout", "remote"})
	if code != exitOK || !strings.Contains(stdout, `"status":"logged_out"`) {
		t.Fatalf("logout: code=%d stdout=%s", code, stdout)
	}
	code, stdout, _ = invoke(t, []string{"--direct", "--config", path, "auth", "status", "remote"})
	if code != exitOK || !strings.Contains(stdout, `"status":"unauthenticated"`) {
		t.Fatalf("status after logout: code=%d stdout=%s", code, stdout)
	}
}

func TestOAuthFixtureDaemonLogoutRetiresCredentialSession(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	runtimeDir := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	startTestDaemon(t)
	fixture := newOAuthFixture(t, oauthFixtureOptions{IssuerInCallback: true})
	path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.Server.URL+`/mcp" } }`)
	previousTerminal, previousOpen := isInteractiveTerminal, openAuthorizationURL
	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	openAuthorizationURL = func(raw string) error { completeFixtureAuthorization(raw); return nil }
	t.Cleanup(func() {
		isInteractiveTerminal, openAuthorizationURL = previousTerminal, previousOpen
	})

	code, stdout, stderr := invoke(t, []string{"--config", path, "auth", "login", "remote"})
	if code != exitOK || !strings.Contains(stdout, `"status":"authenticated"`) {
		t.Fatalf("daemon login: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = invoke(t, []string{"--config", path, "remote"})
	if code != exitOK {
		t.Fatalf("daemon checkout: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = invoke(t, []string{"--config", path, "auth", "logout", "remote"})
	if code != exitOK || !strings.Contains(stdout, `"status":"logged_out"`) {
		t.Fatalf("daemon logout: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	code, stdout, stderr = invoke(t, []string{"daemon", "status"})
	if code != exitOK {
		t.Fatalf("daemon status: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	status := decodeOutput(t, stdout)["daemon"].(map[string]any)
	if status["active_instances"].(json.Number).String() != "0" {
		t.Fatalf("active instances after logout = %s", status["active_instances"])
	}
	t.Setenv("WIRECMD_NONINTERACTIVE", "1")
	code, stdout, _ = invoke(t, []string{"--config", path, "remote"})
	if code != exitAuthentication || !strings.Contains(stdout, `"code":"authorization_required"`) {
		t.Fatalf("post-logout call: code=%d stdout=%s", code, stdout)
	}
}

func TestOAuthFixtureDaemonReusesAutomaticAuthorizationSession(t *testing.T) {
	useTestOAuthStore(t, &testKeyring{})
	runtimeDir := testRuntimeDirectory(t)
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
	startTestDaemon(t)
	fixture := newOAuthFixture(t, oauthFixtureOptions{IssuerInCallback: true})
	path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.Server.URL+`/mcp" } }`)
	previousTerminal, previousOpen := isInteractiveTerminal, openAuthorizationURL
	isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
	openAuthorizationURL = func(raw string) error { completeFixtureAuthorization(raw); return nil }
	t.Cleanup(func() {
		isInteractiveTerminal, openAuthorizationURL = previousTerminal, previousOpen
	})

	if code, stdout, stderr := invoke(t, []string{"--config", path, "remote"}); code != exitOK {
		t.Fatalf("initial automatic authorization: code=%d stdout=%s stderr=%s", code, stdout, stderr)
	}
	for attempt := 0; attempt < 32; attempt++ {
		code, stdout, stderr := invoke(t, []string{"--config", path, "remote"})
		if code != exitOK {
			t.Fatalf("reused automatic authorization %d: code=%d stdout=%s stderr=%s", attempt+1, code, stdout, stderr)
		}
	}
	if got := fixture.authorizations.Load(); got != 1 {
		t.Fatalf("authorizations=%d, want one", got)
	}
}

func TestDaemonOAuthStartupWaitDistinguishesActiveAndCompletedFlows(t *testing.T) {
	d := &daemon{}
	active := &poolEntry{ready: make(chan struct{}), authStarted: make(chan struct{})}
	close(active.authStarted)
	if _, appErr := d.waitForInstance(context.Background(), active, 0, false); appErr == nil || appErr.code != "authorization_in_progress" {
		t.Fatalf("active authorization error=%#v", appErr)
	}

	completed := &poolEntry{ready: make(chan struct{}), authStarted: make(chan struct{}), err: transportError("startup_failed", "fixture startup failure", "retry")}
	close(completed.authStarted)
	close(completed.ready)
	if _, appErr := d.waitForInstance(context.Background(), completed, 0, false); appErr == nil || appErr.code != "startup_failed" {
		t.Fatalf("completed authorization error=%#v", appErr)
	}
}

func TestOAuthFixtureDirectCredentialChangesRetireDaemonSession(t *testing.T) {
	for _, action := range []string{"logout", "login"} {
		t.Run(action, func(t *testing.T) {
			useTestOAuthStore(t, &testKeyring{})
			runtimeDir := testRuntimeDirectory(t)
			t.Setenv("XDG_RUNTIME_DIR", runtimeDir)
			startTestDaemon(t)
			fixture := newOAuthFixture(t, oauthFixtureOptions{IssuerInCallback: true})
			path := writeConfig(t, `wirecmd { mcp "remote" { scope "workspace"; http "`+fixture.Server.URL+`/mcp" } }`)
			previousTerminal, previousOpen := isInteractiveTerminal, openAuthorizationURL
			isInteractiveTerminal = func(io.Reader, io.Writer) bool { return true }
			openAuthorizationURL = func(raw string) error { completeFixtureAuthorization(raw); return nil }
			t.Cleanup(func() {
				isInteractiveTerminal, openAuthorizationURL = previousTerminal, previousOpen
			})

			if code, stdout, stderr := invoke(t, []string{"--config", path, "auth", "login", "remote"}); code != exitOK {
				t.Fatalf("daemon login: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if code, stdout, stderr := invoke(t, []string{"--config", path, "remote"}); code != exitOK {
				t.Fatalf("daemon checkout: code=%d stdout=%s stderr=%s", code, stdout, stderr)
			}
			if code, stdout, stderr := invoke(t, []string{"--direct", "--config", path, "auth", action, "remote"}); code != exitOK {
				t.Fatalf("direct %s: code=%d stdout=%s stderr=%s", action, code, stdout, stderr)
			}

			code, stdout, _ := invoke(t, []string{"--config", path, "remote"})
			if code != exitTransport || !strings.Contains(stdout, `"code":"instance_retired"`) {
				t.Fatalf("first daemon checkout after direct %s: code=%d stdout=%s", action, code, stdout)
			}
			if action == "logout" {
				t.Setenv("WIRECMD_NONINTERACTIVE", "1")
				code, stdout, _ = invoke(t, []string{"--config", path, "remote"})
				if code != exitAuthentication || !strings.Contains(stdout, `"code":"authorization_required"`) {
					t.Fatalf("second daemon checkout after direct logout: code=%d stdout=%s", code, stdout)
				}
			} else {
				code, stdout, stderr := invoke(t, []string{"--config", path, "remote"})
				if code != exitOK {
					t.Fatalf("second daemon checkout after direct replacement: code=%d stdout=%s stderr=%s", code, stdout, stderr)
				}
				if got := fixture.registrations.Load(); got != 1 {
					t.Fatalf("dynamic registrations=%d after replacement, want one", got)
				}
			}
		})
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
	flowActive := false
	for time.Now().Before(deadline) {
		d.mu.Lock()
		active := len(d.authFlows)
		d.mu.Unlock()
		if active == 1 {
			flowActive = true
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !flowActive {
		cancel()
		t.Fatal("first daemon authorization did not acquire the global flow lock")
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
