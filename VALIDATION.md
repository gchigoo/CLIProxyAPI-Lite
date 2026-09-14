# Initial Lite validation

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
