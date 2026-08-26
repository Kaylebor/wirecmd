package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/oauthstore"
	mcpauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

var errAuthorizationRequired = errors.New("interactive authorization is required")
var errAuthorizationInProgress = errors.New("authorization already in progress")
var newOAuthStore = oauthstore.New

type oauthRecordPayload struct {
	Registration string        `json:"registration"`
	Config       oauthConfig   `json:"config"`
	Token        *oauth2.Token `json:"token"`
}

type oauthConfig struct {
	ClientID     string           `json:"client_id"`
	ClientSecret string           `json:"client_secret,omitempty"`
	AuthURL      string           `json:"auth_url"`
	TokenURL     string           `json:"token_url"`
	AuthStyle    oauth2.AuthStyle `json:"auth_style"`
	RedirectURL  string           `json:"redirect_url"`
	Scopes       []string         `json:"scopes,omitempty"`
}

func configFromOAuth(value oauthConfig) *oauth2.Config {
	return &oauth2.Config{ClientID: value.ClientID, ClientSecret: value.ClientSecret, Endpoint: oauth2.Endpoint{AuthURL: value.AuthURL, TokenURL: value.TokenURL, AuthStyle: value.AuthStyle}, RedirectURL: value.RedirectURL, Scopes: append([]string(nil), value.Scopes...)}
}

func configToOAuth(value *oauth2.Config) oauthConfig {
	return oauthConfig{ClientID: value.ClientID, ClientSecret: value.ClientSecret, AuthURL: value.Endpoint.AuthURL, TokenURL: value.Endpoint.TokenURL, AuthStyle: value.Endpoint.AuthStyle, RedirectURL: value.RedirectURL, Scopes: append([]string(nil), value.Scopes...)}
}

type oauthRuntime struct {
	store       *oauthstore.Store
	identity    []byte
	interactive bool
	force       bool
	emitURL     func(string)
	listener    net.Listener
	redirectURL string

	mu            sync.Mutex
	record        oauthstore.Record
	payload       oauthRecordPayload
	found         bool
	loadErr       error
	flowActive    bool
	flowResult    chan *mcpauth.AuthorizationResult
	flowErr       chan error
	server        *http.Server
	serverStarted bool
	onFlowStart   func() error
	onFlowEnd     func()
	initialCtx    context.Context
	redactor      *redactor
}

func newOAuthRuntime(httpConfig config.HTTP, endpoint string, clientSecret string, interactive, force bool, emitURL func(string)) (*oauthRuntime, *appError) {
	store, err := newOAuthStore()
	if err != nil {
		return nil, oauthStoreError(err)
	}
	identity, err := oauthCredentialIdentity(httpConfig, endpoint, clientSecret)
	if err != nil {
		return nil, configurationError("oauth_identity_invalid", err.Error(), "correct the configured HTTP OAuth values")
	}
	record, found, err := store.Load(identity)
	if err != nil && !errors.Is(err, oauthstore.ErrUnavailable) {
		return nil, oauthStoreError(err)
	}
	runtime := &oauthRuntime{store: store, identity: identity, interactive: interactive, force: force, emitURL: emitURL, record: record, found: found, loadErr: err}
	if found {
		if err := json.Unmarshal(record.Payload, &runtime.payload); err != nil || runtime.payload.Token == nil || runtime.payload.Config.ClientID == "" {
			return nil, oauthStoreError(oauthstore.ErrCorrupt)
		}
	}
	redirect := ""
	if httpConfig.OAuth != nil {
		redirect = httpConfig.OAuth.RedirectURI
	} else if found && runtime.payload.Config.RedirectURL != "" {
		redirect = runtime.payload.Config.RedirectURL
	}
	runtime.redirectURL = redirect
	// A new DCR registration needs the actual ephemeral redirect URI up front.
	// Existing registrations and preregistered clients bind lazily only if a
	// browser flow is required, so token reuse does not reserve a callback port.
	if redirect == "" {
		listener, redirectURL, err := listenOAuthCallback("")
		if err != nil {
			return nil, configurationError("oauth_callback_unavailable", err.Error(), "make a loopback callback address available")
		}
		runtime.listener, runtime.redirectURL = listener, redirectURL
	}
	return runtime, nil
}

