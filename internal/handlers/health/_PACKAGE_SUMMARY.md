# Package: `internal/handlers/health`

## Role

**Liveness/readiness** endpoints.
`GET /api/health/live` is process liveness only and never touches the database; it is what the ALB target-group health check should use.
`GET /api/health/ready` checks database connectivity within the ALB timeout budget, for dashboards and humans.
`GET /api/health` is the legacy path and behaves exactly like `/ready`.

## Responsibilities

- **`Handler.Live`:** always 200 with `{"status":"healthy","checks":{}}`.
- **`Handler.Ready`** (and **`Handler.Check`**, which delegates to it): JSON response with per-check status map, 503 when the DB ping fails.
  DB ping uses short timeout (`dbPingTimeout` — must stay under ALB health check deadline, see const in `handler.go`).

## Dependencies

- **Inbound:** `internal/server` registers `/api/health`, `/api/health/live` and `/api/health/ready` unauthenticated on the API subrouter.
- **Outbound:** `database/sql`, `zap`.

## Non-obvious decisions

- **3s DB ping cap** vs common 5s ALB timeout — documented in `handler.go`.
- **Liveness is split from readiness** so a database blip (failover, saturated pool) does not fail every task's ALB check at once.
  A DB-backed ALB check made ECS replace all tasks together, and the restart storm's startup migrations added load to the database.

## Testing

- `handler_test.go` — response shape and checks.

## Related documentation

- [Architecture summary](../../../docs/ARCHITECTURE_SUMMARY.md) — **Infra** (ALB, ECS).
