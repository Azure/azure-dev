# Feature-Telemetry Inventory Matrix

This document provides a comprehensive inventory of every `azd` command and its telemetry coverage.
It identifies gaps where commands rely solely on the global middleware span and recommends
specific telemetry additions.

## Telemetry Coverage Legend

| Symbol | Meaning |
|--------|---------|
| ✅ | Covered — command-specific attributes or events are emitted |
| ⚠️ | Global span only — no command-specific telemetry |
| 🚫 | Telemetry intentionally disabled |

## Commands with Telemetry Disabled

These commands have `DisableTelemetry: true` set on their `ActionDescriptor`.

| Command | Reason |
|---------|--------|
| `version` | Trivial local-only command; no value in tracking |
| `telemetry upload` | Disabled to prevent recursive telemetry-about-telemetry |

## Commands with Command-Specific Telemetry

These commands emit attributes or events beyond the global middleware span.

| Command | Attributes / Events | Notes |
|---------|---------------------|-------|
| `init` | `init.method` (template / app / project / environment / copilot), `appinit.detected.databases`, `appinit.detected.services`, `appinit.confirmed.databases`, `appinit.confirmed.services`, `appinit.modify_add.count`, `appinit.modify_remove.count`, `appinit.lastStep` | Comprehensive coverage via `SetUsageAttributes` and `repository/app_init.go` |
| `update` | `update.installMethod`, `update.channel`, `update.fromVersion`, `update.toVersion`, `update.result` | Result codes cover success, failure, and skip reasons |
| Extensions (dynamic) | `extension.id`, `extension.version` + trace-context propagation to child process | Covers `ext.run` and `ext.install` events |
| `mcp start` | Per-tool spans via `tracing.Start` with `mcp.client.name`, `mcp.client.version` | MCP event prefix `mcp.*` |

## Full Inventory Matrix