func (r *oauthRuntime) Close() {
	if r == nil {
		return
	}
	r.mu.Lock()
	server, listener := r.server, r.listener
	r.mu.Unlock()
	if server != nil {
		_ = server.Close()
	} else if listener != nil {
		_ = listener.Close()
	}
}

func (r *oauthRuntime) CredentialsCurrent() (bool, error) {
	if r == nil || r.loadErr != nil {
		return true, nil
	}
	record, found, err := r.store.Load(r.identity)
	if err != nil {
		return false, err
	}
	r.mu.Lock()
	wantFound, generation := r.found, r.record.Generation
	r.mu.Unlock()
	if found != wantFound {
		return false, nil
	}
	return !found || record.Generation == generation, nil
}

func (r *oauthRuntime) markConnected() {
	r.mu.Lock()
	r.initialCtx = nil
	r.mu.Unlock()
}

func (r *oauthRuntime) setRedactor(redactor *redactor) {
	r.mu.Lock()
	r.redactor = redactor
	if r.found && r.payload.Token != nil {
		redactor.ProtectSecrets(r.payload.Config.ClientSecret, r.payload.Token.AccessToken, r.payload.Token.RefreshToken)
	}
	r.mu.Unlock()
}

func (r *oauthRuntime) flowKey() string { return credentialIDForDiagnostics(r.identity) }

func (d *daemon) beginOAuthFlow(runtime *oauthRuntime) error {
	key := runtime.flowKey()
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, exists := d.authFlows[key]; exists {
		return errAuthorizationInProgress
	}
	d.authFlows[key] = struct{}{}
	return nil
}

func (d *daemon) endOAuthFlow(runtime *oauthRuntime) {
	d.mu.Lock()
	delete(d.authFlows, runtime.flowKey())
	d.mu.Unlock()
}

func (r *oauthRuntime) Handler(httpConfig config.HTTP, clientSecret string) (mcpauth.OAuthHandler, *appError) {
	if r.loadErr != nil {
		return unavailableOAuthHandler{err: r.loadErr}, nil
	}
	var initial oauth2.TokenSource
	if r.found && !r.force {
		initial = configFromOAuth(r.payload.Config).TokenSource(context.Background(), r.payload.Token)
		initial = &savingTokenSource{runtime: r, source: initial}
	}
	settings := &mcpauth.AuthorizationCodeHandlerConfig{
		RedirectURL:              r.redirectURL,
		AuthorizationCodeFetcher: r.fetchAuthorizationCode,
		RequestRefreshToken:      true,
		InitialTokenSource:       initial,
	}
	registration := "dynamic"
	if httpConfig.OAuth != nil {
		registration = "preregistered"
		credentials := &oauthex.ClientCredentials{ClientID: httpConfig.OAuth.ClientID}
		if clientSecret != "" {
			credentials.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: clientSecret}
		}
		settings.PreregisteredClient = credentials
	} else if r.found && r.payload.Registration == "dynamic" {
		credentials := &oauthex.ClientCredentials{ClientID: r.payload.Config.ClientID}
		if r.payload.Config.ClientSecret != "" {
			credentials.ClientSecretAuth = &oauthex.ClientSecretAuth{ClientSecret: r.payload.Config.ClientSecret}
		}
		settings.PreregisteredClient = credentials
	} else {
		settings.DynamicClientRegistrationConfig = &mcpauth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{
			ClientName:              "Wirecmd",
			RedirectURIs:            []string{r.redirectURL},
			GrantTypes:              []string{"authorization_code", "refresh_token"},
			ResponseTypes:           []string{"code"},
			TokenEndpointAuthMethod: "none",
		}}
	}
	settings.NewTokenSource = func(ctx context.Context, cfg *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
		r.mu.Lock()
		r.payload = oauthRecordPayload{Registration: registration, Config: configToOAuth(cfg), Token: token}
		payload, err := json.Marshal(r.payload)
		if err == nil {
			// A successful explicit or automatic authorization is a new credential
			// generation. Later refreshes preserve it through savingTokenSource.
			r.record, err = r.store.Save(r.identity, oauthstore.Record{Payload: payload})
			if err == nil {
				r.found = true
			}
		}
		if err == nil && r.redactor != nil {
			r.redactor.ProtectSecrets(cfg.ClientSecret, token.AccessToken, token.RefreshToken)
		}
		r.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return &savingTokenSource{runtime: r, source: cfg.TokenSource(ctx, token)}, nil
	}
	handler, err := mcpauth.NewAuthorizationCodeHandler(settings)
	if err != nil {
		return nil, configurationError("oauth_configuration_invalid", err.Error(), "correct the configured OAuth client and redirect URI")
	}
	if !r.interactive {
		return nonInteractiveOAuthHandler{delegate: handler}, nil
	}
	return handler, nil
}

