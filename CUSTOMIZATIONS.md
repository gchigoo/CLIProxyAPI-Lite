# Personal customization contract

The initial personal source was based on upstream v7.2.156. Its 32 customized source/test paths were preserved exactly before upstream fixes and plugin removal began. The original source remains separate from this repository.

## Behavior to preserve

1. Codex OAuth requests use a stable account-specific native identity/device profile. Native headers and body metadata are consistent across HTTP, SSE, WebSocket and image requests. Account overrides remain effective.
2. Codex client identity and catalog-fetch defaults use the same version, currently 0.154.0. A future version update must update runtime constants, metadata and tests together.
3. Embedded and remotely refreshed Codex plan catalogs do not force identity headers over the account profile, including `gpt-5.6-luna`, `gpt-5.6-sol`, `gpt-5.6-terra` and `gpt-6-astra`. Filtering happens before catalog publication; non-identity metadata and explicit config/auth/model overrides remain effective.
4. Official Codex WebSocket connections retain the custom TLS/uTLS dialing behavior. API-key and other endpoint scopes retain their existing behavior.
5. An explicitly configured proxy must fail closed on proxy errors. No silent direct-network fallback is allowed.
6. Antigravity retains its provider TLS profile alongside upstream connection-pool limits and lifecycle handling. Gemini and Vertex retain their provider headers.
7. `codex_exec` and its versioned user-agent form retain Codex multi-agent recognition and agent-message compatibility.
8. Native model registration, account scheduling, credential updates, reasoning conversion, streaming errors and built-in usage accounting remain functional after dynamic extension removal.

## Update boundaries

An upstream change that restores hardcoded identity headers or removes one of these behaviors is not an acceptable automatic conflict resolution. Preserve the intended behavior, adapt the relevant upstream fix, and run its tests together with the personal regression tests.

Runtime version updates and remote catalogs are separate from source backports, but both must preserve the same account identity boundary. Test startup and periodic catalog refresh as well as embedded defaults. Local tests and a successful build do not establish current live-provider compatibility.
