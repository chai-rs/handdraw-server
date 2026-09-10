# Handdraw Server

Go backend: `github.com/chai-rs/handdraw-server`, entry point `cmd/app`.

```sh
make run
```

The Fiber v3 bootstrap serves `GET /healthz` at `http://127.0.0.1:8081` and supports graceful
shutdown. By default it needs no database credentials. Optional identity configuration adds
`GET /v1/me` and a restricted profile-resolver database connection; startup never runs migrations.

The first [board metadata domain](../docs/server/board-domain.md) includes models, services,
Bun repositories and isolated PostgreSQL tests. Shared utilities are integrated, and ID prefixes live in model constants. See the [package adoption record](../docs/server/shared-packages.md). The [identity slice](../docs/server/identity-domain.md) adds Supabase verification and stable `usr_<ksuid>` profiles. The complete nine-table foundation SQL now lives in `migrations/` and is exercised by Testcontainers through golang-migrate. Identity stays disabled until the target database and restricted credentials are provisioned; T03 adds optional workspace list/get/Owner rename routes with a separate restricted request connection; T04 adds optional onboarding/project/board routes and a separately credentialed in-process cleanup worker. See the [migration guide](../docs/server/migration-foundation.md).
Run `make check` and `make test-integration` (requires Docker) to verify it.

See the shared [server guide](../docs/server/README.md) for structure,
configuration, verification and the pure golang-migrate workflow.

The foundation [HTTP/content contract](../docs/server/content-compatibility.md) adds portable OpenAPI, dependency checks, a schema-1 document codec/builder and protocol-1 control fixtures. `make check-all` adds actual-migration Docker tests and the real adjacent client compatibility harness. CI setup is in `.github/workflows/verify.yml`. These contracts prepare future workflows; they do not turn on board/collaboration routes.


Run `make openapi` to open the standalone Scalar reference at http://127.0.0.1:8082.
`cmd/openapi` embeds the reviewed OpenAPI contract, serves `/openapi.json`, and needs no
Auth or database credentials. Override its bind address with `OPENAPI_ADDR`.
`make build` produces both `bin/handdraw-server` and `bin/handdraw-openapi`.
Scalar's UI bundle loads from its public CDN; the contract is served locally. Operations
are grouped by Identity, Workspaces, Projects, Boards, Members, Invitations and Guests. See [workspace/access setup](../docs/server/workspace-access.md)
for runtime flags, restricted roles and browser CORS configuration.

See [T04 setup and local browser acceptance](../docs/server/onboarding-board-api.md). `make local-cloud` starts a disposable local integration fixture; `APP_BOARD_ENABLED` controls product board routes and `APP_CLEANUP_ENABLED` additionally mounts deletion. Runtime schema is now version 9.

[T05 membership/invitations](../docs/server/membership-invitations.md) adds Owner-guarded member changes, atomic invitation seat reservation/acceptance and board Viewer grants under `APP_MEMBERSHIP_ENABLED`. Email delivery remains explicitly pending integration; the API returns no bearer invitation tokens.

### Durable collaboration

T06 adds the optional Fiber WebSocket route `/v1/collaboration`, backed by ygo, actor RLS and PostgreSQL CAS. Enable `APP_COLLABORATION_ENABLED` with explicit `APP_COLLABORATION_ORIGINS` and identity/workspace/board routes. A pinned direct/session-pooled database connection owns the single beta runtime; transaction pooling is unsupported. See [the collaboration contract](../docs/server/durable-collaboration.md). `make check-all` includes real five-peer Yjs, restart, lost-ACK and database-fault tests using the adjacent client checkout.