type unavailableOAuthHandler struct{ err error }

func (h unavailableOAuthHandler) TokenSource(context.Context) (oauth2.TokenSource, error) {
	return nil, nil
}
func (h unavailableOAuthHandler) Authorize(context.Context, *http.Request, *http.Response) error {
	return h.err
}

type nonInteractiveOAuthHandler struct{ delegate mcpauth.OAuthHandler }

func (h nonInteractiveOAuthHandler) TokenSource(ctx context.Context) (oauth2.TokenSource, error) {
	return h.delegate.TokenSource(ctx)
}
func (h nonInteractiveOAuthHandler) Authorize(context.Context, *http.Request, *http.Response) error {
	return errAuthorizationRequired
}

type savingTokenSource struct {
	mu      sync.Mutex
	runtime *oauthRuntime
	source  oauth2.TokenSource
}

func (s *savingTokenSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		return nil, err
	}
	s.runtime.mu.Lock()
	previous := s.runtime.payload.Token
	if previous == nil || previous.AccessToken != token.AccessToken || previous.RefreshToken != token.RefreshToken || !previous.Expiry.Equal(token.Expiry) {
		s.runtime.payload.Token = token
		payload, marshalErr := json.Marshal(s.runtime.payload)
		if marshalErr == nil {
			s.runtime.record.Payload = payload
			s.runtime.record, marshalErr = s.runtime.store.Save(s.runtime.identity, s.runtime.record)
		}
		if marshalErr != nil {
			s.runtime.mu.Unlock()
			return nil, marshalErr
		}
		if s.runtime.redactor != nil {
			s.runtime.redactor.ProtectSecrets(token.AccessToken, token.RefreshToken)
		}
	}
	s.runtime.mu.Unlock()
	return token, nil
}