| Command | Subcommands | Global Span | Command-Specific Attrs | Feature Events | Gap? | Recommended Additions |
|---------|-------------|:-----------:|:----------------------:|:--------------:|:----:|----------------------|
| **Auth** | | | | | | |
| `auth login` | — | ✅ | ❌ | ❌ | **Yes** | `auth.method` (browser, device-code, service-principal-secret, service-principal-certificate, federated-github, federated-azure-pipelines, federated-oidc, managed-identity, external, oneauth), `auth.result` (success/failure) |
| `auth logout` | — | ✅ | ❌ | ❌ | **Yes** | `auth.result` |
| `auth status` | — | ✅ | ❌ | ❌ | **Yes** | `auth.method` (check-status), `auth.result` |
| `auth token` | — | ✅ | ❌ | ❌ | **Yes** | `auth.result` |
| **Config** | | | | | | |
| `config` | `show`, `list`, `get`, `set`, `unset`, `reset`, `list-alpha`, `options` | ✅ | ❌ | ❌ | **Yes** | `config.operation` (show/list/get/set/unset/reset/list-alpha/options) |
| **Environment** | | | | | | |
| `env` | `set`, `set-secret`, `select`, `new`, `remove`, `list`, `refresh`, `get-values`, `get-value` | ✅ | ❌ | ❌ | **Yes** | `env.operation` (set/set-secret/select/new/remove/list/refresh/get-values/get-value), `env.count` (measurement — number of environments) |
| `env config` | `get`, `set`, `unset` | ✅ | ❌ | ❌ | **Yes** | `env.operation` (config-get/config-set/config-unset) |
| **Hooks** | | | | | | |
| `hooks run` | — | ✅ | ❌ | ❌ | **Yes** | `hooks.name`, `hooks.type` (project/service) |
| **Templates** | | | | | | |
| `template` | `list`, `show` | ✅ | ❌ | ❌ | **Yes** | `template.operation` (list/show) |
| `template source` | `list`, `add`, `remove` | ✅ | ❌ | ❌ | **Yes** | `template.operation` (source-list/source-add/source-remove) |
| **Pipeline** | | | | | | |
| `pipeline config` | — | ✅ | ❌ | ❌ | **Yes** | `pipeline.provider` (github/azdo), `pipeline.auth` (federated/client-credentials) |
| **Monitor** | | | | | | |
| `monitor` | — | ✅ | ❌ | ❌ | **Yes** | `monitor.type` (overview/logs/live) |
| **Show** | | | | | | |
| `show` | — | ✅ | ❌ | ❌ | **Yes** | `show.output.format` (json/table/etc.) |
| **Infrastructure** | | | | | | |
| `infra generate` | — | ✅ | ❌ | ❌ | **Yes** | `infra.provider` (bicep/terraform) |
| `infra synth` | — | ✅ | ❌ | ❌ | **Yes** | `infra.provider` (bicep/terraform) |
| `infra create` | — (hidden, deprecated) | ✅ | ❌ | ❌ | Low | Wraps `provision`; inherits its telemetry once added |
| `infra delete` | — (hidden, deprecated) | ✅ | ❌ | ❌ | Low | Wraps `down`; inherits its telemetry once added |
| **Core Lifecycle** | | | | | | |
| `restore` | — | ✅ | ❌ | ❌ | **Yes** | Service-level attrs (language, host, count) |
| `build` | — | ✅ | ❌ | ❌ | **Yes** | Service-level attrs (language, host, count) |
| `provision` | — | ✅ | ❌ | ❌ | **Yes** | `infra.provider`, resource count, duration breakdown |
| `package` | — | ✅ | ❌ | ❌ | **Yes** | Service-level attrs (language, host, count) |
| `deploy` | — | ✅ | ❌ | ❌ | **Yes** | Service host type, target count, deployment strategy |
| `publish` | — | ✅ | ❌ | ❌ | **Yes** | Same as `deploy` (alias behavior) |
| `up` | — | ✅ | ❌ | ❌ | **Yes** | Orchestration attrs: which phases ran, total service count |
| `down` | — | ✅ | ❌ | ❌ | **Yes** | `infra.provider`, resource count, purge flag |
| **Add** | | | | | | |
| `add` | — | ✅ | ❌ | ❌ | **Yes** | Component type added, source (template/manual) |
| **Completion** | | | | | | |
| `completion` | `bash`, `zsh`, `fish`, `powershell`, `fig` | ✅ | ❌ | ❌ | Low | Shell type — low priority, minimal analytical value |
| **VS Server** | | | | | | |
| `vs-server` | — | ✅ | ❌ | ❌ | Low | Long-running RPC; covered by `vsrpc.*` events |
| **Copilot Consent** | | | | | | |
| `copilot consent` | `list`, `revoke`, `grant` | ✅ | ❌ | ❌ | **Yes** | Consent operation type, scope |
| **Extension Management** | | | | | | |
| `extension` | `list`, `show`, `install`, `uninstall`, `upgrade` | ✅ | ❌ | ❌ | **Yes** | `extension.id`, `extension.version`, operation type |
| `extension source` | `list`, `add`, `remove`, `validate` | ✅ | ❌ | ❌ | **Yes** | Source operation type |
| **Init** | | | | | | |
| `init` | — | ✅ | ✅ | ✅ | No | — Already covered |
| **Update** | | | | | | |
| `update` | — | ✅ | ✅ | ✅ | No | — Already covered |
| **MCP** | | | | | | |
| `mcp start` | — | ✅ | ✅ | ✅ | No | — Already covered |
| **Disabled** | | | | | | |
| `version` | — | 🚫 | — | — | No | Intentionally disabled |
| `telemetry upload` | — | 🚫 | — | — | No | Intentionally disabled |

## Gap Summary

| Priority | Count | Commands |
|----------|-------|----------|
| **High** | 8 | `auth login/logout/status/token`, `provision`, `deploy`, `up`, `down` |
| **Medium** | 14 | `config *`, `env *`, `pipeline config`, `hooks run`, `template *`, `monitor`, `show`, `infra generate/synth`, `restore`, `build`, `package`, `add` |
| **Low** | 6 | `completion *`, `vs-server`, `infra create/delete` (deprecated), `copilot consent *`, `extension *` management |

## Implementation Priority

1. **Phase 1 — Auth & Core Lifecycle**: `auth login`, `provision`, `deploy`, `up`, `down`
   — These are the highest-traffic commands with the most analytical value.

2. **Phase 2 — Config, Env, Pipeline**: `config *`, `env *`, `pipeline config`, `hooks run`
   — Understanding user configuration patterns and environment workflows.

3. **Phase 3 — Templates & Infrastructure**: `template *`, `monitor`, `show`, `infra generate/synth`
   — Template discovery and infrastructure generation insights.

4. **Phase 4 — Remaining**: `restore`, `build`, `package`, `add`, `completion`, extension management
   — Lower traffic or lower analytical value.
