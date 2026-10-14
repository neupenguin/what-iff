// Package providerkeys answers one question: which model-provider credential
// does this request use?
//
// Before this existed the answer was decided in several places — a key read
// from configuration at startup, handed to a provider client's constructor,
// and separately restated wherever something needed to know whether a provider
// was usable at all. Routing every answer through one interface means there is
// one place to change when the answer stops being the same for everybody.
package providerkeys

import (
	"context"
	"strings"

	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

// Resolver answers which credential to use for a provider.
//
// Implementations must be safe for concurrent use, and must tolerate a context
// carrying no actor: background work that belongs to nobody still asks.
type Resolver interface {
	// Key returns the credential for provider, or "" when there is none.
	//
	// An empty result is not an error. It means this caller cannot reach that
	// provider, which callers report as such rather than by substituting a
	// credential belonging to somebody else.
	Key(ctx context.Context, provider models.ModelProvider) string

	// Configured reports whether a client for provider is worth constructing.
	//
	// It answers a startup question, not a per-request one. Where credentials
	// come from configuration it is known then; where they belong to accounts
	// it cannot be, and the honest answer is yes — the per-request Key result
	// decides whether the call can actually be made.
	Configured(provider models.ModelProvider) bool
}

// DeploymentResolver answers the same for every caller, from the credentials
// the deployment was configured with.
type DeploymentResolver struct {
	keys map[models.ModelProvider]string
}

// NewDeploymentResolver builds a resolver over configured credentials. Blank
// entries are dropped, so a provider left unset is indistinguishable from one
// never mentioned.
func NewDeploymentResolver(keys map[models.ModelProvider]string) DeploymentResolver {
	copied := make(map[models.ModelProvider]string, len(keys))
	for provider, key := range keys {
		if k := strings.TrimSpace(key); k != "" {
			copied[provider] = k
		}
	}
	return DeploymentResolver{keys: copied}
}

// Key returns the configured credential for provider. The context is ignored:
// every caller gets the same answer.
func (r DeploymentResolver) Key(_ context.Context, provider models.ModelProvider) string {
	return r.keys[provider]
}

// Configured reports whether provider has a configured credential.
func (r DeploymentResolver) Configured(provider models.ModelProvider) bool {
	return r.keys[provider] != ""
}

// New chooses the resolver the server uses, given the deployment's configured
// keys and the keys accounts store themselves. Keys come from the deployment's
// configuration until accounts can add their own in the app. A build that
// always supplies keys itself replaces New in an init().
var New = func(deployment DeploymentResolver, _ Resolver) Resolver {
	return deployment
}