func (r *oauthRuntime) fetchAuthorizationCode(ctx context.Context, args *mcpauth.AuthorizationArgs) (*mcpauth.AuthorizationResult, error) {
	r.mu.Lock()
	if r.flowActive {
		r.mu.Unlock()
		return nil, errors.New("authorization already in progress")
	}
	r.flowActive = true
	r.flowResult = make(chan *mcpauth.AuthorizationResult, 1)
	r.flowErr = make(chan error, 1)
	result, failure := r.flowResult, r.flowErr
	r.mu.Unlock()
	release, err := r.store.LockAuthorization(r.identity)
	if errors.Is(err, oauthstore.ErrAuthorizationLocked) {
		r.mu.Lock()
		r.flowActive = false
		r.flowResult, r.flowErr = nil, nil
		r.mu.Unlock()
		return nil, errAuthorizationInProgress
	}
	if err != nil {
		r.mu.Lock()
		r.flowActive = false
		r.flowResult, r.flowErr = nil, nil
		r.mu.Unlock()
		return nil, err
	}
	defer release()
	if r.onFlowStart != nil {
		if err := r.onFlowStart(); err != nil {
			r.mu.Lock()
			r.flowActive = false
			r.flowResult, r.flowErr = nil, nil
			r.mu.Unlock()
			return nil, err
		}
	}
	defer func() {
		r.mu.Lock()
		r.flowActive = false
		r.flowResult, r.flowErr = nil, nil
		r.mu.Unlock()
		if r.onFlowEnd != nil {
			r.onFlowEnd()
		}
	}()
	if err := r.ensureCallbackServer(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	initialCtx := r.initialCtx
	r.mu.Unlock()
	var initialDone <-chan struct{}
	if initialCtx != nil {
		initialDone = initialCtx.Done()
	}
	if r.emitURL != nil {
		r.emitURL(args.URL)
	}
	select {
	case value := <-result:
		return value, nil
	case err := <-failure:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-initialDone:
		return nil, context.Canceled
	}
}

func (r *oauthRuntime) ensureCallbackServer() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.serverStarted {
		return nil
	}
	if r.listener == nil {
		listener, _, err := listenOAuthCallback(r.redirectURL)
		if err != nil {
			return fmt.Errorf("bind OAuth callback: %w", err)
		}
		r.listener = listener
	}
	r.server = &http.Server{Handler: http.HandlerFunc(r.handleCallback), ReadHeaderTimeout: 10 * time.Second}
	r.serverStarted = true
	server, listener := r.server, r.listener
	go func() { _ = server.Serve(listener) }()
	return nil
}

func (r *oauthRuntime) handleCallback(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet || request.URL.Path != callbackPath(r.redirectURL) {
		http.NotFound(writer, request)
		return
	}
	r.mu.Lock()
	result, failure, active := r.flowResult, r.flowErr, r.flowActive
	r.mu.Unlock()
	if !active {
		http.Error(writer, "No Wirecmd authorization is in progress.", http.StatusConflict)
		return
	}
	query := request.URL.Query()
	if providerError := query.Get("error"); providerError != "" {
		select {
		case failure <- fmt.Errorf("authorization provider returned %s", providerError):
		default:
		}
		http.Error(writer, "Authorization was not completed. You may close this window.", http.StatusBadRequest)
		return
	}
	code, state := query.Get("code"), query.Get("state")
	if code == "" || state == "" {
		http.Error(writer, "Invalid authorization callback.", http.StatusBadRequest)
		return
	}
	select {
	case result <- &mcpauth.AuthorizationResult{Code: code, State: state, Iss: query.Get("iss")}:
		writer.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = writer.Write([]byte("Authorization complete. You may close this window.\n"))
	default:
		http.Error(writer, "Authorization callback was already received.", http.StatusConflict)
	}
}

func listenOAuthCallback(configured string) (net.Listener, string, error) {
	if configured == "" {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return nil, "", err
		}
		return listener, "http://" + listener.Addr().String() + "/oauth/callback", nil
	}
	parsed, err := url.Parse(configured)
	if err != nil {
		return nil, "", err
	}
	host := parsed.Host
	listener, err := net.Listen("tcp", host)
	if err != nil {
		return nil, "", err
	}
	return listener, configured, nil
}

func callbackPath(raw string) string {
	parsed, _ := url.Parse(raw)
	return parsed.Path
}

func oauthCredentialIdentity(httpConfig config.HTTP, endpoint, clientSecret string) ([]byte, error) {
	registration := "dynamic"
	clientID := ""
	if httpConfig.OAuth != nil {
		registration, clientID = "preregistered", httpConfig.OAuth.ClientID
	}
	canonical, err := json.Marshal(struct {
		Version      int    `json:"version"`
		Endpoint     string `json:"endpoint"`
		Registration string `json:"registration"`
		ClientID     string `json:"client_id,omitempty"`
		ClientSecret string `json:"client_secret,omitempty"`
	}{1, endpoint, registration, clientID, clientSecret})
	if err != nil {
		return nil, err
	}
	return canonical, nil
}

