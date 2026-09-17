package cli

// This file implements Wirecmd's deliberately small, same-user daemon. Its
// protocol is private: it transports already-normalized shell operations, not
// MCP requests or SDK objects.

import (
	"bufio"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Kaylebor/wirecmd/internal/buildinfo"
	"github.com/Kaylebor/wirecmd/internal/config"
	"github.com/Kaylebor/wirecmd/internal/lspclient"
	secretpkg "github.com/Kaylebor/wirecmd/internal/secrets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const daemonProtocol = 15

type daemonAdmin struct {
	command string
	err     *appError
}

// parseDaemonAdmin intentionally reserves only bare administrative forms.
// --json/--stdin and an exact-call object remain an escape hatch for a server
// named daemon.
func parseDaemonAdmin(positionals []string, opts options) (daemonAdmin, bool) {
	if len(positionals) != 2 || positionals[0] != "daemon" || !isDaemonAdminCommand(positionals[1]) {
		return daemonAdmin{}, false
	}
	if opts.jsonSet || opts.stdin || startsJSONObject(positionals[1]) {
		return daemonAdmin{}, false
	}
	if opts.direct || len(opts.configs) != 0 {
		return daemonAdmin{err: invocationError("daemon_admin_flags", "daemon administration does not accept --direct or --config", "run wirecmd daemon run, status, or reload without client flags")}, true
	}
	return daemonAdmin{command: positionals[1]}, true
}

func isDaemonAdminCommand(command string) bool {
	return command == "run" || command == "status" || command == "reload"
}

type foregroundDaemon struct{}

func (foregroundDaemon) run(ctx context.Context, out, errOut io.Writer, presentation presentation) int {
	d, err := newDaemon(errOut)
	if err != nil {
		writeOutput(out, failureEnvelope(err), presentation)
		return err.exitCode
	}
	defer d.close()
	writeOutput(out, map[string]any{"ok": true, "daemon": map[string]any{"status": "running", "protocol": daemonProtocol, "pid": os.Getpid()}}, presentation)
	if err := d.serve(ctx); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
		fmt.Fprintln(errOut, "wirecmd daemon:", err)
		return exitInternal
	}
	return exitOK
}

type daemonHello struct {
	Type     string `json:"type"`
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
}

type daemonHelloReply struct {
	Type     string     `json:"type"`
	Protocol int        `json:"protocol,omitempty"`
	Error    *errorBody `json:"error,omitempty"`
}

func clientDaemonHello() daemonHello {
	return daemonHello{Type: "hello", Protocol: daemonProtocol, Version: buildinfo.Version()}
}

type secretInput struct {
	Present bool   `json:"present"`
	Value   string `json:"value,omitempty"`
}

type daemonRequest struct {
	Operation             operation                   `json:"operation"`
	Server                string                      `json:"server,omitempty"`
	Tool                  string                      `json:"tool,omitempty"`
	URI                   string                      `json:"uri,omitempty"`
	Arguments             map[string]any              `json:"arguments,omitempty"`
	Projected             []projectedArgument         `json:"projected,omitempty"`
	Overlay               map[string]any              `json:"overlay,omitempty"`
	Help                  helpKind                    `json:"help,omitempty"`
	CWD                   string                      `json:"cwd"`
	ProjectRoot           string                      `json:"project_root,omitempty"`
	GlobalRoot            string                      `json:"global_root,omitempty"`
	ProviderRoot          string                      `json:"provider_root,omitempty"`
	ScopeOwner            string                      `json:"scope_owner,omitempty"`
	TrustedBoundary       string                      `json:"trusted_boundary,omitempty"`
	WorkspaceConfig       string                      `json:"workspace_config,omitempty"`
	WorkspaceDirectory    string                      `json:"workspace_directory,omitempty"`
	Configs               []string                    `json:"configs,omitempty"`
	Discovered            bool                        `json:"discovered,omitempty"`
	Fingerprint           string                      `json:"fingerprint,omitempty"`
	Execution             string                      `json:"execution_fingerprint,omitempty"`
	SecretScopes          map[string]secretScopeInput `json:"secret_scopes,omitempty"`
	Admin                 string                      `json:"admin,omitempty"`
	Auth                  string                      `json:"auth,omitempty"`
	Interactive           bool                        `json:"interactive,omitempty"`
	LSPFile               string                      `json:"lsp_file,omitempty"`
	LSPLine               int                         `json:"lsp_line,omitempty"`
	LSPColumn             int                         `json:"lsp_column,omitempty"`
	LSPQuery              string                      `json:"lsp_query,omitempty"`
	LSPQuerySet           bool                        `json:"lsp_query_set,omitempty"`
	LSPOperation          string                      `json:"lsp_operation,omitempty"`
	LSPIncludeDeclaration bool                        `json:"lsp_include_declaration,omitempty"`
}

type secretScopeInput struct {
	EnvSecrets   map[string]secretInput `json:"env_secrets,omitempty"`
	SecretStores []string               `json:"secret_stores,omitempty"`
}

type daemonReply struct {
	Type     string          `json:"type,omitempty"`
	URL      string          `json:"url,omitempty"`
	Result   json.RawMessage `json:"result,omitempty"`
	Error    *errorBody      `json:"error,omitempty"`
	ExitCode int             `json:"exit_code,omitempty"`
	Warnings []string        `json:"warnings,omitempty"`
}

func appErrorFromBody(body *errorBody, code int) *appError {
	if body == nil {
		return nil
	}
	if code == 0 {
		code = exitInternal
	}
	return &appError{category: body.Category, code: body.Code, message: body.Message, action: body.Action, details: body.Details, exitCode: code}
}

func absoluteConfigPaths(cwd string, paths []string) ([]string, error) {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		path = filepath.Clean(path)
		if canonical, err := filepath.EvalSymlinks(path); err == nil {
			path = filepath.Clean(canonical)
		}
		result = append(result, path)
	}
	return result, nil
}

func daemonRequestFromConfig(req request, cfg *config.Config, source configContext) daemonRequest {
	request := daemonRequest{Operation: req.operation, Server: req.server, Tool: req.tool, URI: req.uri, Arguments: req.arguments, Projected: req.projected, Overlay: req.overlay, Help: req.help, CWD: source.CWD, Configs: source.Configs, Discovered: source.Discovered, TrustedBoundary: source.TrustedBoundary, WorkspaceConfig: source.WorkspaceConfig, WorkspaceDirectory: source.WorkspaceDirectory, Fingerprint: configFingerprint(cfg)}
	if req.operation != listServers {
		if server, ok := findServer(cfg, req.server); ok {
			request.Execution = serverExecutionFingerprint(server, nil, source.CWD, cfg.Secrets)
		}
	}
	return request
}

func selectedSecretInputs(server config.Server, lookup func(string) (string, bool)) map[string]secretInput {
	result := map[string]secretInput{}
	add := func(value config.Value) {
		if !value.IsSecret() {
			return
		}
		name, env := isEnvReference(value.Text)
		if !env {
			return
		}
		if _, exists := result[name]; exists {
			return
		}
		resolved, present := lookup(name)
		result[name] = secretInput{Present: present, Value: resolved}
	}
	if server.HTTP != nil {
		for _, field := range server.HTTP.Query {
			add(field.Value)
		}
		for _, field := range server.HTTP.Headers {
			add(field.Value)
		}
		if server.HTTP.OAuth != nil && server.HTTP.OAuth.ClientSecret != nil {
			add(*server.HTTP.OAuth.ClientSecret)
		}
	} else {
		return selectedStdioSecretInputs(server.Stdio, lookup)
	}
	return result
}

// configFingerprint deliberately excludes invocation context and resolved
// values. A declared root remains part of static configuration because its
// declaring file gives a relative value meaning.
func configFingerprint(cfg *config.Config) string {
	servers := make([]any, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		servers = append(servers, semanticServer(server))
	}
	root := ""
	if cfg.Root != nil {
		root = resolveRoot(*cfg.Root)
	}
	lsps := make([]any, 0, len(cfg.LSPs))
	for _, definition := range cfg.LSPs {
		lsps = append(lsps, semanticLSP(definition))
	}
	return fingerprint(map[string]any{"v": 4, "root": root, "git_root": cfg.GitRoot.Enabled, "secrets": semanticSecrets(cfg.Secrets), "servers": servers, "lsps": lsps})
}

func semanticSecrets(configured config.Secrets) any {
	identities := []string(nil)
	if configured.Age != nil {
		identities = make([]string, len(configured.Age.Identities))
		for index, identity := range configured.Age.Identities {
			identities[index] = identity.Path
		}
	}
	return map[string]any{"age_identities": identities}
}

func serverExecutionFingerprint(server config.Server, root *config.Root, cwd string, configured config.Secrets) string {
	value := map[string]any{"v": 1, "execution": executionFingerprint(server, root, cwd)}
	if len(selectedAgeReferences(serverSecretValues(server))) != 0 {
		value["secrets"] = semanticSecrets(configured)
	}
	return fingerprint(value)
}

func matchedLSPExecutionFingerprint(matches []lspMatch, root *config.Root, cwd string, configured config.Secrets) string {
	value := map[string]any{"v": 1, "execution": lspMatchesExecutionFingerprint(matches, root, cwd)}
	if len(selectedAgeReferences(lspSecretValues(matches))) != 0 {
		value["secrets"] = semanticSecrets(configured)
	}
	return fingerprint(value)
}

func lspExecutionFingerprint(definition config.LSP, root *config.Root, cwd string) string {
	resolvedRoot := cwd
	if root != nil {
		resolvedRoot = resolveRoot(*root)
	}
	return fingerprint(map[string]any{"v": 3, "root": resolvedRoot, "lsp": semanticLSPExecution(definition)})
}

func lspMatchesExecutionFingerprint(matches []lspMatch, root *config.Root, cwd string) string {
	resolvedRoot := cwd
	if root != nil {
		resolvedRoot = resolveRoot(*root)
	}
	definitions := make([]any, 0, len(matches))
	for _, match := range matches {
		providerRoot := resolvedRoot
		owner := resolvedRoot
		if match.Context.Root != "" {
			providerRoot = match.Context.Root
			owner = match.Context.Owner
		}
		definitions = append(definitions, []any{semanticLSPExecution(match.Definition), match.LanguageID, owner, providerRoot})
	}
	return fingerprint(map[string]any{"v": 2, "providers": definitions})
}

