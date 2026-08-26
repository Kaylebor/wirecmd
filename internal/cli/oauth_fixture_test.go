package cli

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// oauthFixture is a deliberately small OAuth provider plus protected MCP
// resource. It validates what the SDK sends, but never logs or exposes secret
// values, authorization codes, states, or URLs that carry them. The current
// test's code and state are retained only long enough for secrecy assertions.
type oauthFixture struct {
	Server *httptest.Server

	upstream *httpFixture
	options  oauthFixtureOptions
	baseURL  string

	mu                 sync.Mutex
	codes              map[string]oauthFixtureCode
	nextCode           atomic.Int64
	registered         bool
	registeredRedirect string
	pkceOK             bool
	resourceOK         bool
	redirectOK         bool
	clientOK           bool
	secretOK           bool
	issuedCode         string
	issuedState        string

	protectedMetadata atomic.Int64
	authorizationMeta atomic.Int64
	registrations     atomic.Int64
	authorizations    atomic.Int64
	tokenRequests     atomic.Int64
	refreshRequests   atomic.Int64
	authorizedMCP     atomic.Int64
}

type oauthFixtureOptions struct {
	ClientID         string
	ClientSecret     string
	Deny             bool
	MalformedPRM     bool
	LegacyASMetadata bool
	RefreshFails     bool
	IssuerInCallback bool
	RequireIssuer    bool
	CallbackIssuer   string
	StateMismatch    bool
	OmitMetadataHint bool
	PRMNotFound      bool
}

type oauthFixtureCode struct {
	challenge string
	redirect  string
	clientID  string
}

func newOAuthFixture(t *testing.T, options oauthFixtureOptions) *oauthFixture {
	t.Helper()
	upstream := newHTTPFixture(t)
	upstreamURL, err := url.Parse(upstream.Server.URL)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &oauthFixture{upstream: upstream, options: options, codes: make(map[string]oauthFixtureCode)}
	proxy := httputil.NewSingleHostReverseProxy(upstreamURL)
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/mcp":
			fixture.handleMCP(proxy, writer, request)
		case "/.well-known/oauth-protected-resource":
			fixture.handlePRM(writer)
		case "/.well-known/oauth-protected-resource/mcp":
			fixture.handlePRM(writer)
		case "/.well-known/oauth-authorization-server":
			fixture.handleAuthorizationMetadata(writer)
		case "/register":
			fixture.handleRegister(writer, request)
		case "/authorize":
			fixture.handleAuthorize(writer, request)
		case "/token":
			fixture.handleToken(writer, request)
		default:
			http.NotFound(writer, request)
		}
	}))
	fixture.baseURL = fixture.Server.URL
	t.Cleanup(fixture.Server.Close)
	return fixture
}