func oauthStoreError(err error) *appError {
	if errors.Is(err, oauthstore.ErrStale) {
		return authenticationError("credentials_changed", "the stored OAuth credential changed during this operation", "retry using the current credential or authenticate again")
	}
	if errors.Is(err, oauthstore.ErrUnavailable) {
		return userActionError("credential_store_unavailable", "the native credential store is unavailable", "start or unlock the desktop keyring service and retry")
	}
	if errors.Is(err, oauthstore.ErrUnsafeState) || errors.Is(err, oauthstore.ErrCorrupt) {
		return userActionError("credential_store_unsafe", err.Error(), "repair or remove the unsafe Wirecmd OAuth state and authenticate again")
	}
	return &appError{category: "internal", code: "credential_store_failed", message: err.Error(), action: "check the Wirecmd state directory and native keyring", exitCode: exitInternal}
}

func runDirectAuth(ctx context.Context, admin authAdmin, server config.Server, root *config.Root, cwd string, in io.Reader, errOut io.Writer) (any, *appError) {
	target, secrets, appErr := makeTarget(server, root, cwd, os.LookupEnv)
	if appErr != nil {
		return nil, appErr
	}
	clientSecret, secretValues, appErr := resolveOAuthClientSecret(*server.HTTP, os.LookupEnv)
	if appErr != nil {
		return nil, appErr
	}
	secrets = append(secrets, secretValues...)
	identity, err := oauthCredentialIdentity(*server.HTTP, target.endpoint, clientSecret)
	if err != nil {
		return nil, configurationError("oauth_identity_invalid", err.Error(), "correct the configured OAuth values")
	}
	store, err := newOAuthStore()
	if err != nil {
		return nil, oauthStoreError(err)
	}
	switch admin.command {
	case "status":
		record, found, err := store.Load(identity)
		if err != nil {
			return nil, oauthStoreError(err)
		}
		status := authStatus{Server: server.Name, Status: "unauthenticated"}
		if found {
			var payload oauthRecordPayload
			if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.Token == nil {
				return nil, oauthStoreError(oauthstore.ErrCorrupt)
			}
			status.Status, status.Registration = "authenticated", payload.Registration
			if !payload.Token.Expiry.IsZero() {
				status.ExpiresAt = payload.Token.Expiry.UTC().Format(time.RFC3339)
			}
		}
		return authEnvelope{OK: true, Auth: status}, nil
	case "logout":
		if _, err := store.Delete(identity); err != nil {
			return nil, oauthStoreError(err)
		}
		return authEnvelope{OK: true, Auth: authStatus{Server: server.Name, Status: "logged_out"}}, nil
	case "login":
		if !isInteractiveTerminal(in, errOut) || os.Getenv("WIRECMD_NONINTERACTIVE") == "1" {
			return nil, userActionError("interactive_terminal_required", "OAuth login requires local terminal input and diagnostics", "run this command from an interactive terminal on the Wirecmd host")
		}
		before, beforeFound, err := store.Load(identity)
		if err != nil {
			return nil, oauthStoreError(err)
		}
		redactor := newRedactor(secrets, errOut)
		redactor.ProtectEndpoint(target.endpoint)
		target, oauthSecrets, appErr := attachOAuth(target, server.Name, *server.HTTP, os.LookupEnv, true, true, browserEventHandler(errOut, redactor))
		if appErr != nil {
			return nil, appErr
		}
		defer target.oauthRun.Close()
		redactor.ProtectSecrets(oauthSecrets...)
		target.oauthRun.setRedactor(redactor)
		defer redactor.FlushTo(errOut)
		session, appErr := connectTarget(ctx, target, redactor)
		if appErr != nil {
			return nil, appErr.redacted(redactor)
		}
		_ = session.Close()
		after, found, err := store.Load(identity)
		if err != nil {
			return nil, oauthStoreError(err)
		}
		if !found || (beforeFound && after.Generation == before.Generation) {
			return nil, authenticationError("oauth_not_required", "the upstream server did not request a fresh OAuth authorization", "verify that the selected endpoint requires OAuth")
		}
		var payload oauthRecordPayload
		if err := json.Unmarshal(after.Payload, &payload); err != nil || payload.Token == nil {
			return nil, oauthStoreError(oauthstore.ErrCorrupt)
		}
		status := authStatus{Server: server.Name, Status: "authenticated", Registration: payload.Registration}
		if !payload.Token.Expiry.IsZero() {
			status.ExpiresAt = payload.Token.Expiry.UTC().Format(time.RFC3339)
		}
		return authEnvelope{OK: true, Auth: status}, nil
	default:
		return nil, invocationError("auth_admin_invalid", "invalid auth command", "use login, status, or logout")
	}
}

