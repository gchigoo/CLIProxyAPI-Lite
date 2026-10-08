# Lite validation

## GPT-6 Sol / Grok 4.7 update (2026-09-23)

Production source baseline: `d5310b33fe83b75c2a574f2884d8bdf88a012fdf`.
Selected compatibility changes are recorded in `UPSTREAM_PATCHES.md`; this is not a full upstream v7.3.14 merge.

- Windows amd64 / Go 1.26.1 / `CGO_ENABLED=0`: complete `go test -p 4 ./...` passed after backporting the upstream bootstrap test cleanup and mock-clock synchronization fix.
- The initial runs exposed pre-existing test-only stream draining and mock-clock races. Those failed runs are not treated as passing evidence.
- New model registration/identity tests and focused native-identity/SOCKS5 cancellation tests passed.
- Linux amd64 production binary cross-build passed. Binary SHA-256: `cf95f421f528b4afc9b141c6298aafc065b409cc0970cc401c1de9451fba6d75`.
- `git diff --check` passed. Main-agent review covered changed production code and preserved account identity, cancellation, proxy, and removed-provider boundaries.
- Live deployment evidence is maintained outside the public repository. Model catalog presence does not establish upstream account entitlement.

## Initial validation (2026-09-14)

Validated on 2026-09-14. The comparison baseline is the preserved personal v7.2.156 build. Lite includes 33 selected later upstream changes and dynamic plugin removal; the differences cannot all be attributed to removal alone.

## Executed checks

| Check | Result |
|---|---|
| Windows amd64, Go 1.26.1, `CGO_ENABLED=0`, `go test -json -p 4 ./...` | 8,905 tests/subtests passed, 0 failed, 8 skipped; 115 packages processed, including packages without tests |
| Linux amd64 core packages, Go 1.26.1 test binaries | 5,894 tests/subtests passed, 0 failed, 7 skipped across 20 packages |
| Windows executable and stripped Linux executable | Both built successfully |
| Dockerfile | Built successfully with the `golang:1.26-bookworm` builder and `alpine:3.24` runtime |
| Compose definitions and build helpers | Both Compose files validated; PowerShell and shell build scripts passed syntax checks |
| Local HTTP/SSE smoke tests | Chat Completions and Responses JSON/SSE, model registration, API auth, management auth and usage endpoint passed on Windows and Linux |
| Removed extension behavior | Plugin management/resource/OAuth routes return 404; legacy enabled plugin YAML is inert; management reports `X-CPA-SUPPORT-PLUGIN: 0` |
| Personal preservation | 63 selected personal function fingerprints matched; the original source's 1,473 tracked files, HEAD and working-tree status were unchanged |

Linux tests ran in a temporary container with no external network, 2 CPUs, 512 MiB memory and a read-only source mount. Packages covered server startup, Codex catalog fetching, configuration, API/management, watcher/synthesis, Home, executors/helpers, model registry, signatures, thinking, Codex multi-agent handling, HTTP/Responses handlers, service/auth and translators.

The skips were six signature tests requiring external native sample corpora, one opt-in TLS capture test requiring `CPA_TLS_FP_PROXY`, and one Windows GitHub-token environment test. Linux did not run that last package. These skips are not evidence of live provider compatibility.

## Size comparison

| Measurement | Personal v7.2.156 | Lite | Reduction |
|---|---:|---:|---:|
| Non-test Go files | 676 | 583 | 93 files |
| Non-test Go lines, including comments and blank lines | 226,663 | 198,747 | 27,916 lines, about 12.3% |
| Windows executable, default build flags | 88,958,464 bytes | 86,346,240 bytes | 2.94% |
| Linux executable, `-trimpath -ldflags="-s -w"` | 65,167,522 bytes | 63,393,954 bytes | 2.72% |

Most native provider, transport, storage and Home dependencies remain. Removing dynamic extension code substantially reduces the maintenance surface, while the linked binary reduction is more modest.

## Local mock measurement

The baseline and Lite ran sequentially in the same no-network Linux container with identical resource settings. Each used a loopback mock OpenAI-compatible upstream, 5 warmups and 60 sequential latency samples. RSS was read after 69 mock requests.

| Measurement | Baseline | Lite |
|---|---:|---:|
| Startup to readiness | 890 ms | 531 ms |
| Median request latency | 2.617 ms | 2.581 ms |
| p95 request latency | 3.556 ms | 3.401 ms |
| Process RSS | 41,280 KiB | 39,840 KiB |