func semanticLSP(definition config.LSP) any {
	value := semanticLSPExecution(definition).(map[string]any)
	value["implementation_id"] = definition.ImplementationID
	return value
}

func semanticLSPExecution(definition config.LSP) any {
	args := make([]any, 0, len(definition.Stdio.Args))
	for _, arg := range definition.Stdio.Args {
		args = append(args, []any{arg.Kind, arg.Text})
	}
	env := make([]any, 0, len(definition.Stdio.Env))
	for _, entry := range definition.Stdio.Env {
		env = append(env, []any{entry.Name, entry.Value.Kind, entry.Value.Text})
	}
	selectors := make([]any, 0, len(definition.Selectors))
	for _, selector := range definition.Selectors {
		selectors = append(selectors, []any{selector.LanguageID, selector.Pattern})
	}
	return map[string]any{"name": definition.Name, "scope": definition.Scope, "selectors": selectors, "initialization_options": semanticJSONValue(definition.InitializationOptions), "command": semanticValue(definition.Stdio.Command), "args": args, "env": env}
}

func semanticJSONValue(value *config.JSONValue) any {
	if value == nil {
		return nil
	}
	digest := sha256.Sum256(value.Raw)
	return []any{value.Template, hex.EncodeToString(digest[:])}
}

func executionFingerprint(server config.Server, root *config.Root, cwd string) string {
	resolvedRoot := cwd
	if root != nil {
		resolvedRoot = resolveRoot(*root)
	}
	return fingerprint(map[string]any{"v": 1, "root": resolvedRoot, "server": semanticServer(server)})
}

func semanticServer(server config.Server) any {
	if server.HTTP != nil {
		query := make([]any, 0, len(server.HTTP.Query))
		for _, field := range server.HTTP.Query {
			query = append(query, []any{field.Name, field.Value.Kind, field.Value.Text})
		}
		headers := make([]any, 0, len(server.HTTP.Headers))
		for _, field := range server.HTTP.Headers {
			headers = append(headers, []any{field.Name, field.Value.Kind, field.Value.Text})
		}
		var oauth any
		if server.HTTP.OAuth != nil {
			secret := any(nil)
			if server.HTTP.OAuth.ClientSecret != nil {
				secret = []any{server.HTTP.OAuth.ClientSecret.Kind, server.HTTP.OAuth.ClientSecret.Text}
			}
			oauth = map[string]any{"client_id": semanticValue(server.HTTP.OAuth.ClientID), "client_secret": secret, "redirect_uri": semanticValue(server.HTTP.OAuth.RedirectURI)}
		}
		transport := "http"
		if server.HTTP.Kind == config.HTTPTransportSSE {
			transport = "sse"
		}
		return map[string]any{"name": server.Name, "scope": server.Scope, "transport": transport, "endpoint": semanticValue(server.HTTP.Endpoint), "query": query, "headers": headers, "oauth": oauth}
	}
	args := make([]any, 0, len(server.Stdio.Args))
	for _, arg := range server.Stdio.Args {
		args = append(args, []any{arg.Kind, arg.Text})
	}
	env := make([]any, 0, len(server.Stdio.Env))
	for _, entry := range server.Stdio.Env {
		env = append(env, []any{entry.Name, entry.Value.Kind, entry.Value.Text})
	}
	return map[string]any{"name": server.Name, "scope": server.Scope, "transport": "stdio", "command": semanticValue(server.Stdio.Command), "args": args, "env": env}
}

func semanticValue(value config.Value) any {
	return []any{value.Kind, value.Text}
}

func fingerprint(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(append([]byte("wirecmd-semantic-v1\x00"), encoded...))
	return hex.EncodeToString(digest[:])
}

func runtimePaths() (string, string, string, *appError) {
	runtime, configured := os.LookupEnv("XDG_RUNTIME_DIR")
	if !configured {
		runtime = defaultRuntimeDirectory()
	}
	return runtimePathsFor(runtime, !configured)
}

func runtimePathsFor(runtime string, rejectSymlink bool) (string, string, string, *appError) {
	if runtime == "" || !filepath.IsAbs(runtime) {
		return "", "", "", transportError("runtime_dir_unavailable", "XDG_RUNTIME_DIR must name an absolute private runtime directory", "set XDG_RUNTIME_DIR or use --direct deliberately")
	}
	stat := os.Stat
	if rejectSymlink {
		stat = os.Lstat
	}
	info, err := stat(runtime)
	if err != nil {
		return "", "", "", transportError("runtime_dir_unavailable", err.Error(), "use an accessible private XDG_RUNTIME_DIR or --direct deliberately")
	}
	if !info.IsDir() || (rejectSymlink && info.Mode()&os.ModeSymlink != 0) || info.Mode().Perm()&0o077 != 0 || !ownedByCurrentUser(info) {
		return "", "", "", transportError("runtime_dir_unsafe", "XDG_RUNTIME_DIR is not a private directory owned by the current user", "correct XDG_RUNTIME_DIR permissions or use --direct deliberately")
	}
	directory := filepath.Join(runtime, "wirecmd")
	return directory, filepath.Join(directory, "daemon.sock"), filepath.Join(directory, "daemon.lock"), nil
}

func ownedByCurrentUser(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Getuid())
}

func openRuntimeDirectory() (string, string, string, *appError) {
	directory, socket, lock, appErr := runtimePaths()
	if appErr != nil {
		return "", "", "", appErr
	}
	if err := os.Mkdir(directory, 0o700); err != nil && !os.IsExist(err) {
		return "", "", "", transportError("runtime_dir_unavailable", err.Error(), "create a private XDG runtime directory or use --direct deliberately")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !ownedByCurrentUser(info) {
		return "", "", "", transportError("runtime_dir_unsafe", "Wirecmd runtime directory must be a private directory owned by the current user", "remove unsafe runtime state and restart the daemon")
	}
	return directory, socket, lock, nil
}

type daemon struct {
	listener net.Listener
	socket   string
	lock     *os.File
	stderr   io.Writer

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu          sync.Mutex
	generation  uint64
	configs     map[string]*daemonConfig
	pools       map[string]*poolEntry
	retiring    map[*retainedInstance]struct{}
	connections map[net.Conn]struct{}
	closing     bool
	hmacKey     []byte
	authFlows   map[string]struct{}
	secretCache map[string]secretCacheEntry
}

type daemonConfig struct {
	config       *config.Config
	declaredRoot *config.Root
	fingerprint  string
}

type secretCacheEntry struct {
	snapshot string
	poolKeys map[string]string
}

type poolEntry struct {
	ready       chan struct{}
	instance    *retainedInstance
	err         *appError
	broken      bool
	retiring    bool
	authStarted chan struct{}
	authOnce    sync.Once
}

type retainedInstance struct {
	mu                   sync.Mutex
	session              *mcp.ClientSession
	lspSession           *lspclient.Session
	redactor             *redactor
	breakOnRequestCancel bool
	toolsPrimed          bool
	active               int
	retiring             bool
	broken               bool
	closed               bool
	oauthRun             *oauthRuntime
	lspName              string
	lspWorkspace         string
	lspExecution         string
	lspGeneration        uint64
}

func (instance *retainedInstance) close() {
	if instance.closed {
		return
	}
	instance.closed = true
	if instance.session != nil {
		_ = instance.session.Close()
	}
	if instance.lspSession != nil {
		_ = instance.lspSession.Close()
	}
	if instance.oauthRun != nil {
		instance.oauthRun.Close()
	}
	if instance.redactor != nil {
		instance.redactor.FlushTo(instance.redactor.writer)
	}
}

func newDaemon(stderr io.Writer) (*daemon, *appError) {
	_, socket, lockPath, appErr := openRuntimeDirectory()
	if appErr != nil {
		return nil, appErr
	}
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, transportError("daemon_lock_failed", err.Error(), "check the Wirecmd runtime directory")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = lock.Close()
		return nil, transportError("daemon_already_running", "another Wirecmd daemon holds the runtime lock", "use wirecmd daemon status or stop the existing daemon")
	}
	if info, err := os.Lstat(socket); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0o600 || !ownedByCurrentUser(info) {
			_ = lock.Close()
			return nil, transportError("daemon_socket_unsafe", "existing daemon socket path is unsafe", "remove unsafe runtime state before starting Wirecmd")
		}
		if err := os.Remove(socket); err != nil {
			_ = lock.Close()
			return nil, transportError("daemon_socket_cleanup_failed", err.Error(), "remove the stale Wirecmd socket and retry")
		}
	} else if !os.IsNotExist(err) {
		_ = lock.Close()
		return nil, transportError("daemon_socket_stat_failed", err.Error(), "check the Wirecmd runtime directory")
	}
	listener, err := net.Listen("unix", socket)
	if err != nil {
		_ = lock.Close()
		return nil, transportError("daemon_listen_failed", err.Error(), "check the Wirecmd runtime directory and existing daemon")
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = listener.Close()
		removeOwnedSocket(socket)
		_ = lock.Close()
		return nil, transportError("daemon_socket_permissions_failed", err.Error(), "check the Wirecmd runtime directory")
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		_ = listener.Close()
		removeOwnedSocket(socket)
		_ = lock.Close()
		return nil, transportError("daemon_randomness_failed", err.Error(), "restart Wirecmd after system randomness is available")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &daemon{listener: listener, socket: socket, lock: lock, stderr: stderr, ctx: ctx, cancel: cancel, configs: map[string]*daemonConfig{}, pools: map[string]*poolEntry{}, retiring: map[*retainedInstance]struct{}{}, connections: map[net.Conn]struct{}{}, hmacKey: key, authFlows: map[string]struct{}{}, secretCache: map[string]secretCacheEntry{}}, nil
}

func (d *daemon) close() {
	d.cancel()
	_ = d.listener.Close()
	d.mu.Lock()
	d.closing = true
	instances := make([]*retainedInstance, 0, len(d.pools)+len(d.retiring))
	for _, entry := range d.pools {
		entry.retiring = true
		if entry.instance != nil {
			entry.instance.retiring = true
			instances = append(instances, entry.instance)
		}
	}
	for instance := range d.retiring {
		instances = append(instances, instance)
	}
	connections := make([]net.Conn, 0, len(d.connections))
	for connection := range d.connections {
		connections = append(connections, connection)
	}
	d.mu.Unlock()
	for _, connection := range connections {
		_ = connection.Close()
	}
	for _, instance := range instances {
		instance.mu.Lock()
		instance.close()
		instance.mu.Unlock()
	}
	d.wg.Wait()
	removeOwnedSocket(d.socket)
	if d.lock != nil {
		_ = syscall.Flock(int(d.lock.Fd()), syscall.LOCK_UN)
		_ = d.lock.Close()
	}
}

