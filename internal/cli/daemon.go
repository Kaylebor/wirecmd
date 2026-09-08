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
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const daemonProtocol = 8

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
	Operation             operation              `json:"operation"`
	Server                string                 `json:"server,omitempty"`
	Tool                  string                 `json:"tool,omitempty"`
	Arguments             map[string]any         `json:"arguments,omitempty"`
	Projected             []projectedArgument    `json:"projected,omitempty"`
	Overlay               map[string]any         `json:"overlay,omitempty"`
	Help                  helpKind               `json:"help,omitempty"`
	CWD                   string                 `json:"cwd"`
	Configs               []string               `json:"configs,omitempty"`
	Discovered            bool                   `json:"discovered,omitempty"`
	Fingerprint           string                 `json:"fingerprint,omitempty"`
	Execution             string                 `json:"execution_fingerprint,omitempty"`
	Secrets               map[string]secretInput `json:"secrets,omitempty"`
	Admin                 string                 `json:"admin,omitempty"`
	Auth                  string                 `json:"auth,omitempty"`
	Interactive           bool                   `json:"interactive,omitempty"`
	LSPFile               string                 `json:"lsp_file,omitempty"`
	LSPLine               int                    `json:"lsp_line,omitempty"`
	LSPColumn             int                    `json:"lsp_column,omitempty"`
	LSPQuery              string                 `json:"lsp_query,omitempty"`
	LSPQuerySet           bool                   `json:"lsp_query_set,omitempty"`
	LSPOperation          string                 `json:"lsp_operation,omitempty"`
	LSPIncludeDeclaration bool                   `json:"lsp_include_declaration,omitempty"`
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
		result = append(result, filepath.Clean(path))
	}
	return result, nil
}

func daemonRequestFromConfig(req request, cfg *config.Config, cwd string, paths []string, discovered bool, secrets map[string]secretInput) daemonRequest {
	request := daemonRequest{Operation: req.operation, Server: req.server, Tool: req.tool, Arguments: req.arguments, Projected: req.projected, Overlay: req.overlay, Help: req.help, CWD: cwd, Configs: paths, Discovered: discovered, Fingerprint: configFingerprint(cfg, cwd), Secrets: secrets}
	if req.operation != listServers {
		if server, ok := findServer(cfg, req.server); ok {
			request.Execution = executionFingerprint(server, cfg.Root, cwd)
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
		name := strings.TrimPrefix(value.Text, "env://")
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

// configFingerprint deliberately excludes provenance and resolved values, while
// retaining the resolved root because a root's declaring file changes execution
// semantics for relative paths.
func configFingerprint(cfg *config.Config, cwd string) string {
	servers := make([]any, 0, len(cfg.Servers))
	for _, server := range cfg.Servers {
		servers = append(servers, semanticServer(server))
	}
	root := cwd
	if cfg.Root != nil {
		root = resolveRoot(*cfg.Root)
	}
	lsps := make([]any, 0, len(cfg.LSPs))
	for _, definition := range cfg.LSPs {
		lsps = append(lsps, semanticLSP(definition))
	}
	return fingerprint(map[string]any{"v": 2, "root": root, "servers": servers, "lsps": lsps})
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
		definitions = append(definitions, []any{semanticLSPExecution(match.Definition), match.LanguageID})
	}
	return fingerprint(map[string]any{"v": 1, "root": resolvedRoot, "providers": definitions})
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
	return map[string]any{"name": definition.Name, "scope": definition.Scope, "selectors": selectors, "command": definition.Stdio.Command, "args": args, "env": env}
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
			oauth = map[string]any{"client_id": server.HTTP.OAuth.ClientID, "client_secret": secret, "redirect_uri": server.HTTP.OAuth.RedirectURI}
		}
		return map[string]any{"name": server.Name, "scope": server.Scope, "transport": "http", "endpoint": server.HTTP.Endpoint, "query": query, "headers": headers, "oauth": oauth}
	}
	args := make([]any, 0, len(server.Stdio.Args))
	for _, arg := range server.Stdio.Args {
		args = append(args, []any{arg.Kind, arg.Text})
	}
	env := make([]any, 0, len(server.Stdio.Env))
	for _, entry := range server.Stdio.Env {
		env = append(env, []any{entry.Name, entry.Value.Kind, entry.Value.Text})
	}
	return map[string]any{"name": server.Name, "scope": server.Scope, "transport": "stdio", "command": server.Stdio.Command, "args": args, "env": env}
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
}