These are single-run local observations, not production throughput or latency guarantees. The measured request latency is broadly comparable. The mock also confirmed the Responses named-tool-choice backport: Lite emitted the expected nested Chat Completions tool choice, while the baseline retained the old shape.

## Limits

No production service was replaced or restarted, no release was created, and no real provider account was used for these checks. External Home deployment, remote storage services and live provider compatibility remain unverified. Public CI performs tests and builds only.

## Selective v7.3.3 update (2026-09-15)

Starting Lite revision: `7466c585684fe44cf132294e434b87ff805e68ed`. Target upstream release: `v7.3.3` (`7bbfeaf8a7acf2cd5a834dcb0842539fe6aabc2b`). This update adds 12 selected changes plus the local preservation fixes described in `UPSTREAM_PATCHES.md`; it is not the complete upstream release.

Fresh checks ran on macOS arm64 with Go 1.27.1. The module language version remains Go 1.26.0.

| Check | Result |
|---|---|
| `CGO_ENABLED=0 go test -count=1 -json -p 4 ./...` | 9,050 tests/subtests passed, 0 failed, 7 skipped; 115 packages, including 29 without tests |
| `CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" ... ./cmd/server` | macOS arm64, Linux amd64 and Windows amd64 builds passed; Linux binary is statically linked |
| Targeted `go test -race -count=1 -p 4` with `CGO_ENABLED=1` | Auth revision/registration and management status synchronization, WebSocket compaction/prewarm, catalog publication, SOCKS5 cancellation, bootstrap buffering and reasoning replay tests passed |
| `CGO_ENABLED=1 go test -race -count=1 ./sdk/proxyutil ./cmd/fetch_antigravity_models` | Both packages passed |
| Auth registration lock/stale-disable/same-revision cases | 100 repeated runs and 30 race-detector runs passed after making worker selection and cleanup deterministic |
| Catalog preservation tests | Startup and periodic refresh retain metadata but strip catalog identity headers from Codex plans; other providers and explicit model overrides remain effective |
| Transport/replay regression tests | Pre-HTTP retries do not cool credentials; certificate/proxy configuration failures remain non-transient; reasoning replay and interrupted SOCKS5 connection cleanup passed |
| Native protocol and management integration | Local HTTP/SSE/WebSocket tests cover tool pairing, Gemini IDs, compaction continuation/reset/disabled auth, Home runtime selection and nonblocking management hooks |
| Lite boundary tests | Removed plugin routes/configuration remain inert; source imports/directories exclude dynamic plugins and Devin while retaining native usage sinks |
| Ledger audit | All 31 non-merge commits in the release range have full original SHAs and decisions: 5 included, 7 adapted, 19 excluded. Four merge first-parent diffs were checked separately |
| `git diff --check` | Passed |

A repeat full run exposed an upstream test assumption that the second worker to start represented a particular auth. Its failure cleanup also allowed unfinished registration to affect later model-list tests. The test hook now receives the auth ID, and cleanup waits for all started workers before resetting hooks or removing models. The repeated checks above validate that correction; the initial passing run alone was not treated as sufficient.

The seven skips remain the six external signature-corpus tests and the opt-in TLS capture test. No live provider credentials were used. Linux and Windows binaries were cross-built, not executed on those operating systems in this session. Container builds, real Home/storage deployments and production performance were not revalidated. Browser tools were unavailable; management and model-state behavior was checked through local API/WebSocket integration tests instead. The initial size and latency measurements above describe the initial branch only and must not be attributed to this update.

The race detector requires CGO for the test binaries; release builds still use `CGO_ENABLED=0`. No commit, push, release or deployment was performed as part of this validation.

## Selective reliability update (2026-09-28)

The GitHub maintenance baseline is d5310b33fe83b75c2a574f2884d8bdf88a012fdf. The deployed selective v7.3.14 source was first verified against its 19-file SHA-256 manifest and retained. Eleven subsequent upstream changes were included/adapted through the v8.0.3 review boundary; the result retains the v7 configuration/API and module contract.

Executed on Windows amd64 with Go 1.26.1 and CGO_ENABLED=0:

- Affected logging, session, auth, executor/helpers, multi-agent and translator package tests passed.
- Final go test -p 4 ./... passed. Go reused successful unchanged package results from the preceding runs.
- Required stripped Windows integration build and Linux amd64 cross-build passed. Git safe.directory was scoped to the build process because the sandbox and interactive users have different repository ownership.
- gofmt reported no outstanding changes; git diff --check passed.
- Native identity, account header precedence, TLS/proxy, model registration, usage/Redis and removed integration regression tests remain part of the passing suite.