func resolveOAuthClientSecret(transport config.HTTP, lookup func(string) (string, bool)) (string, []string, *appError) {
	if transport.OAuth == nil || transport.OAuth.ClientSecret == nil {
		return "", nil, nil
	}
	resolved, err := transport.OAuth.ClientSecret.ResolveEnv(lookup)
	if err != nil {
		return "", nil, configurationError("secret_not_available", err.Error(), "set the required environment variable before invoking Wirecmd")
	}
	secrets := []string(nil)
	if resolved.Sensitive && resolved.Text != "" {
		secrets = append(secrets, resolved.Text)
	}
	return resolved.Text, secrets, nil
}

func (d *daemon) executeAuth(ctx context.Context, request daemonRequest, server config.Server, root *config.Root, warnings []string, emitURL func(string) error) daemonReply {
	if server.HTTP == nil || hasAuthorizationHeader(*server.HTTP) {
		return errorReplyWithWarnings(authServerError(server.Name), warnings)
	}
	lookup := func(name string) (string, bool) {
		value, ok := request.Secrets[name]
		return value.Value, ok && value.Present
	}
	target, secrets, appErr := makeTarget(server, root, request.CWD, lookup)
	if appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	clientSecret, oauthSecrets, appErr := resolveOAuthClientSecret(*server.HTTP, lookup)
	if appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	secrets = append(secrets, oauthSecrets...)
	identity, err := oauthCredentialIdentity(*server.HTTP, target.endpoint, clientSecret)
	if err != nil {
		return errorReplyWithWarnings(configurationError("oauth_identity_invalid", err.Error(), "correct the configured OAuth values"), warnings)
	}
	store, err := newOAuthStore()
	if err != nil {
		return errorReplyWithWarnings(oauthStoreError(err), warnings)
	}
	switch request.Auth {
	case "status":
		record, found, err := store.Load(identity)
		if err != nil {
			return errorReplyWithWarnings(oauthStoreError(err), warnings)
		}
		status := authStatus{Server: server.Name, Status: "unauthenticated"}
		if found {
			var payload oauthRecordPayload
			if err := json.Unmarshal(record.Payload, &payload); err != nil || payload.Token == nil {
				return errorReplyWithWarnings(oauthStoreError(oauthstore.ErrCorrupt), warnings)
			}
			status.Status, status.Registration = "authenticated", payload.Registration
			if !payload.Token.Expiry.IsZero() {
				status.ExpiresAt = payload.Token.Expiry.UTC().Format(time.RFC3339)
			}
		}
		return resultReply(authEnvelope{OK: true, Auth: status}, warnings)
	case "logout":
		if _, err := store.Delete(identity); err != nil {
			return errorReplyWithWarnings(oauthStoreError(err), warnings)
		}
		d.retireOAuthCredential(credentialIDForDiagnostics(identity))
		return resultReply(authEnvelope{OK: true, Auth: authStatus{Server: server.Name, Status: "logged_out"}}, warnings)
	case "login":
		if !request.Interactive {
			return errorReplyWithWarnings(userActionError("interactive_terminal_required", "OAuth login requires local terminal input and diagnostics", "run this command from an interactive terminal on the Wirecmd host"), warnings)
		}
		before, beforeFound, err := store.Load(identity)
		if err != nil {
			return errorReplyWithWarnings(oauthStoreError(err), warnings)
		}
		target, oauthSecrets, appErr = attachOAuth(target, server.Name, *server.HTTP, lookup, true, true, func(raw string) {
			if emitURL != nil {
				_ = emitURL(raw)
			}
		})
		if appErr != nil {
			return errorReplyWithWarnings(appErr, warnings)
		}
		target.oauthRun.onFlowStart = func() error { return d.beginOAuthFlow(target.oauthRun) }
		target.oauthRun.onFlowEnd = func() { d.endOAuthFlow(target.oauthRun) }
		defer target.oauthRun.Close()
		redactor := newRedactor(append(secrets, oauthSecrets...), d.stderr)
		target.oauthRun.setRedactor(redactor)
		redactor.ProtectEndpoint(target.endpoint)
		defer redactor.FlushTo(d.stderr)
		session, appErr := connectTarget(ctx, target, redactor)
		if appErr != nil {
			return errorReplyWithWarnings(appErr.redacted(redactor), warnings)
		}
		_ = session.Close()
		after, found, err := store.Load(identity)
		if err != nil {
			return errorReplyWithWarnings(oauthStoreError(err), warnings)
		}
		if !found || (beforeFound && after.Generation == before.Generation) {
			return errorReplyWithWarnings(authenticationError("oauth_not_required", "the upstream server did not request a fresh OAuth authorization", "verify that the selected endpoint requires OAuth"), warnings)
		}
		var payload oauthRecordPayload
		if err := json.Unmarshal(after.Payload, &payload); err != nil || payload.Token == nil {
			return errorReplyWithWarnings(oauthStoreError(oauthstore.ErrCorrupt), warnings)
		}
		d.retireOAuthCredential(credentialIDForDiagnostics(identity))
		status := authStatus{Server: server.Name, Status: "authenticated", Registration: payload.Registration}
		if !payload.Token.Expiry.IsZero() {
			status.ExpiresAt = payload.Token.Expiry.UTC().Format(time.RFC3339)
		}
		return resultReply(authEnvelope{OK: true, Auth: status}, warnings)
	default:
		return errorReplyWithWarnings(invocationError("auth_admin_invalid", "invalid auth command", "use login, status, or logout"), warnings)
	}
}