type daemonConfig struct {
	config      *config.Config
	fingerprint string
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
	return &daemon{listener: listener, socket: socket, lock: lock, stderr: stderr, ctx: ctx, cancel: cancel, configs: map[string]*daemonConfig{}, pools: map[string]*poolEntry{}, retiring: map[*retainedInstance]struct{}{}, connections: map[net.Conn]struct{}{}, hmacKey: key, authFlows: map[string]struct{}{}}, nil
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
	if request.Operation != listServers && request.Operation != listTools && request.Operation != callTool && request.Operation != inspectTool && request.Operation != navigateLSP && request.Operation != inspectLSP && request.Operation != statusLSP {
		return errorReply(invocationError("daemon_operation_invalid", "invalid daemon operation", "use a compatible Wirecmd client"))
	}
	if !filepath.IsAbs(request.CWD) || len(request.Configs) == 0 {
		return errorReply(configurationError("daemon_context_invalid", "daemon requests require absolute caller context and configuration paths", "use the Wirecmd CLI"))
	}
	for _, path := range request.Configs {
		if !filepath.IsAbs(path) {
			return errorReply(configurationError("daemon_context_invalid", "daemon configuration paths must be absolute", "use the Wirecmd CLI"))
		}
	}
	generation := d.currentGeneration()
	key := daemonConfigKey(request.CWD, request.Configs, request.Discovered, generation)
	d.mu.Lock()
	cached := d.configs[key]
	d.mu.Unlock()
	if cached == nil {
		var loaded *config.Config
		var err error
		if request.Discovered {
			loaded, err = config.LoadEffectiveDiscovered(request.Configs)
		} else {
			loaded, err = config.LoadEffective(request.Configs)
		}
		if err != nil {
			return errorReply(configurationError("config_invalid", err.Error(), "correct the supplied KDL configuration"))
		}
		cached = &daemonConfig{config: loaded, fingerprint: configFingerprint(loaded, request.CWD)}
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
	if request.Fingerprint != cached.fingerprint && request.Execution != executionFingerprint(server, cached.config.Root, request.CWD) {
		return errorReplyWithWarnings(configurationError("config_mismatch", "selected server execution configuration differs from the daemon cache", "run wirecmd daemon reload and retry"), warnings)
	}
	if err := validateSecretInputs(server, request.Secrets); err != nil {
		return errorReplyWithWarnings(err, warnings)
	}
	if request.Auth != "" {
		return d.executeAuth(ctx, request, server, cached.config.Root, warnings, emitURL)
	}
	instance, appErr := d.acquire(ctx, server, cached.config.Root, request.CWD, request.Secrets, generation, request.Interactive, emitURL)
	if appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
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

func (d *daemon) executeLSP(ctx context.Context, request daemonRequest, cached *daemonConfig, generation uint64, warnings []string) daemonReply {
	workspace := effectiveLSPWorkspace(cached.config.Root, request.CWD)
	if request.Operation == statusLSP {
		if request.LSPFile != "" && !filepath.IsAbs(request.LSPFile) {
			return errorReplyWithWarnings(invocationError("lsp_file_invalid", "daemon LSP status requires an absolute file", "use the Wirecmd CLI"), warnings)
		}
		return resultReply(makeLSPStatusEnvelope(cached.config.LSPs, workspace, request.LSPFile, d.lspRuntimeStatuses(cached.config.LSPs, workspace, generation)), warnings)
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
		matches = allLSPDefinitions(cached.config.LSPs)
	} else {
		matches, appErr = matchLSPDefinitions(cached.config.LSPs, workspace, request.LSPFile)
		if appErr != nil {
			return errorReplyWithWarnings(appErr, warnings)
		}
	}
	if request.Fingerprint != cached.fingerprint && request.Execution != lspMatchesExecutionFingerprint(matches, cached.config.Root, request.CWD) {
		return errorReplyWithWarnings(configurationError("config_mismatch", "selected LSP execution configuration differs from the daemon cache", "run wirecmd daemon reload and retry"), warnings)
	}
	for _, match := range matches {
		if appErr := validateLSPSecretInputs(match.Definition, request.Secrets); appErr != nil {
			return errorReplyWithWarnings(appErr, warnings)
		}
	}
	lspReq := lspRequest{Operation: request.LSPOperation, File: request.LSPFile, Line: request.LSPLine, Column: request.LSPColumn, Query: request.LSPQuery, QuerySet: request.LSPQuerySet, IncludeDeclaration: request.LSPIncludeDeclaration}
	if appErr := validateLSPRequestInput(request.LSPFile, lspReq, matches); appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	results := make([]lspProviderRun, len(matches))
	var wait sync.WaitGroup
	for index, match := range matches {
		wait.Add(1)
		go func(index int, match lspMatch) {
			defer wait.Done()
			instance, appErr := d.acquireLSP(ctx, match.Definition, cached.config.Root, request.CWD, request.Secrets, generation)
			if appErr != nil {
				results[index].Err = appErr
				return
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
				results[index].Err = mapped
				return
			}
			redactLSPProviderRun(&results[index], instance.redactor)
		}(index, match)
	}
	wait.Wait()
	result, appErr := aggregateLSPRequestResults(lspReq, workspace, request.LSPFile, matches, results)
	if appErr != nil {
		return errorReplyWithWarnings(appErr, warnings)
	}
	return resultReply(result, warnings)
}

func (d *daemon) lspRuntimeStatuses(definitions []config.LSP, workspace string, generation uint64) map[string]lspRuntimeStatus {
	wanted := make(map[string]struct{}, len(definitions))
	for _, definition := range definitions {
		wanted[definition.Name] = struct{}{}
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
		if entry.instance == nil || entry.instance.lspSession == nil || entry.instance.lspWorkspace != workspace || entry.instance.lspGeneration != generation {
			continue
		}
		if _, ok := wanted[entry.instance.lspName]; ok {
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

func daemonConfigKey(cwd string, paths []string, discovered bool, generation uint64) string {
	return fmt.Sprintf("%d\x00%t\x00%s\x00%s", generation, discovered, cwd, strings.Join(paths, "\x00"))
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

func (d *daemon) acquireLSP(ctx context.Context, definition config.LSP, root *config.Root, cwd string, inputs map[string]secretInput, generation uint64) (*retainedInstance, *appError) {
	workspace := cwd
	if root != nil {
		workspace = resolveRoot(*root)
	}
	key := strings.Join([]string{"lsp", definition.Name, workspace, lspExecutionFingerprint(definition, root, cwd), fmt.Sprint(generation), d.lspAuthIdentity(definition, inputs)}, "\x00")
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
	go d.startLSPInstance(entry, key, definition, root, cwd, inputs, generation)
	return d.waitForLSPInstance(ctx, entry, generation, true)
}

func (d *daemon) waitForLSPInstance(ctx context.Context, entry *poolEntry, generation uint64, owner bool) (*retainedInstance, *appError) {
	instance, appErr := d.waitForInstance(ctx, entry, generation, owner)
	if appErr != nil && appErr.code == "instance_unavailable" {
		return nil, transportError("lsp_instance_unavailable", "the retained LSP instance is unavailable", "run wirecmd daemon reload or restart the daemon")
	}
	return instance, appErr
}

func (d *daemon) startLSPInstance(entry *poolEntry, key string, definition config.LSP, root *config.Root, cwd string, inputs map[string]secretInput, generation uint64) {
	defer d.wg.Done()
	lookup := func(name string) (string, bool) { value, ok := inputs[name]; return value.Value, ok && value.Present }
	command, secrets, appErr := makeStdioCommand(definition.Stdio, root, cwd, lookup)
	var started *retainedInstance
	if appErr == nil {
		redactor := newRedactor(secrets, d.stderr)
		workspace := cwd
		if root != nil {
			workspace = resolveRoot(*root)
		}
		session, err := lspclient.Start(d.ctx, lspclient.Command{Path: command.Path, Args: command.Args[1:], Env: command.Env, Dir: command.Dir, Stderr: redactor}, workspace, buildinfo.Version())
		if err != nil {
			redactor.FlushTo(d.stderr)
			appErr = lspOperationError(err, "initialize").redacted(redactor)
		} else {
			started = &retainedInstance{lspSession: session, redactor: redactor, lspName: definition.Name, lspWorkspace: workspace, lspGeneration: generation}
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
	mac := hmac.New(sha256.New, d.hmacKey)
	items := make([]string, 0)
	add := func(destination string, value config.Value) {
		if !value.IsSecret() {
			return
		}
		name := strings.TrimPrefix(value.Text, "env://")
		input := inputs[name]
		items = append(items, fmt.Sprintf("%s\x00%t\x00%s", destination, input.Present, input.Value))
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

func (d *daemon) acquire(ctx context.Context, server config.Server, root *config.Root, cwd string, inputs map[string]secretInput, generation uint64, interactive bool, emitURL func(string) error) (*retainedInstance, *appError) {
	workspace := cwd
	if root != nil {
		workspace = resolveRoot(*root)
	}
	auth := d.authIdentity(server, inputs)
	key := strings.Join([]string{"mcp", server.Name, workspace, executionFingerprint(server, root, cwd), fmt.Sprint(generation), auth}, "\x00")
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
	if server.HTTP != nil && !hasAuthorizationHeader(*server.HTTP) {
		entry.authStarted = make(chan struct{})
	}
	d.pools[key] = entry
	d.wg.Add(1)
	d.mu.Unlock()
	go d.startInstance(ctx, entry, key, server, root, cwd, inputs, generation, interactive, emitURL)
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

func (d *daemon) startInstance(clientCtx context.Context, entry *poolEntry, key string, server config.Server, root *config.Root, cwd string, inputs map[string]secretInput, generation uint64, interactive bool, emitURL func(string) error) {
	defer d.wg.Done()
	lookup := func(name string) (string, bool) { value, ok := inputs[name]; return value.Value, ok && value.Present }
	target, secrets, appErr := makeTarget(server, root, cwd, lookup)
	if appErr == nil && server.HTTP != nil {
		var oauthSecrets []string
		target, oauthSecrets, appErr = attachOAuth(target, server.Name, *server.HTTP, lookup, interactive, false, func(raw string) {
			if emitURL != nil {
				_ = emitURL(raw)
			}
		})
		secrets = append(secrets, oauthSecrets...)
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
		redactor := newRedactor(secrets, d.stderr)
		if target.oauthRun != nil {
			target.oauthRun.setRedactor(redactor)
		}
		redactor.ProtectEndpoint(target.endpoint)
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
			started = &retainedInstance{session: session, redactor: redactor, breakOnRequestCancel: target.endpoint != "", oauthRun: target.oauthRun}
		}
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
	items := make([]string, 0)
	seen := map[string]struct{}{}
	mac := hmac.New(sha256.New, d.hmacKey)
	add := func(destination string, value config.Value) {
		if !value.IsSecret() {
			return
		}
		name := strings.TrimPrefix(value.Text, "env://")
		if _, ok := seen[destination]; ok {
			return
		}
		seen[destination] = struct{}{}
		input := inputs[name]
		items = append(items, destination+"\x00"+input.Value)
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