// removeOwnedSocket never unlinks an unexpected replacement at a daemon path.
// It is used after the singleton lock is held, but same-UID processes are still
// allowed to alter their own runtime tree.
func removeOwnedSocket(path string) {
	info, err := os.Lstat(path)
	if err == nil && info.Mode()&os.ModeSymlink == 0 && info.Mode()&os.ModeSocket != 0 && ownedByCurrentUser(info) {
		_ = os.Remove(path)
	}
}

func (d *daemon) serve(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		d.cancel()
		_ = d.listener.Close()
	}()
	for {
		connection, err := d.listener.Accept()
		if err != nil {
			if d.ctx.Err() != nil || ctx.Err() != nil {
				return context.Canceled
			}
			return err
		}
		d.mu.Lock()
		if d.closing {
			d.mu.Unlock()
			_ = connection.Close()
			continue
		}
		d.connections[connection] = struct{}{}
		d.wg.Add(1)
		d.mu.Unlock()
		go func() {
			defer func() {
				d.mu.Lock()
				delete(d.connections, connection)
				d.mu.Unlock()
				d.wg.Done()
			}()
			defer connection.Close()
			d.handle(connection)
		}()
	}
}

func (d *daemon) handle(connection net.Conn) {
	reader := bufio.NewReader(connection)
	encoder := json.NewEncoder(connection)
	var encodeMu sync.Mutex
	encode := func(value any) error { encodeMu.Lock(); defer encodeMu.Unlock(); return encoder.Encode(value) }
	var hello daemonHello
	if err := readDaemonFrame(reader, &hello); err != nil || hello.Type != "hello" {
		_ = encoder.Encode(daemonHelloReply{Type: "hello_error", Error: &errorBody{Category: "transport", Code: "daemon_protocol_invalid", Message: "expected daemon hello", Action: "use a compatible Wirecmd client"}})
		return
	}
	if hello.Protocol != daemonProtocol {
		_ = encoder.Encode(daemonHelloReply{Type: "hello_error", Error: &errorBody{Category: "transport", Code: "daemon_incompatible", Message: "Wirecmd daemon protocol is incompatible", Action: "restart or upgrade the Wirecmd daemon"}})
		return
	}
	if err := encoder.Encode(daemonHelloReply{Type: "hello_ok", Protocol: daemonProtocol}); err != nil {
		return
	}
	var request daemonRequest
	if err := readDaemonFrame(reader, &request); err != nil {
		_ = encoder.Encode(daemonReply{Error: &errorBody{Category: "invocation", Code: "daemon_request_invalid", Message: "invalid daemon request", Action: "use a compatible Wirecmd client"}, ExitCode: exitInvocation})
		return
	}
	requestCtx, cancel := d.requestContext(reader)
	reply := d.execute(requestCtx, request, func(raw string) error { return encode(daemonReply{Type: "authorization_url", URL: raw}) })
	// Once the operation has completed, do not let the connection watcher's
	// eventual EOF affect the completed response. Closing the connection on
	// return releases that watcher.
	cancel()
	_ = encode(reply)
}

// readDaemonFrame preserves the private protocol's one-line framing without
// leaving a json.Decoder read-ahead buffer between the request parser and the
// disconnect watcher. The watcher must observe peer EOF promptly to cancel the
// daemon-derived upstream context.
func readDaemonFrame(reader *bufio.Reader, target any) error {
	frame, err := reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	return decodeJSON(frame, target)
}

// requestContext is canceled both when the daemon shuts down and when the
// client disconnects before its request completes. The daemon receives only
// one request per connection, so after the request has been decoded it is safe
// to watch the same buffered read side solely for peer closure while writing
// the response.
func (d *daemon) requestContext(reader io.Reader) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithCancel(d.ctx)
	go func() {
		var discarded [1]byte
		for {
			if count, err := reader.Read(discarded[:]); err != nil || count != 0 {
				cancel()
				return
			}
		}
	}()
	return ctx, cancel
}

func daemonDial() (net.Conn, *appError) {
	_, socket, _, appErr := runtimePaths()
	if appErr != nil {
		return nil, appErr
	}
	connection, err := net.Dial("unix", socket)
	if err != nil {
		return nil, daemonUnavailable()
	}
	return connection, nil
}

func daemonRequestCall(ctx context.Context, request daemonRequest, errOut io.Writer) (any, *appError, bool) {
	client, appErr := openDaemonClient(ctx)
	if appErr != nil {
		return nil, appErr, false
	}
	defer client.Close()
	return daemonRequestCallWithClient(client, request, errOut)
}

func daemonRequestCallWithClient(client *daemonClient, request daemonRequest, errOut io.Writer) (any, *appError, bool) {
	reply, appErr := client.request(request)
	if appErr != nil {
		return nil, appErr, false
	}
	for _, warning := range reply.Warnings {
		fmt.Fprintln(errOut, "wirecmd warning:", warning)
	}
	if reply.Error != nil {
		appErr := appErrorFromBody(reply.Error, reply.ExitCode)
		if len(reply.Result) != 0 {
			var result toolResult
			if err := decodeJSON(reply.Result, &result); err == nil {
				appErr.result = &result
			}
		}
		return nil, appErr, false
	}
	var result any
	decoder := json.NewDecoder(strings.NewReader(string(reply.Result)))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, transportError("daemon_response_invalid", err.Error(), "restart the Wirecmd daemon"), false
	}
	return result, nil, false
}

func runDaemonAdmin(ctx context.Context, command string, errOut io.Writer) (any, *appError) {
	reply, appErr := daemonRoundTrip(ctx, daemonRequest{Admin: command})
	if appErr != nil {
		return nil, appErr
	}
	for _, warning := range reply.Warnings {
		fmt.Fprintln(errOut, "wirecmd warning:", warning)
	}
	if reply.Error != nil {
		return nil, appErrorFromBody(reply.Error, reply.ExitCode)
	}
	var result any
	decoder := json.NewDecoder(strings.NewReader(string(reply.Result)))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, transportError("daemon_response_invalid", err.Error(), "restart the Wirecmd daemon")
	}
	return result, nil
}

func daemonRoundTrip(ctx context.Context, request daemonRequest) (daemonReply, *appError) {
	client, appErr := openDaemonClient(ctx)
	if appErr != nil {
		return daemonReply{}, appErr
	}
	defer client.Close()
	return client.request(request)
}

type daemonClient struct {
	connection net.Conn
	decoder    *json.Decoder
	encoder    *json.Encoder
	stopCancel func() bool
	event      func(string)
}

func (c *daemonClient) Close() error {
	if c.stopCancel != nil {
		c.stopCancel()
	}
	return c.connection.Close()
}

func openDaemonClient(ctx context.Context) (*daemonClient, *appError) {
	connection, appErr := daemonDial()
	if appErr != nil {
		return nil, appErr
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = connection.SetDeadline(deadline)
	}
	stopCancel := context.AfterFunc(ctx, func() { cancelDaemonRequest(connection) })
	encoder := json.NewEncoder(connection)
	decoder := json.NewDecoder(bufio.NewReader(connection))
	decoder.UseNumber()
	if err := encoder.Encode(clientDaemonHello()); err != nil {
		stopCancel()
		_ = connection.Close()
		return nil, daemonUnavailable()
	}
	var hello daemonHelloReply
	if err := decoder.Decode(&hello); err != nil {
		stopCancel()
		_ = connection.Close()
		return nil, daemonUnavailable()
	}
	if hello.Type != "hello_ok" || hello.Protocol != daemonProtocol {
		stopCancel()
		_ = connection.Close()
		if hello.Error != nil {
			return nil, appErrorFromBody(hello.Error, exitTransport)
		}
		return nil, transportError("daemon_incompatible", "Wirecmd daemon protocol is incompatible", "restart or upgrade the Wirecmd daemon")
	}
	return &daemonClient{connection: connection, decoder: decoder, encoder: encoder, stopCancel: stopCancel}, nil
}

// cancelDaemonRequest sends a private lifecycle control byte before closing the
// Unix write side, then unblocks this client's pending response read. The
// daemon accepts one semantic request only; any subsequent input cancels that
// request. Relying solely on peer EOF is racy with a blocked Streamable HTTP
// request because the local caller must be released before the daemon writes a
// response.
func cancelDaemonRequest(connection net.Conn) {
	if unixConnection, ok := connection.(*net.UnixConn); ok {
		_, _ = unixConnection.Write([]byte(`{"type":"cancel"}` + "\n"))
		_ = unixConnection.CloseWrite()
		_ = unixConnection.SetReadDeadline(time.Now())
		return
	}
	_ = connection.Close()
}

func (c *daemonClient) request(request daemonRequest) (daemonReply, *appError) {
	if err := c.encoder.Encode(request); err != nil {
		return daemonReply{}, transportError("daemon_request_failed", err.Error(), "restart the Wirecmd daemon")
	}
	for {
		var reply daemonReply
		if err := c.decoder.Decode(&reply); err != nil {
			return daemonReply{}, transportError("daemon_response_failed", err.Error(), "restart the Wirecmd daemon")
		}
		if reply.Type == "authorization_url" {
			if c.event != nil {
				c.event(reply.URL)
			}
			continue
		}
		return reply, nil
	}
}

