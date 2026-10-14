package server

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	appmodels "github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/providerkeys"
)

// Every rule must resolve its own provider's credential. If a rule's resolver
// captured the wrong provider, requests to one host would carry another
// provider's key.
func TestCredentialRules_EachRuleResolvesItsOwnProvider(t *testing.T) {
	t.Parallel()
	cfg := &Config{}
	endpoints := cfg.providerEndpoints()
	keys := make(map[appmodels.ModelProvider]string, len(endpoints))
	for _, e := range endpoints {
		keys[e.provider] = "key-for-" + string(e.provider)
	}

	rules := credentialRules(endpoints, providerkeys.NewDeploymentResolver(keys))

	require.Len(t, rules, len(endpoints))
	for i, e := range endpoints {
		require.Equal(t, "key-for-"+string(e.provider), rules[i].Resolve(context.Background()), "rule for %s", e.provider)
		require.Equal(t, e.baseURL, rules[i].BaseURL)
	}
}

// A configured base URL moves the rule with the request, so a provider pointed
// at a proxy still gets its own credential.
func TestProviderEndpoints_FollowConfiguredBaseURLs(t *testing.T) {
	t.Parallel()
	cfg := &Config{ZAIBaseURL: "https://llm-proxy.example/zai"}
	byProvider := map[appmodels.ModelProvider]providerEndpoint{}
	for _, e := range cfg.providerEndpoints() {
		byProvider[e.provider] = e
	}

	require.Equal(t, "https://llm-proxy.example/zai", byProvider[appmodels.ModelProviderZAI].baseURL)
	require.Equal(t, provider.DefaultGeminiBaseURL, byProvider[appmodels.ModelProviderGoogle].baseURL, "unset base URLs fall back to the provider default")
	require.Equal(t, provider.AnthropicAPIKey, byProvider[appmodels.ModelProviderZAI].style, "z.ai speaks the Anthropic Messages API")
}
