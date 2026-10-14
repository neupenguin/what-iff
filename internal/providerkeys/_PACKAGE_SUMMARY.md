# Package: `internal/providerkeys`

## Role

Answers which model-provider credential a request uses, in one place.

## Responsibilities

- **`Resolver`:** `Key(ctx, provider)` returns the credential for a request, or `""` when there is none.
  `Configured(provider)` answers the startup question of whether a client for that provider is worth building.
- **`DeploymentResolver`:** answers from the keys the deployment was configured with, the same for every caller; blank values count as unconfigured.
- **`New`:** chooses the resolver the server uses, given the deployment resolver and the account resolver (`internal/accountkeys`).
  It returns the deployment resolver until accounts can add keys in the app.
  A build that always supplies keys itself sets it in an `init()`.
  The server mounts the account key routes only when `New` picks the account resolver.

## Dependencies

- **Inbound:** `internal/server` (builds the resolver and the credential transport), `internal/agent` (`AgentConfig.KeyResolver`).
- **Outbound:** `internal/models` only.

## Non-obvious decisions

- An empty `Key` result is not an error: callers report the provider as unconfigured rather than substituting another credential.
- Keys are applied at the shared HTTP transport (`provider.CredentialHTTPClient`) rather than at client construction, because the SDK client value is copied into several holders and has no single field to reassign.
- Work that runs after its request has ended must put the owning account on its context, or the account resolver gives it no key.
  File chunking (`handlerutils.TriggerAsyncFileChunking`) and account import (`accountexport`) do.
  The startup memory-embedding backfill pages across every account at once and has no single owner.

## Testing

- `providerkeys_test.go` — configured and blank values, and that the caller does not change the answer.
- `New` defaults to the deployment's keys (also in `providerkeys_test.go`); the account resolver is tested in `internal/accountkeys`.
- The transport itself is tested in `internal/agent/provider/credentials_test.go`.

## Related documentation

- [Architecture summary](../../docs/ARCHITECTURE_SUMMARY.md)