func (d *daemon) execute(ctx context.Context, request daemonRequest, emitURL func(string) error) daemonReply {
	if request.Admin != "" {
		return d.executeAdmin(request.Admin)
	}
	if request.Operation != listServers && request.Operation != listTools && request.Operation != callTool && request.Operation != inspectTool && request.Operation != listResources && request.Operation != listResourceTemplates && request.Operation != readResource && request.Operation != navigateLSP && request.Operation != inspectLSP && request.Operation != statusLSP {
		return errorReply(invocationError("daemon_operation_invalid", "invalid daemon operation", "use a compatible Wirecmd client"))
	}
	if request.Operation == readResource && !validResourceURI(request.URI) {
		return errorReply(invocationError("resource_uri_invalid", "resource reads require a valid absolute URI", "supply a URI with a non-empty scheme"))
	}
	if !filepath.IsAbs(request.CWD) || filepath.Clean(request.CWD) != request.CWD || len(request.Configs) == 0 {
		return errorReply(configurationError("daemon_context_invalid", "daemon requests require absolute caller context and configuration paths", "use the Wirecmd CLI"))
	}
	if request.Operation != listServers {
		if !filepath.IsAbs(request.ProjectRoot) || filepath.Clean(request.ProjectRoot) != request.ProjectRoot {
			return errorReply(configurationError("daemon_context_invalid", "daemon requests require an absolute clean project root", "use the Wirecmd CLI"))
		}
	}
	if request.GlobalRoot != "" && (!filepath.IsAbs(request.GlobalRoot) || filepath.Clean(request.GlobalRoot) != request.GlobalRoot) {
		return errorReply(configurationError("daemon_context_invalid", "daemon global root must be absolute and clean", "use the Wirecmd CLI"))
	}
	if request.ProviderRoot != "" && (!filepath.IsAbs(request.ProviderRoot) || filepath.Clean(request.ProviderRoot) != request.ProviderRoot) {
		return errorReply(configurationError("daemon_context_invalid", "daemon provider root must be absolute and clean", "use the Wirecmd CLI"))
	}
	for _, path := range []string{request.TrustedBoundary, request.WorkspaceConfig, request.WorkspaceDirectory} {
		if path != "" && (!filepath.IsAbs(path) || filepath.Clean(path) != path) {
			return errorReply(configurationError("daemon_context_invalid", "daemon discovery context paths must be absolute and clean", "use the Wirecmd CLI"))
		}
	}
	for _, path := range request.Configs {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return errorReply(configurationError("daemon_context_invalid", "daemon configuration paths must be absolute and clean", "use the Wirecmd CLI"))
		}
	}
	if appErr := validateDaemonDiscoveryContext(request); appErr != nil {
		return errorReply(appErr)
	}
	for scope, input := range request.SecretScopes {
		if scope != string(config.ScopeWorkspace) && scope != string(config.ScopeGlobal) {
			return errorReply(configurationError("daemon_context_invalid", "daemon secret inputs use an unsupported scope", "use the Wirecmd CLI"))
		}
		for _, path := range input.SecretStores {
			if !filepath.IsAbs(path) || filepath.Clean(path) != path {
				return errorReply(configurationError("daemon_context_invalid", "daemon secret-store paths must be absolute and clean", "use the Wirecmd CLI"))
			}
		}
	}
	generation := d.currentGeneration()
	key := daemonConfigKey(request.Configs, request.Discovered, generation)
	d.mu.Lock()
	cached := d.configs[key]
	d.mu.Unlock()
	if cached == nil {
		source := configContext{CWD: request.CWD, Configs: request.Configs, Discovered: request.Discovered, TrustedBoundary: request.TrustedBoundary, WorkspaceConfig: request.WorkspaceConfig, WorkspaceDirectory: request.WorkspaceDirectory}
		loaded, err := loadContextConfig(source)
		if err != nil {
			return errorReply(configurationError("config_invalid", err.Error(), "correct the supplied KDL configuration"))
		}
		cached = &daemonConfig{config: loaded.Config, declaredRoot: loaded.DeclaredRoot, fingerprint: configFingerprint(loaded.Config)}
		d.mu.Lock()
		if d.closing || d.generation != generation {
			d.mu.Unlock()
			return errorReply(transportError("instance_retired", "the daemon configuration generation changed while loading configuration", "retry after daemon reload completes"))
		}
		// A concurrent loader may have populated the same context; either
		// semantic equivalent config is safe because no watcher exists.
		if current := d.configs[key]; current != nil {
			cached = current
		} else {
			d.configs[key] = cached
		}
		d.mu.Unlock()
	}
	warnings := []string(nil)
	if request.Fingerprint != cached.fingerprint {
		warnings = []string{"configuration differs from the daemon cache; run wirecmd daemon reload to apply it"}
	}
	if request.Operation == listServers {
		return resultReply(serverList(cached.config), warnings)
	}
	if request.Operation == navigateLSP || request.Operation == inspectLSP || request.Operation == statusLSP {
		return d.executeLSP(ctx, request, cached, generation, warnings)
	}
	server, ok := findServer(cached.config, request.Server)
	if !ok {
		return errorReplyWithWarnings(configurationError("config_mismatch", fmt.Sprintf("cached configuration has no server %q", request.Server), "run wirecmd daemon reload and retry"), warnings)
	}
	expectedRoot := daemonExpectedProjectRoot(request, cached)
	providerContext := invocationContext{configContext: configContext{CWD: request.CWD}, ProjectRoot: expectedRoot, GlobalRoot: request.GlobalRoot}
	runtimeContext, runtimeErr := serverRuntimeContext(server, providerContext)
	if runtimeErr != nil {
		return errorReplyWithWarnings(runtimeErr, warnings)
	}
	server, runtimeErr = materializeServer(server, providerContext)
	if runtimeErr != nil {
		return errorReplyWithWarnings(runtimeErr, warnings)
	}
	if request.Fingerprint != cached.fingerprint && request.Execution != serverExecutionFingerprint(server, nil, runtimeContext.Root, cached.config.Secrets) {
		return errorReplyWithWarnings(configurationError("config_mismatch", "selected server execution configuration differs from the daemon cache", "run wirecmd daemon reload and retry"), warnings)
	}
	if request.ProviderRoot != runtimeContext.Root || request.ScopeOwner != runtimeContext.Owner {
		return errorReplyWithWarnings(configurationError("daemon_context_invalid", "daemon provider scope context does not match the selected server", "use the Wirecmd CLI"), warnings)
	}
	scopeInput, ok := request.SecretScopes[string(server.Scope)]
	if !ok {
		return errorReplyWithWarnings(configurationError("daemon_context_invalid", "daemon request is missing scope-separated secret inputs", "use the Wirecmd CLI"), warnings)
	}
	selectedValues := serverSecretValues(server)
	if appErr := validateScopedSecretStores(server.Scope, request.GlobalRoot, selectedValues, scopeInput.SecretStores); appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	if err := validateSecretInputs(server, scopeInput.EnvSecrets); err != nil {
		return errorReplyWithWarnings(err, warnings)
	}
	envLookup := func(name string) (string, bool) {
		value, ok := scopeInput.EnvSecrets[name]
		return value.Value, ok && value.Present
	}
	ageReferences := selectedAgeReferences(selectedValues)
	var instance *retainedInstance
	var metadataKey, snapshotIdentity string
	var storeSnapshot *secretpkg.StoreSnapshot
	if request.Auth == "" && len(ageReferences) != 0 {
		var snapshotErr *appError
		storeSnapshot, snapshotErr = captureAgeStores(scopeInput.SecretStores, request.Discovered && server.Scope == config.ScopeWorkspace)
		if snapshotErr != nil {
			return errorReplyWithWarnings(snapshotErr, warnings)
		}
		snapshotIdentity = storeSnapshot.Identity()
		metadataKey = d.serverSecretCacheKey(server, runtimeContext.Owner, nil, runtimeContext.Root, cached.config.Secrets, scopeInput.EnvSecrets, ageReferences, scopeInput.SecretStores, generation)
		var reused bool
		instance, reused, _ = d.cachedInstance(ctx, metadataKey, snapshotIdentity, server.Name, generation, false)
		if !reused {
			instance = nil
		}
	}
	var resolved resolvedSecretSet
	if instance == nil || request.Auth != "" {
		var resolveErr *appError
		resolved, resolveErr = resolveSelectedSecrets(ctx, server.Scope, cached.config.Secrets, scopeInput.SecretStores, request.Discovered && server.Scope == config.ScopeWorkspace, storeSnapshot, selectedValues, envLookup)
		if resolveErr != nil {
			return errorReplyWithWarnings(resolveErr, warnings)
		}
	}
	if request.Auth != "" {
		return d.executeAuth(ctx, request, server, nil, resolved.lookup, warnings, emitURL)
	}
	if instance == nil {
		var appErr *appError
		instance, appErr = d.acquire(ctx, server, runtimeContext.Owner, nil, runtimeContext.Root, resolved, generation, request.Interactive, emitURL)
		if appErr != nil {
			return errorReplyWithWarnings(appErr, warnings)
		}
		if metadataKey != "" {
			auth := d.resolvedAuthIdentity(server, resolved.lookup)
			d.rememberSecretPools(metadataKey, snapshotIdentity, map[string]string{server.Name: mcpPoolKey(server, runtimeContext.Owner, nil, runtimeContext.Root, generation, auth)})
		}
	}
	defer d.release(instance)
	if instance.oauthRun != nil {
		current, err := instance.oauthRun.CredentialsCurrent()
		if err != nil {
			return errorReplyWithWarnings(oauthStoreError(err), warnings)
		}
		if !current {
			d.retireOAuthCredential(instance.oauthRun.flowKey())
			return errorReplyWithWarnings(transportError("instance_retired", "the stored OAuth credential changed since this session started", "retry to create a session with the current credential"), warnings)
		}
	}
	instance.mu.Lock()
	defer instance.mu.Unlock()
	if ctx.Err() != nil {
		return errorReplyWithWarnings(transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request"), warnings)
	}
	if instance.closed || instance.session == nil || instance.broken {
		return errorReplyWithWarnings(transportError("instance_unavailable", "the retained server instance is unavailable", "run wirecmd daemon reload or restart the daemon"), warnings)
	}
	var result any
	if request.Operation == listTools {
		catalog, appErr := sessionToolCatalog(ctx, instance.session, instance.redactor)
		if appErr != nil {
			d.noteSDKOperation(instance, appErr)
			return errorReplyWithWarnings(appErr.redacted(instance.redactor), warnings)
		}
		instance.toolsPrimed = true
		if ctx.Err() != nil {
			return errorReplyWithWarnings(transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request"), warnings)
		}
		if request.Help == serverHelp {
			result = helpResponse{Kind: serverHelp, Server: server.Name, Tools: catalog.summaries}
		} else {
			result = toolsEnvelope{OK: true, Server: server.Name, Tools: catalog.summaries}
		}
	} else if request.Operation == inspectTool {
		description, appErr := sessionToolDescription(ctx, instance.session, request.Tool, instance.redactor)
		if appErr != nil {
			d.noteSDKOperation(instance, appErr)
			return errorReplyWithWarnings(appErr.redacted(instance.redactor), warnings)
		}
		if ctx.Err() != nil {
			return errorReplyWithWarnings(transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request"), warnings)
		}
		result = helpResponse{Kind: toolHelp, Server: server.Name, Tool: description}
	} else if request.Operation == listResources {
		resources, appErr := sessionResources(ctx, instance.session, instance.redactor)
		if appErr != nil {
			d.noteSDKOperation(instance, appErr)
			return errorReplyWithWarnings(appErr.redacted(instance.redactor), warnings)
		}
		result = resourcesEnvelope{OK: true, Server: server.Name, Resources: resources}
	} else if request.Operation == listResourceTemplates {
		templates, appErr := sessionResourceTemplates(ctx, instance.session, instance.redactor)
		if appErr != nil {
			d.noteSDKOperation(instance, appErr)
			return errorReplyWithWarnings(appErr.redacted(instance.redactor), warnings)
		}
		result = resourceTemplatesEnvelope{OK: true, Server: server.Name, ResourceTemplates: templates}
	} else if request.Operation == readResource {
		contents, appErr := sessionResource(ctx, instance.session, request.URI, instance.redactor)
		if appErr != nil {
			d.noteSDKOperation(instance, appErr)
			return errorReplyWithWarnings(appErr.redacted(instance.redactor), warnings)
		}
		result = resourceEnvelope{OK: true, Server: server.Name, URI: redactedResourceIdentifier(request.URI, instance.redactor, false), Contents: contents}
	} else {
		arguments := request.Arguments
		if len(request.Projected) != 0 || request.Overlay != nil {
			var description, helpDescription toolDescription
			var appErr *appError
			if wantsToolHelpFallback(request.Projected, request.Overlay) {
				definition, findErr := sessionTool(ctx, instance.session, request.Tool)
				if findErr != nil {
					appErr = findErr
				} else {
					description, appErr = projectionDescription(definition)
					if appErr == nil && !hasProjectedArgument(description, "help") {
						helpDescription, appErr = displayDescription(definition, instance.redactor)
					}
				}
			} else {
				description, appErr = sessionToolProjection(ctx, instance.session, request.Tool)
			}
			if appErr != nil {
				d.noteSDKOperation(instance, appErr)
				return errorReplyWithWarnings(appErr.redacted(instance.redactor), warnings)
			}
			if ctx.Err() != nil {
				return errorReplyWithWarnings(transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request"), warnings)
			}
			if wantsToolHelpFallback(request.Projected, request.Overlay) && !hasProjectedArgument(description, "help") {
				return resultReply(helpResponse{Kind: toolHelp, Server: server.Name, Tool: helpDescription}, warnings)
			}
			arguments, appErr = resolveProjectedArguments(description, request.Projected, request.Overlay)
			if appErr != nil {
				return errorReplyWithWarnings(appErr.redacted(instance.redactor), warnings)
			}
		}
		if instance.breakOnRequestCancel && !instance.toolsPrimed {
			_, appErr := primeSessionTools(ctx, instance.session, instance.redactor)
			if appErr != nil {
				d.noteSDKOperation(instance, appErr)
				return errorReplyWithWarnings(appErr.redacted(instance.redactor), warnings)
			}
			instance.toolsPrimed = true
		}
		if ctx.Err() != nil {
			return errorReplyWithWarnings(transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request"), warnings)
		}
		call, appErr := sessionCall(ctx, instance.session, request.Tool, arguments, instance.redactor)
		if appErr != nil {
			d.noteSDKOperation(instance, appErr)
			appErr = appErr.redacted(instance.redactor)
			return errorReplyWithWarnings(&appError{category: appErr.category, code: appErr.code, message: appErr.message, action: appErr.action, exitCode: appErr.exitCode, result: appErr.result}, warnings)
		}
		if ctx.Err() != nil {
			return errorReplyWithWarnings(transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request"), warnings)
		}
		result = callEnvelope{OK: true, Server: server.Name, Tool: request.Tool, Result: call}
	}
	return resultReply(result, warnings)
}

func validateDaemonDiscoveryContext(request daemonRequest) *appError {
	if !request.Discovered {
		if request.TrustedBoundary != "" || request.WorkspaceConfig != "" || request.WorkspaceDirectory != "" {
			return configurationError("daemon_context_invalid", "explicit configuration cannot carry automatic-discovery context", "use the Wirecmd CLI")
		}
		return nil
	}
	if (request.WorkspaceConfig == "") != (request.WorkspaceDirectory == "") {
		return configurationError("daemon_context_invalid", "workspace configuration and directory must be supplied together", "use the Wirecmd CLI")
	}
	if request.TrustedBoundary != "" && !pathContains(request.TrustedBoundary, request.CWD) {
		return configurationError("daemon_context_invalid", "trusted boundary must contain the caller CWD", "use the Wirecmd CLI")
	}
	if request.WorkspaceConfig == "" {
		return nil
	}
	if filepath.Base(request.WorkspaceConfig) != "config.kdl" || filepath.Base(filepath.Dir(request.WorkspaceConfig)) != ".wirecmd" || filepath.Dir(filepath.Dir(request.WorkspaceConfig)) != request.WorkspaceDirectory || !pathContains(request.WorkspaceDirectory, request.CWD) {
		return configurationError("daemon_context_invalid", "workspace configuration does not match its discovered directory", "use the Wirecmd CLI")
	}
	found := false
	for _, path := range request.Configs {
		if path == request.WorkspaceConfig {
			found = true
			break
		}
	}
	if !found {
		return configurationError("daemon_context_invalid", "workspace configuration is not an ordered configuration source", "use the Wirecmd CLI")
	}
	if request.TrustedBoundary != "" && !pathContains(request.TrustedBoundary, request.WorkspaceDirectory) {
		return configurationError("daemon_context_invalid", "workspace configuration is outside the trusted boundary", "use the Wirecmd CLI")
	}
	return nil
}

func (d *daemon) executeLSP(ctx context.Context, request daemonRequest, cached *daemonConfig, generation uint64, warnings []string) daemonReply {
	workspace := daemonExpectedProjectRoot(request, cached)
	providerContext := invocationContext{configContext: configContext{CWD: request.CWD}, ProjectRoot: workspace, GlobalRoot: request.GlobalRoot}
	if request.Operation == statusLSP {
		if request.LSPFile != "" && !filepath.IsAbs(request.LSPFile) {
			return errorReplyWithWarnings(invocationError("lsp_file_invalid", "daemon LSP status requires an absolute file", "use the Wirecmd CLI"), warnings)
		}
		definitions, appErr := materializeLSPStatusDefinitions(cached.config.LSPs, providerContext)
		if appErr != nil {
			return errorReplyWithWarnings(appErr, warnings)
		}
		// Status displays only the resolved executable. Full best-effort
		// materialization is used solely to identify an exactly matching retained
		// instance; unavailable unused context leaves the provider disconnected.
		runtimeDefinitions := make([]config.LSP, 0, len(cached.config.LSPs))
		for _, definition := range cached.config.LSPs {
			materialized, materializeErr := materializeLSP(definition, providerContext)
			if materializeErr == nil {
				runtimeDefinitions = append(runtimeDefinitions, materialized)
			}
		}
		runtime := d.lspRuntimeStatuses(runtimeDefinitions, providerContext, generation)
		return resultReply(makeLSPStatusEnvelope(definitions, providerContext, request.LSPFile, runtime), warnings)
	}
	if !isLSPNavigation(request.LSPOperation) && !isLSPInspection(request.LSPOperation) {
		return errorReplyWithWarnings(invocationError("lsp_request_invalid", "daemon LSP request has an unsupported operation", "use a compatible Wirecmd CLI"), warnings)
	}
	if (request.Operation == navigateLSP) != isLSPNavigation(request.LSPOperation) || (request.Operation == inspectLSP) != isLSPInspection(request.LSPOperation) {
		return errorReplyWithWarnings(invocationError("lsp_request_invalid", "daemon LSP operation family does not match its request", "use a compatible Wirecmd CLI"), warnings)
	}
	if isLSPFileOperation(request.LSPOperation) && !filepath.IsAbs(request.LSPFile) {
		return errorReplyWithWarnings(invocationError("lsp_request_invalid", "daemon LSP file operations require an absolute file", "use the Wirecmd CLI"), warnings)
	}
	if isLSPPositionOperation(request.LSPOperation) && (request.LSPLine < 1 || request.LSPColumn < 1 || uint64(request.LSPLine) > math.MaxUint32 || uint64(request.LSPColumn) > math.MaxUint32) {
		return errorReplyWithWarnings(invocationError("lsp_request_invalid", "daemon LSP position operations require positive line and column", "use the Wirecmd CLI"), warnings)
	}
	if request.LSPOperation == lspWorkspaceSymbols && !request.LSPQuerySet {
		return errorReplyWithWarnings(invocationError("lsp_request_invalid", "daemon workspace symbol requests require an explicit query", "use the Wirecmd CLI"), warnings)
	}
	var matches []lspMatch
	var appErr *appError
	if request.LSPOperation == lspWorkspaceSymbols {
		matches, appErr = allLSPDefinitionsForContext(cached.config.LSPs, providerContext)
	} else {
		matches, appErr = matchLSPDefinitionsForContext(cached.config.LSPs, providerContext, request.LSPFile)
	}
	if appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	matches, appErr = materializeLSPMatches(matches, providerContext)
	if appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	if request.Fingerprint != cached.fingerprint && request.Execution != matchedLSPExecutionFingerprint(matches, nil, workspace, cached.config.Secrets) {
		return errorReplyWithWarnings(configurationError("config_mismatch", "selected LSP execution configuration differs from the daemon cache", "run wirecmd daemon reload and retry"), warnings)
	}
	instances := make([]*retainedInstance, len(matches))
	type lspScopeState struct {
		matches          []lspMatch
		input            secretScopeInput
		resolved         resolvedSecretSet
		metadataKey      string
		snapshotIdentity string
		allCached        bool
		newPoolKeys      map[string]string
	}
	states := make(map[config.Scope]*lspScopeState)
	ageSession := secretpkg.NewAgeSession()
	releaseSetupInstances := true
	defer func() {
		if !releaseSetupInstances {
			return
		}
		for _, instance := range instances {
			if instance != nil {
				d.release(instance)
			}
		}
	}()
	groupedMatches := lspMatchesByScope(matches)
	for _, scope := range lspScopeOrder(groupedMatches) {
		scopedMatches := groupedMatches[scope]
		input, ok := request.SecretScopes[string(scope)]
		if !ok {
			return errorReplyWithWarnings(configurationError("daemon_context_invalid", "daemon request is missing scope-separated LSP secret inputs", "use the Wirecmd CLI"), warnings)
		}
		for _, match := range scopedMatches {
			if appErr := validateLSPSecretInputs(match.Definition, input.EnvSecrets); appErr != nil {
				return errorReplyWithWarnings(appErr, warnings)
			}
		}
		state := &lspScopeState{matches: scopedMatches, input: input, newPoolKeys: map[string]string{}}
		selectedValues := lspSecretValues(scopedMatches)
		if appErr := validateScopedSecretStores(scope, request.GlobalRoot, selectedValues, input.SecretStores); appErr != nil {
			return errorReplyWithWarnings(appErr, warnings)
		}
		ageReferences := selectedAgeReferences(selectedValues)
		state.allCached = len(ageReferences) != 0
		var storeSnapshot *secretpkg.StoreSnapshot
		if state.allCached {
			var snapshotErr *appError
			storeSnapshot, snapshotErr = captureAgeStores(input.SecretStores, request.Discovered && scope == config.ScopeWorkspace)
			if snapshotErr != nil {
				return errorReplyWithWarnings(snapshotErr, warnings)
			}
			state.snapshotIdentity = storeSnapshot.Identity()
			state.metadataKey = d.lspSecretCacheKey(scopedMatches, nil, workspace, cached.config.Secrets, input.EnvSecrets, ageReferences, input.SecretStores, generation)
			for index, match := range matches {
				if normalizedScope(match.Definition.Scope) != scope {
					continue
				}
				instance, reused, _ := d.cachedInstance(ctx, state.metadataKey, state.snapshotIdentity, match.Definition.Name, generation, true)
				if !reused {
					state.allCached = false
					break
				}
				instances[index] = instance
			}
			if !state.allCached {
				for index, match := range matches {
					if normalizedScope(match.Definition.Scope) == scope && instances[index] != nil {
						d.release(instances[index])
						instances[index] = nil
					}
				}
			}
		}
		if !state.allCached {
			envLookup := func(name string) (string, bool) {
				value, ok := input.EnvSecrets[name]
				return value.Value, ok && value.Present
			}
			var resolveErr *appError
			state.resolved, resolveErr = resolveSelectedSecretsWithAgeSession(ctx, scope, cached.config.Secrets, input.SecretStores, request.Discovered && scope == config.ScopeWorkspace, storeSnapshot, selectedValues, envLookup, ageSession)
			if resolveErr != nil {
				return errorReplyWithWarnings(resolveErr, warnings)
			}
		}
		states[scope] = state
	}
	lspReq := lspRequest{Operation: request.LSPOperation, File: request.LSPFile, Line: request.LSPLine, Column: request.LSPColumn, Query: request.LSPQuery, QuerySet: request.LSPQuerySet, IncludeDeclaration: request.LSPIncludeDeclaration}
	if appErr := validateLSPRequestInput(request.LSPFile, lspReq, matches); appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	releaseSetupInstances = false
	results := make([]lspProviderRun, len(matches))
	var poolKeysMu sync.Mutex
	var wait sync.WaitGroup
	for index, match := range matches {
		wait.Add(1)
		go func(index int, match lspMatch) {
			defer wait.Done()
			state := states[normalizedScope(match.Definition.Scope)]
			instance := instances[index]
			if instance == nil {
				var appErr *appError
				instance, appErr = d.acquireLSP(ctx, match.Definition, match.Context.Owner, nil, match.Context.Root, state.resolved, generation)
				if appErr != nil {
					results[index].Err = appErr
					return
				}
				if state.metadataKey != "" {
					auth := d.lspResolvedAuthIdentity(match.Definition, state.resolved.lookup)
					poolKeysMu.Lock()
					state.newPoolKeys[match.Definition.Name] = lspPoolKey(match.Definition, match.Context.Owner, nil, match.Context.Root, generation, auth)
					poolKeysMu.Unlock()
				}
			}
			defer d.release(instance)
			instance.mu.Lock()
			defer instance.mu.Unlock()
			if ctx.Err() != nil {
				results[index].Err = transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request")
				return
			}
			if instance.closed || instance.lspSession == nil || instance.broken {
				results[index].Err = transportError("lsp_instance_unavailable", "the retained LSP instance is unavailable", "run wirecmd daemon reload or restart the daemon")
				return
			}
			err := callLSPRequest(ctx, instance.lspSession, request.LSPFile, lspReq, match.LanguageID, &results[index])
			if errors.Is(err, lspclient.ErrCapabilityUnavailable) {
				results[index].Unsupported = true
				return
			}
			if err != nil {
				mapped := lspOperationError(err, request.LSPOperation).redacted(instance.redactor)
				broken := instance.lspSession.Broken()
				if broken == nil && (errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed)) {
					broken = err
				}
				if broken != nil {
					d.markBroken(instance)
					mapped = transportError("lsp_instance_unavailable", broken.Error(), "run wirecmd daemon reload or restart the daemon").redacted(instance.redactor)
				}
				results[index].Err = sanitizeLSPError(mapped, match.Definition, request.LSPOperation)
				return
			}
			redactLSPProviderRun(&results[index], instance.redactor)
		}(index, match)
	}
	wait.Wait()
	for _, state := range states {
		if state.metadataKey != "" && !state.allCached && len(state.newPoolKeys) == len(state.matches) {
			d.rememberSecretPools(state.metadataKey, state.snapshotIdentity, state.newPoolKeys)
		}
	}
	result, appErr := aggregateLSPRequestResults(lspReq, workspace, request.LSPFile, matches, results)
	if appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	return resultReply(result, warnings)
}

func (d *daemon) lspRuntimeStatuses(definitions []config.LSP, context invocationContext, generation uint64) map[string]lspRuntimeStatus {
	wanted := make(map[string]struct {
		root      string
		execution string
	}, len(definitions))
	for _, definition := range definitions {
		root := providerRootForScope(definition.Scope, context)
		wanted[definition.Name] = struct {
			root      string
			execution string
		}{root: root, execution: lspExecutionFingerprint(definition, nil, root)}
	}
	d.mu.Lock()
	type candidate struct {
		key      string
		name     string
		instance *retainedInstance
		broken   bool
	}
	candidates := make([]candidate, 0)
	for key, entry := range d.pools {
		if entry.instance == nil || entry.instance.lspSession == nil || entry.instance.lspGeneration != generation {
			continue
		}
		if desired, ok := wanted[entry.instance.lspName]; ok && entry.instance.lspWorkspace == desired.root && entry.instance.lspExecution == desired.execution {
			candidates = append(candidates, candidate{key: key, name: entry.instance.lspName, instance: entry.instance, broken: entry.broken})
		}
	}
	d.mu.Unlock()
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].key < candidates[j].key })
	result := make(map[string]lspRuntimeStatus, len(candidates))
	for _, candidate := range candidates {
		candidate.instance.mu.Lock()
		status := candidate.instance.lspSession.Status()
		candidate.instance.mu.Unlock()
		state := "connected"
		if candidate.broken {
			state = "broken"
		}
		capabilities := status.Capabilities
		if existing, ok := result[candidate.name]; ok && (existing.Status == "connected" || state == "broken") {
			continue
		}
		result[candidate.name] = lspRuntimeStatus{Status: state, ServerName: status.ServerName, ServerVersion: status.ServerVersion, Capabilities: &capabilities}
	}
	return result
}

func (d *daemon) currentGeneration() uint64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.generation
}

