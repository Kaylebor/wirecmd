package cli

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Kaylebor/wirecmd/internal/config"
)

func TestMakeHTTPTargetResolvesQueryAndHeaders(t *testing.T) {
	transport := config.HTTP{
		Endpoint: "https://example.test/mcp?tenant=old&kept=yes",
		Query: []config.HTTPField{
			{Name: "tenant", Value: literalValue("acme")},
			{Name: "token", Value: secretValue("API_TOKEN")},
		},
		Headers: []config.HTTPField{
			{Name: "X-API-Key", Value: secretValue("API_KEY")},
			{Name: "X-Empty", Value: secretValue("EMPTY")},
		},
	}
	values := map[string]string{"API_TOKEN": "a/b c", "API_KEY": "header-secret", "EMPTY": ""}
	target, secrets, appErr := makeHTTPTarget(transport, func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	})
	if appErr != nil {
		t.Fatalf("makeHTTPTarget: %v", appErr)
	}
	if target.endpoint != "https://example.test/mcp?kept=yes&tenant=acme&token=a%2Fb+c" {
		t.Fatalf("endpoint = %q", target.endpoint)
	}
	if len(secrets) != 2 || secrets[0] != "a/b c" || secrets[1] != "header-secret" {
		t.Fatalf("secrets = %#v", secrets)
	}

	capture := &captureRoundTripper{}
	target.httpClient.Transport.(*configuredHeaderTransport).base = capture
	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, target.endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("X-API-Key", "old")
	if _, err := target.httpClient.Do(request); err != nil {
		t.Fatal(err)
	}
	if capture.request.Header.Get("Accept") != "application/json" {
		t.Fatal("SDK-owned header was not preserved")
	}
	if capture.request.Header.Get("X-API-Key") != "header-secret" {
		t.Fatalf("X-API-Key = %q", capture.request.Header.Get("X-API-Key"))
	}
	headerValues, present := capture.request.Header["X-Empty"]
	if !present || len(headerValues) != 1 || headerValues[0] != "" {
		t.Fatalf("present-empty header = %#v, %v", headerValues, present)
	}
}

func TestMakeHTTPTargetRejectsMissingAndUnsafeResolvedValues(t *testing.T) {
	for _, test := range []struct {
		name      string
		transport config.HTTP
		lookup    func(string) (string, bool)
		code      string
	}{
		{
			name:      "missing secret",
			transport: config.HTTP{Endpoint: "https://example.test/mcp", Query: []config.HTTPField{{Name: "token", Value: secretValue("TOKEN")}}},
			lookup:    func(string) (string, bool) { return "", false }, code: "secret_not_available",
		},
		{
			name:      "header line break",
			transport: config.HTTP{Endpoint: "https://example.test/mcp", Headers: []config.HTTPField{{Name: "X-Test", Value: secretValue("TOKEN")}}},
			lookup:    func(string) (string, bool) { return "bad\r\nInjected: yes", true }, code: "invalid_http_value",
		},
		{
			name:      "invalid UTF-8",
			transport: config.HTTP{Endpoint: "https://example.test/mcp", Query: []config.HTTPField{{Name: "token", Value: secretValue("TOKEN")}}},
			lookup:    func(string) (string, bool) { return string([]byte{0xff}), true }, code: "invalid_http_value",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, _, appErr := makeHTTPTarget(test.transport, test.lookup)
			if appErr == nil || appErr.code != test.code {
				t.Fatalf("error = %#v, want code %q", appErr, test.code)
			}
		})
	}
}

func TestConfiguredHeadersAreNotInjectedAcrossOrigins(t *testing.T) {
	transport := &configuredHeaderTransport{
		base:    &captureRoundTripper{},
		headers: http.Header{"Authorization": []string{"Bearer secret"}},
		scheme:  "https",
		host:    "example.test",
	}
	request, err := http.NewRequest(http.MethodGet, "https://redirected.test/mcp", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transport.RoundTrip(request); err != nil {
		t.Fatal(err)
	}
	captured := transport.base.(*captureRoundTripper).request
	if captured.Header.Get("Authorization") != "" {
		t.Fatal("configured authorization was injected into a different origin")
	}
}

func TestHTTPSecretsParticipateInDaemonInputsAndIdentity(t *testing.T) {
	server := config.Server{Name: "remote", HTTP: &config.HTTP{
		Query:   []config.HTTPField{{Name: "token", Value: secretValue("TOKEN")}},
		Headers: []config.HTTPField{{Name: "Authorization", Value: secretValue("AUTH")}},
	}}
	lookup := func(name string) (string, bool) { return map[string]string{"TOKEN": "one", "AUTH": "two"}[name], true }
	inputs := selectedSecretInputs(server, lookup)
	if len(inputs) != 2 || inputs["TOKEN"].Value != "one" || inputs["AUTH"].Value != "two" {
		t.Fatalf("inputs = %#v", inputs)
	}
	d := &daemon{hmacKey: []byte("test-key")}
	first := d.authIdentity(server, inputs)
	inputs["TOKEN"] = secretInput{Present: true, Value: "different"}
	second := d.authIdentity(server, inputs)
	if first == second {
		t.Fatal("HTTP credential change did not change daemon authentication identity")
	}
}

func literalValue(value string) config.Value {
	return config.Value{Kind: config.ValueLiteral, Text: value}
}

func secretValue(name string) config.Value {
	return config.Value{Kind: config.ValueSecretReference, Text: "env://" + name}
}

type captureRoundTripper struct{ request *http.Request }

func (c *captureRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	c.request = request
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader("")), Header: make(http.Header), Request: request}, nil
}
