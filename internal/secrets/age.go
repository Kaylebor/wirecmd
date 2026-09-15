package secrets

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// MaxPlaintextSize bounds one decrypted JSON document.
	MaxPlaintextSize  int64 = 1 << 20
	maxCiphertextSize int64 = 16 << 20
	defaultAgeTimeout       = 60 * time.Second
)

var (
	ageKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	ageVersionRE  = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[-+].*)?$`)
)

// Store is one encrypted age document. Stores are evaluated in the order
// supplied to NewAgeProvider, from strongest to weakest precedence.
type Store struct {
	Path string
}

// AgeProviderOptions defines the fixed age command and effective store inputs
// for one configured scope. Command is primarily injectable for tests; normal
// construction leaves it empty to use age from PATH.
type AgeProviderOptions struct {
	Command      string
	Identities   []string
	Stores       []Store
	Timeout      time.Duration
	MaxPlaintext int64
}

// AgeProvider decrypts JSON stores with the external age command. It retains
// no plaintext or decoded-document cache.
type AgeProvider struct {
	command      string
	identities   []string
	stores       []Store
	timeout      time.Duration
	maxPlaintext int64
}

// NewAgeProvider validates effective age configuration. Identity paths must
// be absolute; their contents are never opened or inspected by Wirecmd.
func NewAgeProvider(options AgeProviderOptions) (*AgeProvider, error) {
	command := options.Command
	if command == "" {
		command = "age"
	}
	timeout := options.Timeout
	if timeout == 0 {
		timeout = defaultAgeTimeout
	}
	if timeout < 0 {
		return nil, &Error{Code: CodeProviderInvalid, Message: "age secret provider timeout is invalid"}
	}
	maxPlaintext := options.MaxPlaintext
	if maxPlaintext == 0 {
		maxPlaintext = MaxPlaintextSize
	}
	if maxPlaintext < 1 {
		return nil, &Error{Code: CodeProviderInvalid, Message: "age secret provider plaintext limit is invalid"}
	}
	identities := append([]string(nil), options.Identities...)
	for _, identity := range identities {
		if identity == "" || !filepath.IsAbs(identity) {
			return nil, &Error{Code: CodeProviderInvalid, Message: "age identity path must be absolute and non-empty"}
		}
	}
	stores := append([]Store(nil), options.Stores...)
	for _, store := range stores {
		if store.Path == "" {
			return nil, &Error{Code: CodeProviderInvalid, Message: "age secret store path is empty"}
		}
	}
	return &AgeProvider{command: command, identities: identities, stores: stores, timeout: timeout, maxPlaintext: maxPlaintext}, nil
}

func (*AgeProvider) Scheme() string { return "age" }

func (*AgeProvider) ValidateLocator(locator string) error {
	if !ageKeyPattern.MatchString(locator) {
		return invalidLocator("age secret locator is invalid")
	}
	return nil
}

// Resolve decrypts each needed store at most once and applies first-store-wins
// precedence for each requested locator.
func (p *AgeProvider) Resolve(ctx context.Context, _ Scope, locators []string) (BatchResult, error) {
	for _, locator := range locators {
		if err := p.ValidateLocator(locator); err != nil {
			return BatchResult{}, err
		}
	}
	if len(locators) == 0 {
		return BatchResult{Values: map[string]string{}}, nil
	}
	if len(p.identities) == 0 {
		return BatchResult{}, &Error{Code: CodeProviderInvalid, Message: "age secret provider requires at least one identity"}
	}
	if err := p.qualify(ctx); err != nil {
		return BatchResult{}, err
	}

	requested := make(map[string]struct{}, len(locators))
	for _, locator := range locators {
		requested[locator] = struct{}{}
	}
	result := BatchResult{Values: make(map[string]string, len(locators))}
	for _, store := range p.stores {
		if len(result.Values) == len(requested) {
			break
		}
		ciphertext, exists, err := readAgeStore(store.Path, maxCiphertextSize)
		if err != nil {
			return BatchResult{}, err
		}
		if !exists {
			continue
		}
		plaintext, err := p.decrypt(ctx, ciphertext)
		if err != nil {
			return BatchResult{}, err
		}
		values, err := decodeAgeDocument(plaintext)
		if err != nil {
			return BatchResult{}, err
		}
		for locator := range requested {
			if _, already := result.Values[locator]; already {
				continue
			}
			if value, found := values[locator]; found {
				result.Values[locator] = value
			}
		}
	}
	for _, locator := range locators {
		if _, found := result.Values[locator]; !found {
			result.Missing = append(result.Missing, locator)
		}
	}
	sort.Strings(result.Missing)
	return result, nil
}

func (p *AgeProvider) qualify(ctx context.Context) error {
	command, err := exec.LookPath(p.command)
	if err != nil {
		return &Error{Code: CodeAgeUnavailable, Message: "age executable is unavailable"}
	}
	operation, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	output, exceeded, err := runAge(operation, command, []string{"--version"}, nil, 1024)
	if errors.Is(operation.Err(), context.DeadlineExceeded) {
		return &Error{Code: CodeAgeUnavailable, Message: "age version check timed out"}
	}
	if err != nil || exceeded {
		return &Error{Code: CodeAgeUnavailable, Message: "age executable could not be queried"}
	}
	match := ageVersionRE.FindStringSubmatch(strings.TrimSpace(string(output)))
	if match == nil || versionLessThan(match[1], match[2], match[3], 1, 3, 0) {
		return &Error{Code: CodeAgeVersionUnsupported, Message: "age 1.3.0 or newer is required"}
	}
	return nil
}

func versionLessThan(major, minor, patch string, wantMajor, wantMinor, wantPatch int) bool {
	gotMajor, _ := strconv.Atoi(major)
	gotMinor, _ := strconv.Atoi(minor)
	gotPatch, _ := strconv.Atoi(patch)
	if gotMajor != wantMajor {
		return gotMajor < wantMajor
	}
	if gotMinor != wantMinor {
		return gotMinor < wantMinor
	}
	return gotPatch < wantPatch
}

func (p *AgeProvider) decrypt(ctx context.Context, ciphertext []byte) ([]byte, error) {
	command, err := exec.LookPath(p.command)
	if err != nil {
		return nil, &Error{Code: CodeAgeUnavailable, Message: "age executable is unavailable"}
	}
	arguments := make([]string, 0, 1+len(p.identities)*2)
	arguments = append(arguments, "--decrypt")
	for _, identity := range p.identities {
		arguments = append(arguments, "-i", identity)
	}
	operation, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	plaintext, exceeded, err := runAge(operation, command, arguments, ciphertext, p.maxPlaintext)
	if errors.Is(operation.Err(), context.DeadlineExceeded) {
		return nil, &Error{Code: CodeAgeDecryptionTimeout, Message: "age decryption timed out"}
	}
	if exceeded {
		return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store exceeds the plaintext limit"}
	}
	if err != nil {
		return nil, &Error{Code: CodeAgeDecryptionFailed, Message: "age could not decrypt the secret store"}
	}
	return plaintext, nil
}

func decodeAgeDocument(plaintext []byte) (map[string]string, error) {
	if !utf8.Valid(plaintext) {
		return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store is not valid UTF-8 JSON"}
	}
	decoder := json.NewDecoder(bytes.NewReader(plaintext))
	token, err := decoder.Token()
	if err != nil {
		return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store is invalid"}
	}
	delimiter, ok := token.(json.Delim)
	if !ok || delimiter != '{' {
		return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store must be a JSON object"}
	}
	values := map[string]string{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store is invalid"}
		}
		key, ok := keyToken.(string)
		if !ok || !ageKeyPattern.MatchString(key) {
			return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store contains an invalid key"}
		}
		if _, duplicate := values[key]; duplicate {
			return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store contains a duplicate key"}
		}
		var value string
		if err := decoder.Decode(&value); err != nil {
			return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store values must be strings"}
		}
		values[key] = value
	}
	end, err := decoder.Token()
	if err != nil {
		return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store is invalid"}
	}
	endDelimiter, ok := end.(json.Delim)
	if !ok || endDelimiter != '}' {
		return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store is invalid"}
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, &Error{Code: CodeSecretStoreInvalid, Message: "decrypted age secret store has trailing data"}
	}
	return values, nil
}

type boundedBuffer struct {
	buffer   bytes.Buffer
	limit    int64
	exceeded bool
	cancel   context.CancelFunc
}

func (b *boundedBuffer) Write(data []byte) (int, error) {
	if int64(b.buffer.Len())+int64(len(data)) > b.limit {
		b.exceeded = true
		b.cancel()
		return 0, io.ErrShortWrite
	}
	return b.buffer.Write(data)
}

func runAge(ctx context.Context, command string, arguments []string, input []byte, limit int64) ([]byte, bool, error) {
	operation, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := operation.Err(); err != nil {
		return nil, false, err
	}
	cmd := exec.Command(command, arguments...)
	configureAgeProcess(cmd)
	cmd.Stdin = bytes.NewReader(input)
	output := &boundedBuffer{limit: limit, cancel: cancel}
	cmd.Stdout = output
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return nil, false, err
	}
	done := make(chan struct{})
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		select {
		case <-operation.Done():
			killAgeProcess(cmd)
		case <-done:
		}
	}()
	err := cmd.Wait()
	close(done)
	<-stopped
	return output.buffer.Bytes(), output.exceeded, err
}