func daemonConfigKey(paths []string, discovered bool, generation uint64) string {
	return fmt.Sprintf("%d\x00%t\x00%s", generation, discovered, strings.Join(paths, "\x00"))
}

func daemonExpectedProjectRoot(request daemonRequest, cached *daemonConfig) string {
	declared := declaredProjectRoot(cached.declaredRoot)
	if declared != "" {
		return canonicalPath(declared)
	}
	return request.ProjectRoot
}

func validateSecretInputs(server config.Server, inputs map[string]secretInput) *appError {
	expected := selectedSecretInputs(server, func(string) (string, bool) { return "", false })
	for name := range expected {
		input, ok := inputs[name]
		if !ok || !input.Present {
			return configurationError("secret_not_available", fmt.Sprintf("configured environment variable %q is not set", name), "set the required environment variable before invoking Wirecmd")
		}
	}
	return nil
}

func validateLSPSecretInputs(definition config.LSP, inputs map[string]secretInput) *appError {
	expected := selectedLSPSecretInputs(definition, func(string) (string, bool) { return "", false })
	for name := range expected {
		input, ok := inputs[name]
		if !ok || !input.Present {
			return configurationError("secret_not_available", fmt.Sprintf("configured environment variable %q is not set", name), "set the required environment variable before invoking Wirecmd")
		}
	}
	return nil
}

