# Feature-Telemetry Inventory Matrix

This document provides a comprehensive inventory of every `azd` command and its telemetry coverage.
It identifies gaps where commands rely solely on the global middleware span and recommends
specific telemetry additions.

## Telemetry Coverage Legend

| Symbol | Meaning |
|--------|---------|
| ✅ | Covered — command-specific attributes or events are emitted |
| ⚠️ | Global span only — no command-specific telemetry |
| ❌ | Gap identified — needs instrumentation |
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

| Command | Subcommands | Global Span | Command-Specific Attrs | Feature Events | Notes |
|---------|-------------|:-----------:|:----------------------:|:--------------:|-------|
| **Auth** | | | | | |
| `auth login` | — | ✅ | ✅ | ❌ | `auth.method` (browser, device-code, service-principal-secret, etc.) |
| `auth logout` | — | ✅ | ✅ | ❌ | `auth.method` (logout) |
| `auth status` | — | ✅ | ❌ | ❌ | Global telemetry sufficient — simple pass/fail check |
| `auth token` | — | ✅ | ❌ | ❌ | Global telemetry sufficient |
| **Config** | | | | | |
| `config` | `show`, `list`, `get`, `set`, `unset`, `reset`, `list-alpha`, `options` | ✅ | ❌ | ❌ | Redundant — command name in global span captures operation |
| **Environment** | | | | | |
| `env` | `set`, `set-secret`, `select`, `new`, `remove`, `refresh`, `get-values`, `get-value` | ✅ | ❌ | ❌ | Redundant — command name in global span captures operation |
| `env list` | — | ✅ | ✅ | ❌ | `env.count` (measurement — number of environments) |
| `env config` | `get`, `set`, `unset` | ✅ | ❌ | ❌ | Thin wrappers — global telemetry sufficient |
| **Hooks** | | | | | |
| `hooks run` | — | ✅ | ✅ | ❌ | `hooks.name`, `hooks.type` (project/service) |
| **Templates** | | | | | |
| `template` | `list`, `show` | ✅ | ❌ | ❌ | Redundant — command name in global span captures operation |
| `template source` | `list`, `add`, `remove` | ✅ | ❌ | ❌ | Redundant — command name in global span captures operation |
| **Pipeline** | | | | | |
| `pipeline config` | — | ✅ | ✅ | ❌ | `pipeline.provider` (github/azdo), `pipeline.auth` (federated/client-credentials) |
| **Monitor** | | | | | |
| `monitor` | — | ✅ | ❌ | ❌ | Redundant — command name in global span is sufficient |
| **Show** | | | | | |
| `show` | — | ✅ | ❌ | ❌ | Redundant — output format not analytically useful |
| **Infrastructure** | | | | | |
| `infra generate` | — | ✅ | ✅ | ❌ | `infra.provider` (bicep/terraform) |
| `infra synth` | — | ✅ | ✅ | ❌ | `infra.provider` (bicep/terraform) |
| `infra create` | — (hidden, deprecated) | ✅ | ❌ | ❌ | Wraps `provision`; inherits its telemetry |
| `infra delete` | — (hidden, deprecated) | ✅ | ❌ | ❌ | Wraps `down`; inherits its telemetry |
| **Core Lifecycle** | | | | | |
| `restore` | — | ✅ | ❌ | ❌ | Via hooks middleware |
| `build` | — | ✅ | ❌ | ❌ | Via hooks middleware |
| `provision` | — | ✅ | ❌ | ❌ | `infra.provider` set via hooks middleware |
| `package` | — | ✅ | ❌ | ❌ | Via hooks middleware |
| `deploy` | — | ✅ | ❌ | ❌ | `infra.provider`, service attributes via hooks middleware |
| `publish` | — | ✅ | ❌ | ❌ | Same as `deploy` (alias behavior) |
| `up` | — | ✅ | ❌ | ❌ | `infra.provider` via hooks middleware (composes provision+deploy) |
| `down` | — | ✅ | ❌ | ❌ | `infra.provider` via hooks middleware |
| **Add** | | | | | |
| `add` | — | ✅ | ❌ | ❌ | Low priority |
| **Completion** | | | | | |
| `completion` | `bash`, `zsh`, `fish`, `powershell`, `fig` | ✅ | ❌ | ❌ | Low priority — minimal analytical value |
| **VS Server** | | | | | |
| `vs-server` | — | ✅ | ❌ | ❌ | Long-running RPC; covered by `vsrpc.*` events |
| **Copilot Consent** | | | | | |
| `copilot consent` | `list`, `revoke`, `grant` | ✅ | ❌ | ❌ | Low priority |
| **Extension Management** | | | | | |
| `extension` | `list`, `show`, `install`, `uninstall`, `upgrade` | ✅ | ❌ | ❌ | Covered by `extension.*` fields |
| `extension source` | `list`, `add`, `remove`, `validate` | ✅ | ❌ | ❌ | Low priority |
| **Init** | | | | | |
| `init` | — | ✅ | ✅ | ✅ | Comprehensive coverage via `appinit.*` fields |
| **Update** | | | | | |
| `update` | — | ✅ | ✅ | ✅ | Covered by `update.*` fields |
| **MCP** | | | | | |
| `mcp start` | — | ✅ | ✅ | ✅ | Per-tool spans via `mcp.*` |
| **Disabled** | | | | | |
| `version` | — | 🚫 | — | — | Intentionally disabled |
| `telemetry upload` | — | 🚫 | — | — | Intentionally disabled |

## Retained Fields Summary

After the redundancy audit (per PR review feedback from @weikanglim), the following
command-specific telemetry fields provide analytical value beyond the command name:

| Field | OTel Key | Commands | Justification |
|-------|----------|----------|---------------|
| Auth method | `auth.method` | `auth login`, `auth logout` | Distinguishes authentication flow type (browser, device-code, SP, federated, etc.) |
| Env count | `env.count` | `env list` | Measurement — number of environments is a quantitative metric |
| Hooks name | `hooks.name` | `hooks run` | Identifies which hook script ran |
| Hooks type | `hooks.type` | `hooks run` | Distinguishes project vs service hooks |
| Pipeline provider | `pipeline.provider` | `pipeline config` | Distinguishes GitHub vs Azure DevOps |
| Pipeline auth | `pipeline.auth` | `pipeline config` | Distinguishes federated vs client-credentials |
| Infra provider | `infra.provider` | `infra generate`, `infra synth` | Distinguishes Bicep vs Terraform |

### Removed Fields (Redundant with Command Name)

The following fields were removed because the command name in the global span already
captures the operation type, making the attribute redundant:

| Removed Field | Reason |
|---------------|--------|
| `auth.result` | Success/failure already captured by span status |
| `config.operation` | Each config subcommand has its own command name |
| `env.operation` | Each env subcommand has its own command name |
| `template.operation` | Each template subcommand has its own command name |
| `monitor.type` | Single command — no distinguishing value |
| `show.output.format` | Output format not analytically useful |
