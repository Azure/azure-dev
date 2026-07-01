# Release History

## 1.0.0-beta.2 (2026-07-01)

### Other Changes

- Patch release to align version with `azure.ai.agents` 1.0.0-beta.2. No functional changes.

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0`.
- [[#8651]](https://github.com/Azure/azure-dev/pull/8651) Update Go to 1.26.4 and bump golang.org/x/crypto and golang.org/x/net. Thanks @hemarina for the contribution!

## 0.0.1-preview (2026-05-22)

Initial preview release of the Agent Inspector extension.

### Features Added

- Agent Inspector extension: serves the Inspector single-page app on loopback and bridges it to a locally running agent over WebSocket JSON-RPC and HTTP/SSE proxying.
- Added `azd ai inspector launch` to start the Inspector standalone; the `azd ai inspector` command group is kept for future subcommands.
- Integrated with `azd ai agent run` so the Inspector is launched automatically for local agent runs (opt out with `--no-inspector`).

### Other Changes

- Hardened the loopback server with `Host`/`Origin` allowlisting, proxy URL constraints (http/https, loopback hosts, configured agent port), bounded WebSocket frame size, read/write deadlines, ping/pong, and Content-Security-Policy and related hardening headers on the served SPA.
- Restricted `openUrlInBrowser` to `http`/`https` URLs.
- Suppressed proxy body logging and stdout output when launched in silent mode from `azd ai agent run`.
- Added panic recovery in per-message RPC goroutines and fixed a nil-map race in stream registration during session cleanup.