func validateScopedSecretStores(scope config.Scope, globalRoot string, values []config.Value, stores []string) *appError {
	if normalizedScope(scope) != config.ScopeGlobal {
		return nil
	}
	expected := []string(nil)
	if len(selectedAgeReferences(values)) != 0 && globalRoot != "" {
		expected = []string{filepath.Join(globalRoot, "secrets.json.age")}
	}
	if len(stores) != len(expected) {
		return configurationError("daemon_context_invalid", "daemon global secret stores do not match the global scope", "use the Wirecmd CLI")
	}
	for index := range expected {
		if stores[index] != expected[index] {
			return configurationError("daemon_context_invalid", "daemon global secret stores do not match the global scope", "use the Wirecmd CLI")
		}
	}
	return nil
}

func lspPoolKey(definition config.LSP, owner string, root *config.Root, cwd string, generation uint64, auth string) string {
	workspace := cwd
	if root != nil {
		workspace = resolveRoot(*root)
	}
	return strings.Join([]string{"lsp", definition.Name, string(definition.Scope), owner, workspace, lspExecutionFingerprint(definition, root, cwd), fmt.Sprint(generation), auth}, "\x00")
}

func (d *daemon) acquireLSP(ctx context.Context, definition config.LSP, owner string, root *config.Root, cwd string, resolved resolvedSecretSet, generation uint64) (*retainedInstance, *appError) {
	key := lspPoolKey(definition, owner, root, cwd, generation, d.lspResolvedAuthIdentity(definition, resolved.lookup))
	d.mu.Lock()
	if d.closing || d.generation != generation {
		d.mu.Unlock()
		return nil, transportError("instance_retired", "the daemon configuration generation changed before the LSP instance started", "retry after daemon reload completes")
	}
	if entry := d.pools[key]; entry != nil {
		d.mu.Unlock()
		return d.waitForLSPInstance(ctx, entry, generation, false)
	}
	entry := &poolEntry{ready: make(chan struct{})}
	d.pools[key] = entry
	d.wg.Add(1)
	d.mu.Unlock()
	go d.startLSPInstance(entry, key, definition, root, cwd, resolved, generation)
	return d.waitForLSPInstance(ctx, entry, generation, true)
}