The upstream namespace regression test was adapted to Lite's existing helper name. A trial to apply invalid_grant backoff to HTTP 401 was rejected by existing terminal-auth tests and withdrawn; existing HTTP 401 termination semantics remain unchanged. Enabled HTTP 400/statusless invalid_grant backoff and disabled-account unscheduling follow the selected upstream change.

Linux tests were not executed locally; the Linux binary is cross-built. External signature corpora and opt-in TLS capture infrastructure were not supplied. Unit tests and cross-builds do not establish production/provider health. Independent review and any production deployment evidence are recorded separately outside this public repository; this section records local validation only.

## xAI client version hotfix (2026-10-01)

Executed on macOS arm64 with Go 1.26.1 and CGO_ENABLED=0:

- Before the change, the 08ad18d1 tree rebuilt for linux/amd64 with the recorded ldflags reproduced the deployed binary byte for byte.
- `TestXAIChatProxyClientVersionMeetsServerFloor` failed against 0.2.120 and passed against 1.0.44. The header-derivation and per-auth override tests passed before and after.
- `go test ./internal/runtime/executor/` and `go test -p 4 ./...` passed (86 packages with tests, 29 without).
- The Linux amd64 cross-build passed and carries `xai-grok-workspace/1.0.44`.

Linux tests were not executed locally. Unit tests do not establish live xAI availability; production probe evidence is recorded outside this public repository.

## Selective update through v8.0.8 (2026-10-02)

Executed on macOS arm64 with Go 1.26.1:

- Each backport ran its affected package tests. The integer normalization regression (native Codex HTTP, WebSocket and fallback paths) and the executor-identity check were observed failing before their fixes and passing after.
- `CGO_ENABLED=0 go test -count=1 -p 4 ./...` passed: 87 packages with tests, 29 without.
- `CGO_ENABLED=1 go test -race` passed for sdk/cliproxy/auth, internal/runtime/executor/..., internal/client/codex/... and test; the new refresh-epoch tests also passed five repeated race runs.
- gofmt reported no changes in modified Go files; `git diff --check` passed; Linux amd64, macOS arm64 and Windows amd64 builds passed.

`TestAntigravityAuthHasCreditsRequiredHomeBalanceUsesKV` fails when repeated with `-count>1` in one process, on the baseline as well; it passes in normal single runs and is a pre-existing test isolation issue. Linux tests were not executed locally. The review fixes above each have a test that failed before the fix and passes after it; the refresh and credits tests also passed five repeated race runs. Production evidence is recorded separately.

## Selective update through v8.0.20 (2026-10-08)

Executed on macOS arm64 with Go 1.27.1:

- Every pick was built and vetted before its commit; adapted picks also ran their affected package tests, including the new Antigravity backend-error and terminal-disconnect tests, the CAQS replay tests and the credential-version tests.
- `CGO_ENABLED=0 go test -count=1 -p 4 ./...` passed: 87 packages with tests, 30 without. The same command passed on the v8.0.8 baseline before the update.
- `CGO_ENABLED=1 go test -race -count=1` passed for sdk/cliproxy/auth, internal/runtime/executor/..., internal/util, internal/translator/codex/..., sdk/api/handlers/... and test; the refresh, unauthorized, snapshot and credential-version auth tests also passed five repeated race runs.
- gofmt reported no changes in modified Go files; `git diff --check` passed; Linux amd64, macOS arm64 and Windows amd64 builds with `-trimpath -ldflags="-s -w"` passed. Linux amd64 SHA-256: `4db49fae92814623a9522f44c111482c970f9fc64b7de77f92ce11a7c358ef16`.

No live provider requests were made and the Linux and Windows binaries were not run. `claude_thinking_replay_test.go` and `codex_stream_bootstrap_buffering_test.go` are not gofmt-clean on the baseline either and were left unchanged.

An independent read-only review followed these checks. Its two confirmed Antigravity stream findings were fixed with tests that failed before the fix (`TestAntigravityStreamDisconnectBeforeSplitUsagePublishesRecord`, `TestAntigravityStreamMalformedFrameDoesNotSwallowLaterFrames`, `TestAntigravityStreamIncompletePayloadAtEOFReportsError`). The executor package then passed, the Antigravity stream tests passed three repeated race runs, and the full suite was rerun.