func (f *oauthFixture) handleMCP(proxy *httputil.ReverseProxy, writer http.ResponseWriter, request *http.Request) {
	if request.Header.Get("Authorization") != "Bearer fixture-access-token" {
		challenge := `Bearer scope="fixture.read"`
		if !f.options.OmitMetadataHint {
			challenge = `Bearer resource_metadata="` + f.baseURL + `/.well-known/oauth-protected-resource", scope="fixture.read"`
		}
		writer.Header().Set("WWW-Authenticate", challenge)
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	f.authorizedMCP.Add(1)
	proxy.ServeHTTP(writer, request)
}

func (f *oauthFixture) handlePRM(writer http.ResponseWriter) {
	f.protectedMetadata.Add(1)
	if f.options.PRMNotFound {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	if f.options.MalformedPRM {
		writeTestJSON(writer, map[string]any{"resource": f.baseURL + "/mcp", "authorization_servers": []string{}})
		return
	}
	writeTestJSON(writer, map[string]any{
		"resource":              f.baseURL + "/mcp",
		"authorization_servers": []string{f.baseURL},
		"scopes_supported":      []string{"fixture.read"},
	})
}

func (f *oauthFixture) handleAuthorizationMetadata(writer http.ResponseWriter) {
	f.authorizationMeta.Add(1)
	if f.options.LegacyASMetadata {
		http.Error(writer, "not found", http.StatusNotFound)
		return
	}
	writeTestJSON(writer, map[string]any{
		"issuer":                                         f.baseURL,
		"authorization_endpoint":                         f.baseURL + "/authorize",
		"token_endpoint":                                 f.baseURL + "/token",
		"registration_endpoint":                          f.baseURL + "/register",
		"code_challenge_methods_supported":               []string{"S256"},
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_post"},
		"authorization_response_iss_parameter_supported": f.options.IssuerInCallback || f.options.RequireIssuer,
		"scopes_supported":                               []string{"fixture.read", "offline_access"},
	})
}

func (f *oauthFixture) handleRegister(writer http.ResponseWriter, request *http.Request) {
	f.registrations.Add(1)
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var registration struct {
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		ClientName   string   `json:"client_name"`
	}
	if json.NewDecoder(request.Body).Decode(&registration) != nil {
		http.Error(writer, "invalid registration", http.StatusBadRequest)
		return
	}
	f.mu.Lock()
	f.registeredRedirect = ""
	if len(registration.RedirectURIs) == 1 {
		f.registeredRedirect = registration.RedirectURIs[0]
	}
	f.registered = registration.ClientName == "Wirecmd" && strings.HasSuffix(f.registeredRedirect, "/oauth/callback") && containsString(registration.GrantTypes, "authorization_code") && containsString(registration.GrantTypes, "refresh_token")
	f.mu.Unlock()
	writeTestJSON(writer, map[string]any{"client_id": "fixture-dynamic-client", "token_endpoint_auth_method": "none"})
}

func (f *oauthFixture) handleAuthorize(writer http.ResponseWriter, request *http.Request) {
	f.authorizations.Add(1)
	query := request.URL.Query()
	redirect, state := query.Get("redirect_uri"), query.Get("state")
	f.mu.Lock()
	f.pkceOK = query.Get("response_type") == "code" && query.Get("code_challenge_method") == "S256" && query.Get("code_challenge") != ""
	f.resourceOK = query.Get("resource") == f.baseURL+"/mcp"
	f.redirectOK = redirect != ""
	if f.options.ClientID == "" {
		f.redirectOK = f.redirectOK && redirect == f.registeredRedirect
		f.clientOK = query.Get("client_id") == "fixture-dynamic-client"
	} else {
		f.clientOK = query.Get("client_id") == f.options.ClientID
	}
	if f.options.Deny {
		f.mu.Unlock()
		redirectAuthorizationResult(writer, request, redirect, url.Values{"error": {"access_denied"}, "state": {state}}, f.options.IssuerInCallback, f.callbackIssuer())
		return
	}
	code := "fixture-code-" + base64.RawURLEncoding.EncodeToString([]byte{byte(f.nextCode.Add(1))})
	if f.options.StateMismatch {
		state = "fixture-wrong-state"
	}
	f.issuedCode, f.issuedState = code, state
	f.codes[code] = oauthFixtureCode{challenge: query.Get("code_challenge"), redirect: redirect, clientID: query.Get("client_id")}
	f.mu.Unlock()
	redirectAuthorizationResult(writer, request, redirect, url.Values{"code": {code}, "state": {state}}, f.options.IssuerInCallback, f.callbackIssuer())
}

func (f *oauthFixture) callbackIssuer() string {
	if f.options.CallbackIssuer != "" {
		return f.options.CallbackIssuer
	}
	return f.baseURL
}

func redirectAuthorizationResult(writer http.ResponseWriter, request *http.Request, redirect string, query url.Values, includeIssuer bool, issuer string) {
	if redirect == "" {
		http.Error(writer, "missing redirect URI", http.StatusBadRequest)
		return
	}
	if includeIssuer {
		query.Set("iss", issuer)
	}
	target, err := url.Parse(redirect)
	if err != nil {
		http.Error(writer, "invalid redirect URI", http.StatusBadRequest)
		return
	}
	target.RawQuery = query.Encode()
	http.Redirect(writer, request, target.String(), http.StatusFound)
}

func (f *oauthFixture) handleToken(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodPost {
		http.Error(writer, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(writer, "invalid form", http.StatusBadRequest)
		return
	}
	f.tokenRequests.Add(1)
	if request.Form.Get("grant_type") == "refresh_token" {
		f.refreshRequests.Add(1)
		if f.options.RefreshFails {
			writeOAuthError(writer, "invalid_grant")
			return
		}
		writeTestJSON(writer, map[string]any{"access_token": "fixture-access-token", "refresh_token": "fixture-refresh-token", "token_type": "Bearer", "expires_in": 3600})
		return
	}
	if request.Form.Get("grant_type") != "authorization_code" {
		writeOAuthError(writer, "unsupported_grant_type")
		return
	}
	f.mu.Lock()
	code, known := f.codes[request.Form.Get("code")]
	if known {
		delete(f.codes, request.Form.Get("code"))
	}
	clientID := request.Form.Get("client_id")
	clientSecret := request.Form.Get("client_secret")
	if f.options.ClientSecret != "" {
		f.secretOK = clientSecret == f.options.ClientSecret
	} else {
		f.secretOK = true
	}
	if f.options.ClientID != "" {
		f.clientOK = f.clientOK && clientID == f.options.ClientID && code.clientID == f.options.ClientID
	}
	redirectOK := known && request.Form.Get("redirect_uri") == code.redirect
	resourceOK := request.Form.Get("resource") == f.baseURL+"/mcp"
	pkceOK := known && pkceMatches(request.Form.Get("code_verifier"), code.challenge)
	f.redirectOK = f.redirectOK && redirectOK
	f.resourceOK = f.resourceOK && resourceOK
	f.pkceOK = f.pkceOK && pkceOK
	f.mu.Unlock()
	if !known || !redirectOK || !resourceOK || !pkceOK || (f.options.ClientSecret != "" && clientSecret != f.options.ClientSecret) {
		writeOAuthError(writer, "invalid_grant")
		return
	}
	writeTestJSON(writer, map[string]any{"access_token": "fixture-access-token", "refresh_token": "fixture-refresh-token", "token_type": "Bearer", "expires_in": 3600})
}

func writeOAuthError(writer http.ResponseWriter, code string) {
	writer.WriteHeader(http.StatusBadRequest)
	writeTestJSON(writer, map[string]any{"error": code})
}

func pkceMatches(verifier, challenge string) bool {
	sum := sha256.Sum256([]byte(verifier))
	return verifier != "" && base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func (f *oauthFixture) assertions() (registered, pkce, resource, redirect, client, secret bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.registered, f.pkceOK, f.resourceOK, f.redirectOK, f.clientOK, f.secretOK
}

func (f *oauthFixture) sensitiveValues() (code, state string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issuedCode, f.issuedState
}

func completeFixtureAuthorization(raw string) {
	go func() {
		response, err := http.Get(raw)
		if err == nil {
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		}
	}()
}