func (d *daemon) waitForLSPInstance(ctx context.Context, entry *poolEntry, generation uint64, owner bool) (*retainedInstance, *appError) {
	instance, appErr := d.waitForInstance(ctx, entry, generation, owner)
	if appErr != nil && appErr.code == "instance_unavailable" {
		return nil, transportError("lsp_instance_unavailable", "the retained LSP instance is unavailable", "run wirecmd daemon reload or restart the daemon")
	}
	return instance, appErr
}

func (d *daemon) startLSPInstance(entry *poolEntry, key string, definition config.LSP, root *config.Root, cwd string, resolved resolvedSecretSet, generation uint64) {
	defer d.wg.Done()
	command, secrets, appErr := makeStdioCommand(definition.Stdio, root, cwd, resolved.lookup)
	var started *retainedInstance
	if appErr == nil {
		redactor := newRedactor(secrets, d.stderr)
		workspace := cwd
		if root != nil {
			workspace = resolveRoot(*root)
		}
		session, err := lspclient.Start(d.ctx, lspclient.Command{Path: command.Path, Args: command.Args[1:], Env: command.Env, Dir: command.Dir, Stderr: redactor}, workspace, buildinfo.Version(), initializationOptions(definition))
		if err != nil {
			redactor.FlushTo(d.stderr)
			appErr = sanitizeLSPError(lspOperationError(err, "initialize").redacted(redactor), definition, "initialize")
		} else {
			started = &retainedInstance{lspSession: session, redactor: redactor, lspName: definition.Name, lspWorkspace: workspace, lspExecution: lspExecutionFingerprint(definition, root, cwd), lspGeneration: generation}
		}
	}
	d.mu.Lock()
	if appErr == nil && (entry.retiring || d.closing || d.generation != generation) {
		d.mu.Unlock()
		if started != nil {
			started.close()
		}
		d.mu.Lock()
		appErr = transportError("instance_retired", "the LSP instance was retired while starting", "retry after daemon reload completes")
	} else if appErr == nil {
		entry.instance = started
	}
	entry.err = appErr
	close(entry.ready)
	if appErr != nil && d.pools[key] == entry {
		delete(d.pools, key)
	}
	d.mu.Unlock()
}

func (d *daemon) lspAuthIdentity(definition config.LSP, inputs map[string]secretInput) string {
	lookup := func(reference string) (string, bool) {
		name, env := isEnvReference(reference)
		if !env {
			return "", false
		}
		input, ok := inputs[name]
		return input.Value, ok && input.Present
	}
	return d.lspResolvedAuthIdentity(definition, lookup)
}

