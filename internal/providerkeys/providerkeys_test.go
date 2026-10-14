package providerkeys

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestDeploymentResolver_AnswersFromConfiguration(t *testing.T) {
	t.Parallel()
	r := NewDeploymentResolver(map[models.ModelProvider]string{
		models.ModelProviderOpenAI:    "sk-openai",
		models.ModelProviderAnthropic: "sk-anthropic",
	})

	require.Equal(t, "sk-openai", r.Key(context.Background(), models.ModelProviderOpenAI))
	require.Equal(t, "sk-anthropic", r.Key(context.Background(), models.ModelProviderAnthropic))
	require.True(t, r.Configured(models.ModelProviderOpenAI))
}

// A provider set to the empty string must be indistinguishable from one never
// mentioned, so an operator blanking a variable turns the provider off rather
// than leaving a client that sends an empty credential.
func TestDeploymentResolver_BlankIsUnconfigured(t *testing.T) {
	t.Parallel()
	r := NewDeploymentResolver(map[models.ModelProvider]string{
		models.ModelProviderOpenAI: "   ",
		models.ModelProviderQwen:   "",
	})

	for _, p := range []models.ModelProvider{models.ModelProviderOpenAI, models.ModelProviderQwen, models.ModelProviderGoogle} {
		require.Empty(t, r.Key(context.Background(), p), "provider %q", p)
		require.False(t, r.Configured(p), "provider %q", p)
	}
}

// The deployment answer is the same for everyone; nothing about the caller may
// change it.
func TestDeploymentResolver_IgnoresTheCaller(t *testing.T) {
	t.Parallel()
	r := NewDeploymentResolver(map[models.ModelProvider]string{models.ModelProviderOpenAI: "sk-openai"})

	//nolint:staticcheck // a deliberately foreign context value
	ctx := context.WithValue(context.Background(), "user", "someone-else")
	require.Equal(t, "sk-openai", r.Key(ctx, models.ModelProviderOpenAI))
}

// Callers hold the interface, so the concrete type must satisfy it.
func TestDeploymentResolverSatisfiesResolver(t *testing.T) {
	t.Parallel()
	var _ Resolver = NewDeploymentResolver(nil)
}
