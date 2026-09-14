# CLIProxyAPI Lite contributor instructions

## Project scope

This is a personal downstream of CLIProxyAPI. Read `CUSTOMIZATIONS.md`, `UPSTREAM_PATCHES.md` and `MAINTENANCE.md` before changing behavior or importing upstream code.

- Dynamic plugins and Devin are deliberately excluded. Do not reintroduce loaders, extension SDKs, stores, plugin routes, provider hooks, or compatibility stubs.
- The native usage sink interface called `Plugin` is retained. It does not load dynamic code. Preserve built-in usage/Redis accounting.
- Preserve built-in providers, authentication, scheduling, cooldowns, streaming, images, model registration and Home's non-plugin behavior unless a task explicitly changes their scope.
- Preserve personal native identity, per-account device profiles, provider TLS/uTLS, explicit-proxy fail-closed behavior, and Codex multi-agent compatibility.
- Review upstream commits and prerequisites individually. Record their original SHAs and decisions. Do not automatically merge the upstream default branch.

## Architecture

- `cmd/server`: server and CLI entry point.
- `internal/api`: HTTP routing and management API.
- `internal/runtime/executor`: built-in provider executors and their unit tests. Shared helpers belong in `internal/runtime/executor/helps`.
- `internal/thinking`: canonical reasoning configuration, validation and per-provider application. Preserve this architecture.
- `internal/translator` and `sdk/translator`: native protocol conversion and registry.
- `internal/watcher`: configuration and auth-file reload.
- `sdk/cliproxy`: service, native model registration and executor lifecycle.
- `sdk/cliproxy/auth`: account selection, session affinity, cooldowns and refresh.
- `internal/home`: optional native Home integration.

## Validation

Go 1.26 or later. Build and test with `CGO_ENABLED=0`.

```text
go test -p 4 ./...
go build -trimpath -ldflags="-s -w" -o test-output ./cmd/server
```

Use `gofmt` on changed Go files. Run meaningful affected tests, then the required integration build. Record tests that require unavailable external infrastructure as unverified. Mock traffic does not establish live-provider or production health.

For substantial authentication, concurrency, transactionality or coupled request-path changes, review the final stable change after validation and obtain one independent review. Keep reviewers read-only.

## Editing and publication

- Preserve unrelated work. Use one writer per working tree.
- Keep implementation, commit, push, release and deployment authorization separate. Do not deploy or publish a release merely because tests passed.
- Stage exact reviewed paths and inspect the staged diff before committing.
- Never commit runtime configuration, account files, OAuth tokens, API keys, cookies, logs or private deployment evidence.
- Keep the MIT license and upstream attribution.
- Keep CI limited to tests and builds unless publishing/deployment behavior is explicitly requested.

## Code conventions

Prefer small changes and native execution paths. Avoid speculative abstractions and new feature flags for removed functionality. Use English code comments; retain the existing language for user-facing strings. New Markdown is English except explicitly language-specific files such as `README_CN.md`.

Use contextual errors and structured logrus logging without credentials. Do not use `log.Fatal` or panic in request handlers. Preserve cancellation, deadlines and existing provider timeout policies; do not introduce request timeouts after upstream connection establishment. Existing WebSocket liveness deadlines, relay deadlines, management API-call timeout and catalog-fetch command timeouts remain valid.

Prefer controllable clocks or deterministic synchronization over sleeps in TTL, ordering, cache eviction and expiration unit tests.