The Grok npm version updater (90654da5, 34e73c75) was added afterwards. Its upstream tests and the Lite fail-closed proxy test passed. The xAI executor and updater tests passed under `-race`, and the client-version, updater and chat-proxy header tests passed five repeated race runs. `TestXAIWebsocketsExecuteStreamSendsResponseCreateWithPreviousResponseID` fails on its second run and hangs on its third when repeated with `-count>1` in one process. This also happens on the v8.0.8 baseline, so it is a pre-existing test isolation issue; the test passes in single runs.

## Served-model observability (2026-10-08)

Branch `feat/served-model-observability` on top of `upgrade/v8.0.20-selective` (0042b881). Executed on macOS arm64 with Go 1.27.1:

- Each upstream pick ran its affected package tests. The Lite Antigravity response-model tests failed before e9463ff5 and passed after it. The tracker and `served_models` tests failed before their implementation. The two restored Codex usage tests were verified by mutation: recording reasoning effort from the original request, or publishing image tool usage before main usage, makes them fail.
- `CGO_ENABLED=0 go test -count=1 -p 4 ./...` passed: 88 packages with tests.
- `CGO_ENABLED=1 go test -race -count=1` passed for internal/servedmodel, internal/runtime/executor/..., internal/redisqueue, sdk/cliproxy/usage and internal/api/handlers/management. The response-model, substitution, tracker and `served_models` tests also passed five repeated race runs, after the listing test was changed to use per-run auth IDs against the process-wide tracker.
- gofmt reported no changes in modified Go files; `git diff --check` passed; Linux amd64, macOS arm64 and Windows amd64 builds with `-trimpath -ldflags="-s -w"` passed. Linux amd64 SHA-256 before the review fixes: `3fbb4e04045cdab4f8345618bdf519fc0b7c023a132d6a9773578376369ae8d4`.

Mock traffic only: no live provider responses were observed, so the extraction rules are verified against upstream fixtures, not current provider output. Linux and Windows binaries were not run.

An independent read-only review of the branch found no critical issue and one important one: the summary flagged every mapped Kimi request as substituted. The fix records the reporter's substitution decision on the usage record. `TestKimiServedModelSummaryJudgesSubstitutionAgainstMappedModel` failed before the fix and passes after it. `TestUsageReporterResponseModelSubstituted` and the new Codex executor-level `TestCodexExecutorUsageRecordCarriesServedModel` were each confirmed by mutation. After the fixes, the full suite passed again (88 packages with tests), the race runs above passed again including five repeated runs, the three builds passed, and the Linux amd64 SHA-256 is `51fbb492d71d4400686a806abc6b64d1e137e07efe4f64f4cfc6bbc9fa64fd90`.

## Release review fixes (2026-10-08)

Release delta 33c4ff39..HEAD, reviewed by Codex (gpt-6-astra, high effort) in three passes: "Deploy: no", "no", then "yes" after the fixes recorded in `UPSTREAM_PATCHES.md`. Each fix has a test that failed before it. Executed on macOS arm64 with Go 1.27.1:

- `CGO_ENABLED=0 go test -count=1 -p 4 ./...` passed: 88 packages with tests.
- `CGO_ENABLED=1 go test -race -count=1` passed for sdk/cliproxy/auth, sdk/cliproxy, internal/runtime/executor/..., internal/translator/codex/..., internal/servedmodel, internal/api/handlers/management, internal/redisqueue and sdk/cliproxy/usage. The refresh, Antigravity stream, proxy reload and citation tests passed five repeated race runs.
- gofmt reported no changes in modified Go files, and `git diff --check` passed.

`TestClaudeExecutorSharedCredentialMetadataMixedAccess` reports a data race in `ClaudeExecutor.PrepareRequestAuth` (shared credential metadata map) under `-race`. It reproduces on the deployed baseline 33c4ff39 and the files are unchanged in this release. Claude accounts are not configured in production. The race runs above skip it, and it remains a follow-up.

## Known equivalent served models (2026-10-08)

After deployment, xAI logged "served grok-4.7-build for requested grok-4.7" for every request, the same served name the 2026-10-02 baseline probes recorded. `helps/response_model_equivalents.go` now lists grok-4.7 → grok-4.7-build as a known equivalent, so neither the warning nor `served_models` treats it as a substitution. `TestUsageReporterResponseModelSubstituted` (the new equivalent cases) and `TestUsageReporterDoesNotWarnForKnownEquivalentServedModel` failed before the change. Unrelated requests and other served names still count as substitutions. `CGO_ENABLED=0 go test -count=1 -p 4 ./...` passed with 88 packages, and the response-model and tracker tests passed three repeated race runs.
