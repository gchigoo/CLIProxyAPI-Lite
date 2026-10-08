# Personal customization contract

The initial personal source was based on upstream v7.2.156. Its 32 customized source/test paths were preserved exactly before upstream fixes and plugin removal began. The original source remains separate from this repository.

## Behavior to preserve

1. Codex OAuth requests use a stable account-specific native identity/device profile. Native headers and body metadata are consistent across HTTP, SSE, WebSocket and image requests. Account overrides remain effective.
2. Codex client identity and catalog-fetch defaults use the same version, currently 0.155.0. A future version update must update runtime constants, metadata and tests together.
3. Embedded and remotely refreshed Codex plan catalogs do not force identity headers over the account profile, including `gpt-5.6-luna`, `gpt-5.6-sol`, `gpt-5.6-terra` `gpt-6-astra` and `gpt-6-sol`. Filtering happens before catalog publication; non-identity metadata and explicit config/auth/model overrides remain effective.
4. Official Codex WebSocket connections retain the custom TLS/uTLS dialing behavior. API-key and other endpoint scopes retain their existing behavior.
5. An explicitly configured proxy must fail closed on proxy errors. No silent direct-network fallback is allowed.
6. Antigravity retains its provider TLS profile alongside upstream connection-pool limits and lifecycle handling. Gemini and Vertex retain their provider headers.
7. `codex_exec` and its versioned user-agent form retain Codex multi-agent recognition and agent-message compatibility.
8. Native model registration, account scheduling, credential updates, reasoning conversion, streaming errors and built-in usage accounting remain functional after dynamic extension removal.
9. Every executor observes the model the upstream reports before publishing usage. Usage records carry `ResponseModel` and `ResponseModelSubstituted` (judged against the expected upstream model, so mapped models and `grok-X.Y` served as `grok-X.Y-build` (`helps/response_model_equivalents.go`) are not substitutions), and silent substitutions are logged by credential index only. `internal/servedmodel` keeps a bounded, non-persistent per-credential summary, and `GET /v0/management/auth-files` exposes it as `served_models`. Updates that touch usage reporting or executors must keep the observation order and this field.
10. Every xAI catalog model `grok-X.Y` (one-digit major) is also listed and routable as `cpa-xXY`, for example `cpa-x48` for `grok-4.8`. This behaves like a configured `fork: true` alias on the OAuth `xai` channel. Configured entries own their model and alias names and win. Implicit aliases are derived at listing and request time and are never written into the configuration. Local subagent configs rely on these names to follow new Grok versions automatically.

## Update boundaries

An upstream change that restores hardcoded identity headers or removes one of these behaviors is not an acceptable automatic conflict resolution. Preserve the intended behavior, adapt the relevant upstream fix, and run its tests together with the personal regression tests.

Runtime version updates and remote catalogs are separate from source backports, but both must preserve the same account identity boundary. Test startup and periodic catalog refresh as well as embedded defaults. Local tests and a successful build do not establish current live-provider compatibility.
