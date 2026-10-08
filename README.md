# CLIProxyAPI Lite

A personal downstream of [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI), maintained by [gchigoo](https://github.com/gchigoo). The goal is a smaller, focused proxy that preserves existing provider support and personal transport improvements while selectively adopting upstream fixes.

This project keeps the original MIT license and upstream Git history. It is independently maintained and is not an official CLIProxyAPI release.

## Scope

- Retains the built-in OpenAI, Codex, Claude, Gemini, Antigravity, Vertex, Kimi and xAI integrations present in the base version, including compatible API providers.
- Adds Meta (Muse Code) OAuth accounts from upstream: log in with `--meta-login` (device code) or the management `meta-auth-url` endpoint, then use the `muse-spark-*` models. Static `meta-api-key` entries are not supported.
- Retains OAuth, account rotation, cooldowns, model aliases, streaming, WebSocket and image handling, management APIs, configuration reload, and built-in usage accounting.
- Preserves account-stable native identity, provider TLS/uTLS profiles, explicit-proxy fail-closed behavior and Codex multi-agent client compatibility.
- Removes dynamic plugin loading, plugin SDK/store/install/update, plugin routes, plugin OAuth, Home plugin synchronization and dynamic request/response/translation/scheduling hooks.
- Does not include Devin or LAN gateway discovery. Other new upstream integrations and extension frameworks are outside the default scope.

Home's non-plugin functionality, native usage sinks, storage backends and the TUI remain available. The internal usage sink interface named `Plugin` does not load third-party code.

## Source and updates

The initial baseline is upstream **v7.2.156**, with the existing personal customizations and **45 selected changes through v7.3.3**. This is not a complete v7.3.3 feature set. See [UPSTREAM_PATCHES.md](UPSTREAM_PATCHES.md) for included, adapted and skipped commits, and [CUSTOMIZATIONS.md](CUSTOMIZATIONS.md) for the behavior that future updates must preserve.

Upstream fixes are reviewed together with their prerequisites and tests. Changes to existing protocols, reliability, performance and security are candidates for backporting. Devin and dynamic plugin changes are excluded. Mixed changes require a focused adaptation. Do not automatically merge or rebase the personal branch onto the upstream default branch.

## Build

Go 1.26 or later is required. Dynamic library plugins and a C toolchain are not required.

Build from a checkout of this repository. The Go module path remains the upstream path to preserve internal and SDK import compatibility.

```sh
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o cli-proxy-api ./cmd/server
```

PowerShell:

```powershell
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags="-s -w" -o cli-proxy-api.exe ./cmd/server
```

Copy `config.example.yaml` to `config.yaml`, configure the providers you use, and run:

```sh
./cli-proxy-api -config config.yaml
```

Use `-local-model` to use the embedded model catalogs. This flag controls model catalog updates; it does not disable every background metadata check. Automatic Codex catalog refreshes cannot replace account identity headers; explicit config/auth overrides remain effective.

Codex `stream-bootstrap-buffering` remains off by default. Its optional `stream-bootstrap-timeout` releases buffering when subsequent upstream frames arrive after the configured duration; it never aborts the upstream request and does not impose a timeout on a silent upstream.

Legacy `plugins:` YAML sections are ignored and cannot enable an extension. Remove those sections from your own configuration to avoid confusion. Plugin-only management and resource routes are absent; the management capability header reports `X-CPA-SUPPORT-PLUGIN: 0`.

Keep real configuration, OAuth/account files, API keys, logs, binaries and private deployment records outside Git. Example values are placeholders.

## Docker

After creating and configuring `config.yaml`, build and start from this source:

```sh
docker compose up -d --build
```

The default image is local `cliproxyapi-lite:local`; Compose does not pull an official CPA image. The optional `docker-build.ps1` and `docker-build.sh` helpers build with a personal Git version label and start the service. `docker-compose.cluster.yml` retains the optional Home deployment and also builds this local source.

## Validation

```sh
CGO_ENABLED=0 go test ./...
```

[VALIDATION.md](VALIDATION.md) records the initial measurements and fresh checks for the selective v7.3.3 update. Loopback mock tests demonstrate local API routing and protocol behavior; they do not prove availability of live provider accounts or a production deployment. Performance measurements apply to the tested environment and build flags.

CI runs tests and builds for Linux and Windows. It does not publish container images or deploy a service. See [MAINTENANCE.md](MAINTENANCE.md) for the update process.

## License

[MIT](LICENSE), retaining the upstream copyright notices.
