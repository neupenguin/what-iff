# Package: `internal/providerkeys`

## Role

Answers which model-provider credential a request uses, in one place.

## Responsibilities

- **`Resolver`:** `Key(ctx, provider)` returns the credential for a request, or `""` when there is none.
  `Configured(provider)` answers the startup question of whether a client for that provider is worth building.
- **`DeploymentResolver`:** answers from the keys the deployment was configured with, the same for every caller; blank values count as unconfigured.

## Dependencies

- **Inbound:** `internal/server` (builds the resolver and the credential transport), `internal/agent` (`AgentConfig.KeyResolver`).
- **Outbound:** `internal/models` only.

## Non-obvious decisions

- An empty `Key` result is not an error: callers report the provider as unconfigured rather than substituting another credential.
- Keys are applied at the shared HTTP transport (`provider.CredentialHTTPClient`) rather than at client construction, because the SDK client value is copied into several holders and has no single field to reassign.

## Testing

- `providerkeys_test.go` — configured and blank values, and that the caller does not change the answer.
- The transport itself is tested in `internal/agent/provider/credentials_test.go`.

## Related documentation

- [Architecture summary](../../docs/ARCHITECTURE_SUMMARY.md)
