package server

import (
	"context"

	"github.com/theimaginaryfoundation/what-iff/internal/agent/provider"
	appmodels "github.com/theimaginaryfoundation/what-iff/internal/models"
	"github.com/theimaginaryfoundation/what-iff/internal/providerkeys"
)

// providerEndpoint is where one provider's requests go and how it expects its
// credential on the wire.
type providerEndpoint struct {
	provider appmodels.ModelProvider
	baseURL  string
	style    provider.CredentialStyle
}

// providerEndpoints lists every provider the process can reach.
//
// The endpoint here is the same one used to build that provider's client, so
// pointing a provider at a proxy moves both the request and the rule that
// credentials it. Anything absent from this list simply carries whatever
// credential its client was built with.
func (c *Config) providerEndpoints() []providerEndpoint {
	orDefault := func(configured, fallback string) string {
		if configured != "" {
			return configured
		}
		return fallback
	}
	return []providerEndpoint{
		{appmodels.ModelProviderOpenAI, provider.DefaultOpenAIBaseURL, provider.BearerAuthorization},
		{appmodels.ModelProviderAnthropic, provider.DefaultAnthropicBaseURL, provider.AnthropicAPIKey},
		// z.ai speaks the Anthropic Messages API, so it takes the Anthropic
		// header with its own key — which is precisely why the rule has to be
		// scoped to a host rather than to a wire format.
		{appmodels.ModelProviderZAI, orDefault(c.ZAIBaseURL, provider.DefaultZAIBaseURL), provider.AnthropicAPIKey},
		{appmodels.ModelProviderGoogle, orDefault(c.GeminiBaseURL, provider.DefaultGeminiBaseURL), provider.BearerAuthorization},
		{appmodels.ModelProviderMistral, orDefault(c.MistralBaseURL, provider.DefaultMistralBaseURL), provider.BearerAuthorization},
		{appmodels.ModelProviderDeepSeek, orDefault(c.DeepSeekBaseURL, provider.DefaultDeepSeekBaseURL), provider.BearerAuthorization},
		{appmodels.ModelProviderQwen, orDefault(c.QwenBaseURL, provider.DefaultQwenBaseURL), provider.BearerAuthorization},
		{appmodels.ModelProviderXiaomi, orDefault(c.XiaomiBaseURL, provider.DefaultXiaomiBaseURL), provider.BearerAuthorization},
	}
}

// configuredProviderKeys is the credentials this deployment was started with.
func (c *Config) configuredProviderKeys() map[appmodels.ModelProvider]string {
	return map[appmodels.ModelProvider]string{
		appmodels.ModelProviderOpenAI:    c.OpenAIKey,
		appmodels.ModelProviderAnthropic: c.AnthropicKey,
		appmodels.ModelProviderZAI:       c.ZAIKey,
		appmodels.ModelProviderGoogle:    c.GeminiKey,
		appmodels.ModelProviderMistral:   c.MistralKey,
		appmodels.ModelProviderDeepSeek:  c.DeepSeekKey,
		appmodels.ModelProviderQwen:      c.QwenKey,
		appmodels.ModelProviderXiaomi:    c.XiaomiKey,
	}
}

// credentialRules turns the endpoint list into per-host rules that ask the
// resolver for a credential on every outgoing request.
func credentialRules(endpoints []providerEndpoint, resolver providerkeys.Resolver) []provider.CredentialRule {
	rules := make([]provider.CredentialRule, 0, len(endpoints))
	for _, e := range endpoints {
		p := e.provider
		rules = append(rules, provider.CredentialRule{
			BaseURL: e.baseURL,
			Style:   e.style,
			Resolve: func(ctx context.Context) string { return resolver.Key(ctx, p) },
		})
	}
	return rules
}
