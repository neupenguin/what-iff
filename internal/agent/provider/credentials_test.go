package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

type capturingTransport struct{ last *http.Request }

func (c *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	c.last = req
	return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody, Request: req}, nil
}

func static(key string) func(context.Context) string {
	return func(context.Context) string { return key }
}

func do(t *testing.T, c *http.Client, url string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	_, err = c.Do(req)
	require.NoError(t, err)
}

// Every OpenAI-compatible provider sends a different key in the same header,
// which is why a rule is scoped to a host rather than to a wire format.
func TestCredentialHTTPClient_ChoosesCredentialByHost(t *testing.T) {
	t.Parallel()
	cap := &capturingTransport{}
	c := CredentialHTTPClient([]CredentialRule{
		{BaseURL: "https://api.openai.com/v1", Style: BearerAuthorization, Resolve: static("sk-openai")},
		{BaseURL: "https://api.mistral.ai/v1", Style: BearerAuthorization, Resolve: static("sk-mistral")},
		{BaseURL: "https://api.anthropic.com", Style: AnthropicAPIKey, Resolve: static("sk-anthropic")},
	}, &http.Client{Transport: cap})

	do(t, c, "https://api.openai.com/v1/responses")
	require.Equal(t, "Bearer sk-openai", cap.last.Header.Get("Authorization"))

	do(t, c, "https://api.mistral.ai/v1/chat/completions")
	require.Equal(t, "Bearer sk-mistral", cap.last.Header.Get("Authorization"))

	do(t, c, "https://api.anthropic.com/v1/messages")
	require.Equal(t, "sk-anthropic", cap.last.Header.Get("x-api-key"))
	require.Empty(t, cap.last.Header.Get("Authorization"), "Anthropic takes its key in its own header")
}

// A host with no rule must be left exactly as the caller built it.
func TestCredentialHTTPClient_LeavesUnknownHostsAlone(t *testing.T) {
	t.Parallel()
	cap := &capturingTransport{}
	c := CredentialHTTPClient([]CredentialRule{
		{BaseURL: "https://api.openai.com/v1", Style: BearerAuthorization, Resolve: static("sk-openai")},
	}, &http.Client{Transport: cap})

	do(t, c, "https://example.invalid/whatever")
	require.Empty(t, cap.last.Header.Get("Authorization"))
}

// No credential is not an error. The provider's own 401 is a better failure
// than a malformed empty header, and it must not inherit another provider's key.
func TestCredentialHTTPClient_NoCredentialLeavesTheRequestUntouched(t *testing.T) {
	t.Parallel()
	cap := &capturingTransport{}
	c := CredentialHTTPClient([]CredentialRule{
		{BaseURL: "https://api.openai.com/v1", Style: BearerAuthorization, Resolve: static("")},
	}, &http.Client{Transport: cap})

	do(t, c, "https://api.openai.com/v1/responses")
	require.Empty(t, cap.last.Header.Get("Authorization"))
}

// An operator pointing a provider at a proxy moves both the request and the
// rule that credentials it, because the rule is built from the same base URL.
func TestCredentialHTTPClient_FollowsAConfiguredEndpoint(t *testing.T) {
	t.Parallel()
	cap := &capturingTransport{}
	c := CredentialHTTPClient([]CredentialRule{
		{BaseURL: "https://llm-proxy.internal.example/v1", Style: BearerAuthorization, Resolve: static("sk-proxied")},
	}, &http.Client{Transport: cap})

	do(t, c, "https://llm-proxy.internal.example/v1/chat/completions")
	require.Equal(t, "Bearer sk-proxied", cap.last.Header.Get("Authorization"))
}

// net/http forbids a RoundTripper from modifying the request it is handed.
func TestCredentialHTTPClient_DoesNotMutateTheCallersRequest(t *testing.T) {
	t.Parallel()
	cap := &capturingTransport{}
	c := CredentialHTTPClient([]CredentialRule{
		{BaseURL: "https://api.openai.com/v1", Style: BearerAuthorization, Resolve: static("sk-openai")},
	}, &http.Client{Transport: cap})

	req, err := http.NewRequest(http.MethodGet, "https://api.openai.com/v1/responses", nil)
	require.NoError(t, err)
	_, err = c.Do(req)
	require.NoError(t, err)
	require.Empty(t, req.Header.Get("Authorization"), "the caller's request must come back unchanged")
}

// Under a non-vendor backend the base client is the deny-network transport.
// Dropping it would open egress the backend forbids.
func TestCredentialHTTPClient_PreservesTheBaseTransport(t *testing.T) {
	t.Parallel()
	var reached atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached.Store(true) }))
	defer srv.Close()

	denied := DenyNetworkHTTPClient()
	c := CredentialHTTPClient([]CredentialRule{
		{BaseURL: srv.URL, Style: BearerAuthorization, Resolve: static("sk-openai")},
	}, denied)

	req, err := http.NewRequest(http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	_, err = c.Do(req)
	require.ErrorIs(t, err, ErrNetworkDenied, "the refusal must come from the deny-network transport")
	require.False(t, reached.Load(), "a request reached a server the deny-network transport should have blocked")
}