func (d *daemon) retireOAuthCredential(flowKey string) {
	var closeNow []*retainedInstance
	d.mu.Lock()
	for key, entry := range d.pools {
		if entry.instance == nil || entry.instance.oauthRun == nil || entry.instance.oauthRun.flowKey() != flowKey {
			continue
		}
		entry.retiring = true
		delete(d.pools, key)
		if entry.instance != nil {
			entry.instance.retiring = true
			d.retiring[entry.instance] = struct{}{}
			if entry.instance.active == 0 {
				closeNow = append(closeNow, entry.instance)
			}
		}
	}
	d.mu.Unlock()
	for _, instance := range closeNow {
		instance.mu.Lock()
		if !instance.closed {
			instance.closed = true
			_ = instance.session.Close()
			if instance.oauthRun != nil {
				instance.oauthRun.Close()
			}
			instance.redactor.FlushTo(d.stderr)
		}
		instance.mu.Unlock()
		d.mu.Lock()
		delete(d.retiring, instance)
		d.mu.Unlock()
	}
}

func openBrowserURL(raw string) error {
	if strings.TrimSpace(raw) == "" {
		return errors.New("empty authorization URL")
	}
	command, err := browserOpenCommand(raw)
	if err != nil {
		return err
	}
	if err := command.Start(); err != nil {
		return err
	}
	go func() { _ = command.Wait() }()
	return nil
}

func credentialIDForDiagnostics(identity []byte) string {
	sum := sha256.Sum256(identity)
	return hex.EncodeToString(sum[:])
}