func (d *daemon) lspResolvedAuthIdentity(definition config.LSP, lookup func(string) (string, bool)) string {
	mac := hmac.New(sha256.New, d.hmacKey)
	items := make([]string, 0)
	add := func(destination string, value config.Value) {
		if !value.IsSecret() {
			return
		}
		resolved, present := lookup(value.Text)
		items = append(items, fmt.Sprintf("%s\x00%t\x00%s", destination, present, resolved))
	}
	for index, value := range definition.Stdio.Args {
		add(fmt.Sprintf("arg[%d]", index), value)
	}
	for _, assignment := range definition.Stdio.Env {
		add("env["+assignment.Name+"]", assignment.Value)
	}
	sort.Strings(items)
	for _, item := range items {
		_, _ = fmt.Fprintf(mac, "%d:%s", len(item), item)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func captureAgeStores(paths []string, automatic bool) (*secretpkg.StoreSnapshot, *appError) {
	stores := make([]secretpkg.Store, len(paths))
	for index, path := range paths {
		stores[index] = secretpkg.Store{Path: path, RejectParentSymlink: automatic && filepath.Base(filepath.Dir(path)) == ".wirecmd"}
	}
	snapshot, err := secretpkg.CaptureStores(stores)
	if err != nil {
		return nil, secretResolutionError(err)
	}
	return snapshot, nil
}

func (d *daemon) serverSecretCacheKey(server config.Server, owner string, root *config.Root, cwd string, configured config.Secrets, inputs map[string]secretInput, references, stores []string, generation uint64) string {
	return strings.Join([]string{
		"mcp", fmt.Sprint(generation), owner, serverExecutionFingerprint(server, root, cwd, configured),
		d.authIdentity(server, inputs), fingerprint(map[string]any{"references": references, "stores": stores}),
	}, "\x00")
}

func (d *daemon) lspSecretCacheKey(matches []lspMatch, root *config.Root, cwd string, configured config.Secrets, inputs map[string]secretInput, references, stores []string, generation uint64) string {
	envIdentities := make([]string, len(matches))
	for index, match := range matches {
		envIdentities[index] = match.Definition.Name + "\x00" + d.lspAuthIdentity(match.Definition, inputs)
	}
	sort.Strings(envIdentities)
	return strings.Join([]string{
		"lsp", fmt.Sprint(generation), matchedLSPExecutionFingerprint(matches, root, cwd, configured),
		fingerprint(envIdentities), fingerprint(map[string]any{"references": references, "stores": stores}),
	}, "\x00")
}

func (d *daemon) cachedInstance(ctx context.Context, metadataKey, snapshot, name string, generation uint64, lsp bool) (*retainedInstance, bool, *appError) {
	d.mu.Lock()
	metadata, ok := d.secretCache[metadataKey]
	poolKey := metadata.poolKeys[name]
	entry := d.pools[poolKey]
	valid := ok && metadata.snapshot == snapshot && poolKey != "" && entry != nil && !entry.broken && !entry.retiring
	d.mu.Unlock()
	if !valid {
		return nil, false, nil
	}
	var instance *retainedInstance
	var appErr *appError
	if lsp {
		instance, appErr = d.waitForLSPInstance(ctx, entry, generation, false)
	} else {
		instance, appErr = d.waitForInstance(ctx, entry, generation, false)
	}
	if appErr != nil {
		d.mu.Lock()
		delete(d.secretCache, metadataKey)
		d.mu.Unlock()
		return nil, false, nil
	}
	return instance, true, nil
}

func (d *daemon) rememberSecretPools(metadataKey, snapshot string, poolKeys map[string]string) {
	toClose := make([]*retainedInstance, 0)
	displaced := map[string]struct{}{}
	d.mu.Lock()
	previous := d.secretCache[metadataKey]
	for name, previousKey := range previous.poolKeys {
		if nextKey := poolKeys[name]; previousKey != "" && previousKey != nextKey {
			d.retirePoolKeyLocked(previousKey, displaced, &toClose)
		}
	}
	copyKeys := make(map[string]string, len(poolKeys))
	for name, poolKey := range poolKeys {
		copyKeys[name] = poolKey
	}
	d.secretCache[metadataKey] = secretCacheEntry{snapshot: snapshot, poolKeys: copyKeys}
	d.mu.Unlock()
	for _, instance := range toClose {
		instance.mu.Lock()
		instance.close()
		instance.mu.Unlock()
		d.mu.Lock()
		delete(d.retiring, instance)
		d.mu.Unlock()
	}
}

func (d *daemon) retirePoolKeyLocked(key string, displaced map[string]struct{}, toClose *[]*retainedInstance) {
	if _, seen := displaced[key]; seen {
		return
	}
	displaced[key] = struct{}{}
	entry := d.pools[key]
	if entry == nil {
		return
	}
	delete(d.pools, key)
	entry.retiring = true
	if entry.instance == nil {
		return
	}
	entry.instance.retiring = true
	d.retiring[entry.instance] = struct{}{}
	if entry.instance.active == 0 {
		*toClose = append(*toClose, entry.instance)
	}
}

func mcpPoolKey(server config.Server, owner string, root *config.Root, cwd string, generation uint64, auth string) string {
	workspace := cwd
	if root != nil {
		workspace = resolveRoot(*root)
	}
	return strings.Join([]string{"mcp", server.Name, string(server.Scope), owner, workspace, executionFingerprint(server, root, cwd), fmt.Sprint(generation), auth}, "\x00")
}

func (d *daemon) acquire(ctx context.Context, server config.Server, owner string, root *config.Root, cwd string, resolved resolvedSecretSet, generation uint64, interactive bool, emitURL func(string) error) (*retainedInstance, *appError) {
	auth := d.resolvedAuthIdentity(server, resolved.lookup)
	key := mcpPoolKey(server, owner, root, cwd, generation, auth)
	d.mu.Lock()
	if d.closing || d.generation != generation {
		d.mu.Unlock()
		return nil, transportError("instance_retired", "the daemon configuration generation changed before the server instance started", "retry after daemon reload completes")
	}
	if entry := d.pools[key]; entry != nil {
		d.mu.Unlock()
		return d.waitForInstance(ctx, entry, generation, false)
	}
	entry := &poolEntry{ready: make(chan struct{})}
	if server.HTTP != nil && server.HTTP.Kind != config.HTTPTransportSSE && !hasAuthorizationHeader(*server.HTTP) {
		entry.authStarted = make(chan struct{})
	}
	d.pools[key] = entry
	d.wg.Add(1)
	d.mu.Unlock()
	go d.startInstance(ctx, entry, key, server, root, cwd, resolved, generation, interactive, emitURL)
	return d.waitForInstance(ctx, entry, generation, true)
}

// waitForInstance gives each daemon request its own cancellation boundary. The
// pool startup itself is daemon-owned shared work, so a departing first caller
// must not tear it down beneath other callers that have coalesced on it.
func (d *daemon) waitForInstance(ctx context.Context, entry *poolEntry, generation uint64, owner bool) (*retainedInstance, *appError) {
	if owner || entry.authStarted == nil {
		select {
		case <-entry.ready:
		case <-ctx.Done():
			return nil, transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request")
		}
	} else {
		select {
		case <-entry.ready:
		case <-entry.authStarted:
			// Startup may have completed while this select chose authStarted.
			// Recheck readiness before reporting an in-progress authorization.
			select {
			case <-entry.ready:
			default:
				return nil, userActionError("authorization_in_progress", "another request is completing OAuth authorization for this server", "retry after the current authorization finishes")
			}
		case <-ctx.Done():
			return nil, transportError("daemon_request_canceled", "daemon request was canceled by its client", "retry the request")
		}
	}
	d.mu.Lock()
	appErr := entry.err
	broken := entry.broken
	retiring := entry.retiring || d.closing || d.generation != generation
	instance := entry.instance
	if appErr == nil && !broken && !retiring && instance != nil {
		instance.active++
	}
	d.mu.Unlock()
	if appErr != nil {
		return nil, appErr
	}
	if retiring {
		return nil, transportError("instance_retired", "the server instance was retired while starting", "retry after daemon reload completes")
	}
	if broken || instance == nil {
		return nil, transportError("instance_unavailable", "the retained server instance is unavailable", "run wirecmd daemon reload or restart the daemon")
	}
	return instance, nil
}

func (d *daemon) startInstance(clientCtx context.Context, entry *poolEntry, key string, server config.Server, root *config.Root, cwd string, resolved resolvedSecretSet, generation uint64, interactive bool, emitURL func(string) error) {
	defer d.wg.Done()
	target, secrets, appErr := makeTarget(server, root, cwd, resolved.lookup)
	redactor := newRedactor(secrets, d.stderr)
	redactor.ProtectEndpoint(target.endpoint)
	if appErr == nil && server.HTTP != nil && server.HTTP.Kind != config.HTTPTransportSSE {
		var oauthSecrets []string
		target, oauthSecrets, appErr = attachOAuth(target, server.Name, *server.HTTP, resolved.lookup, interactive, false, func(raw string) {
			if emitURL != nil {
				_ = emitURL(redactor.Redact(raw))
			}
		})
		secrets = append(secrets, oauthSecrets...)
		redactor.ProtectSecrets(oauthSecrets...)
		if target.oauthRun != nil && entry.authStarted != nil {
			target.oauthRun.initialCtx = clientCtx
			target.oauthRun.onFlowStart = func() error {
				entry.authOnce.Do(func() { close(entry.authStarted) })
				return d.beginOAuthFlow(target.oauthRun)
			}
			target.oauthRun.onFlowEnd = func() { d.endOAuthFlow(target.oauthRun) }
		}
	}
	var started *retainedInstance
	if appErr == nil {
		if target.oauthRun != nil {
			target.oauthRun.setRedactor(redactor)
		}
		session, connectErr := connectTarget(d.ctx, target, redactor)
		if connectErr != nil {
			if target.oauthRun != nil {
				target.oauthRun.Close()
			}
			redactor.FlushTo(d.stderr)
			appErr = connectErr.redacted(redactor)
		} else {
			if target.oauthRun != nil {
				target.oauthRun.markConnected()
			}
			started = &retainedInstance{session: session, redactor: redactor, breakOnRequestCancel: target.endpoint != "", toolsPrimed: target.sse, oauthRun: target.oauthRun}
		}
	} else {
		redactor.FlushTo(d.stderr)
	}
	d.mu.Lock()
	if appErr == nil && (entry.retiring || d.closing || d.generation != generation) {
		d.mu.Unlock()
		if started != nil {
			started.close()
			started.redactor.FlushTo(d.stderr)
		}
		d.mu.Lock()
		appErr = transportError("instance_retired", "the server instance was retired while starting", "retry after daemon reload completes")
	} else if appErr == nil {
		entry.instance = started
	}
	entry.err = appErr
	close(entry.ready)
	if appErr != nil {
		if d.pools[key] == entry {
			delete(d.pools, key)
		}
	}
	d.mu.Unlock()
}

func (d *daemon) authIdentity(server config.Server, inputs map[string]secretInput) string {
	lookup := func(reference string) (string, bool) {
		name, env := isEnvReference(reference)
		if !env {
			return "", false
		}
		input, ok := inputs[name]
		return input.Value, ok && input.Present
	}
	return d.resolvedAuthIdentity(server, lookup)
}

func (d *daemon) resolvedAuthIdentity(server config.Server, lookup func(string) (string, bool)) string {
	items := make([]string, 0)
	seen := map[string]struct{}{}
	mac := hmac.New(sha256.New, d.hmacKey)
	add := func(destination string, value config.Value) {
		if !value.IsSecret() {
			return
		}
		if _, ok := seen[destination]; ok {
			return
		}
		seen[destination] = struct{}{}
		resolved, _ := lookup(value.Text)
		items = append(items, destination+"\x00"+resolved)
	}
	if server.HTTP != nil {
		for _, field := range server.HTTP.Query {
			add("query["+field.Name+"]", field.Value)
		}
		for _, field := range server.HTTP.Headers {
			add("header["+strings.ToLower(field.Name)+"]", field.Value)
		}
		if server.HTTP.OAuth != nil && server.HTTP.OAuth.ClientSecret != nil {
			add("oauth.client-secret", *server.HTTP.OAuth.ClientSecret)
		}
	} else {
		for i, value := range server.Stdio.Args {
			add(fmt.Sprintf("arg[%d]", i), value)
		}
		for _, env := range server.Stdio.Env {
			add("env["+env.Name+"]", env.Value)
		}
	}
	sort.Strings(items)
	for _, item := range items {
		_, _ = fmt.Fprintf(mac, "%d:%s", len(item), item)
	}
	return hex.EncodeToString(mac.Sum(nil))
}

func (d *daemon) release(instance *retainedInstance) {
	d.mu.Lock()
	instance.active--
	closeNow := instance.retiring && instance.active == 0
	d.mu.Unlock()
	if closeNow {
		instance.mu.Lock()
		instance.close()
		instance.mu.Unlock()
		d.mu.Lock()
		delete(d.retiring, instance)
		d.mu.Unlock()
	}
}

func (d *daemon) noteOperationError(instance *retainedInstance, appErr *appError) {
	if appErr.category != "transport" || appErr.code != "connection_closed" {
		return
	}
	d.markBroken(instance)
}

// noteSDKOperation keeps retained HTTP-session state honest after an SDK call
// that observed cancellation. The pinned SDK's stateless Streamable HTTP
// cancellation notification can poison the shared session for any outgoing
// request, not only tools/call. A request canceled before it acquires the
// instance does not reach this helper and leaves it reusable.
func (d *daemon) noteSDKOperation(instance *retainedInstance, appErr *appError) {
	if appErr == nil {
		return
	}
	d.noteOperationError(instance, appErr)
	if appErr.sdkCanceled && instance.breakOnRequestCancel {
		d.markBroken(instance)
	}
}

func (d *daemon) markBroken(instance *retainedInstance) {
	// execute holds instance.mu while this is called, so a waiter that obtains
	// that mutex after the failed operation will observe the broken state.
	instance.broken = true
	d.mu.Lock()
	for _, entry := range d.pools {
		if entry.instance == instance {
			entry.broken = true
		}
	}
	d.mu.Unlock()
}

func (d *daemon) executeAdmin(command string) daemonReply {
	switch command {
	case "status":
		d.mu.Lock()
		contexts := len(d.configs)
		active, broken := 0, 0
		for _, entry := range d.pools {
			if entry.broken {
				broken++
			}
			if entry.instance != nil {
				if !entry.instance.retiring {
					active++
				}
			}
		}
		generation := d.generation
		retiring := len(d.retiring)
		d.mu.Unlock()
		return resultReply(map[string]any{"ok": true, "daemon": map[string]any{"status": "running", "protocol": daemonProtocol, "pid": os.Getpid(), "generation": generation, "cached_contexts": contexts, "active_instances": active, "retiring_instances": retiring, "broken_instances": broken}}, nil)
	case "reload":
		d.mu.Lock()
		contexts := len(d.configs)
		instances := 0
		toClose := []*retainedInstance{}
		for _, entry := range d.pools {
			entry.retiring = true
			if entry.instance != nil {
				instances++
				entry.instance.retiring = true
				d.retiring[entry.instance] = struct{}{}
				if entry.instance.active == 0 {
					toClose = append(toClose, entry.instance)
				}
			}
		}
		d.configs = map[string]*daemonConfig{}
		d.pools = map[string]*poolEntry{}
		d.secretCache = map[string]secretCacheEntry{}
		d.generation++
		d.mu.Unlock()
		for _, instance := range toClose {
			instance.mu.Lock()
			instance.close()
			instance.mu.Unlock()
			d.mu.Lock()
			delete(d.retiring, instance)
			d.mu.Unlock()
		}
		return resultReply(map[string]any{"ok": true, "reload": map[string]any{"contexts_retired": contexts, "instances_retired": instances}}, nil)
	default:
		return errorReply(invocationError("daemon_admin_invalid", "invalid daemon administrative command", "use status or reload"))
	}
}

func resultReply(value any, warnings []string) daemonReply {
	encoded, err := json.Marshal(value)
	if err != nil {
		return errorReply(transportError("daemon_response_encode_failed", "failed to encode daemon response", "restart the Wirecmd daemon"))
	}
	return daemonReply{Result: encoded, Warnings: warnings}
}

func errorReply(appErr *appError) daemonReply { return errorReplyWithWarnings(appErr, nil) }

func errorReplyWithWarnings(appErr *appError, warnings []string) daemonReply {
	reply := daemonReply{Error: &errorBody{Category: appErr.category, Code: appErr.code, Message: appErr.message, Action: appErr.action, Details: appErr.details}, ExitCode: appErr.exitCode, Warnings: warnings}
	if appErr.result != nil {
		if encoded, err := json.Marshal(appErr.result); err == nil {
			reply.Result = encoded
		}
	}
	return reply
}
