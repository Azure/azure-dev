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
| Extensions (dynamic) | `extension.id`, `extension.version`, `extension.version.from`, `extension.version.to`, `extension.source`, `extension.source.from`, `extension.source.to`, `extension.dependency_of`, `extension.dependency_upgrade_count`, `extension.upgrade.outcome`, `extension.upgrade.duration_ms` + trace-context propagation to child process | Covers `ext.run`, `ext.install`, `ext.upgrade`, `ext.promote` events; upgrade/promote spans set source and dependency attributes |
| `mcp start` | Per-tool spans via `tracing.Start` with `mcp.client.name`, `mcp.client.version` | MCP event prefix `mcp.*` |
| `tool install` / `tool upgrade` / `tool check` / `tool list` / `tool show` | `tool.id`, `tool.ids`, `tool.dry_run`, `tool.install.strategy`, `tool.install.success`, `tool.install.success_count`, `tool.install.failure_count`, `tool.install.failed_ids`, `tool.install.duration_ms`, `tool.upgrade.from_version`, `tool.upgrade.to_version`, `tool.check.updates_available` | Comprehensive coverage in `cli/azd/cmd/tool.go`; install/upgrade emit `tools.pack.build` spans for pack-based tools |
| `copilot` (agent) | `copilot.initialize` event (model + reasoning config), `copilot.session` event (session create/resume) | Emitted from `internal/agent/copilot_agent.go`; covers the experimental copilot agent surface |
| `provision` | `validation.preflight` event (preflight outcome + 5 fields), 8 `arm.*` events (subscription / resource-group deploy / stack-deploy / what-if / validate), `aks.postprovision.skip`, per-layer `provision.layer.*` counts (`count`, `max_parallel`, `safe_fallback_count`, `explicit_dependson_count`) when multi-layer infra is used | Telemetry added across `internal/cmd/provision_*.go` and the ARM deployment client |
| `deploy` / `publish` / `package` | `deploy.appservice.zip` event (zip-deploy outcome), `container.credentials` / `container.publish` / `container.remotebuild` events for container-based services | Per-service-target instrumentation; container events emitted from container-app and ACR push paths |
| `hooks run` (and all hook-running commands) | `hooks.exec` event with `hooks.name` (hashed unless built-in lifecycle name), `hooks.type` (project / service / **layer**), `hooks.kind` (script runtime — `sh` / `pwsh` / `js` / `ts` / `python` / `dotnet`) | `hooks.type=layer` was added with multi-layer provision; pre/post is encoded in `hooks.name` (e.g., `prebuild` / `postbuild`); emitted from the hooks runner on every lifecycle command |

## Full Inventory Matrix

| Command | Subcommands | Global Span | Command-Specific Attrs | Feature Events | Notes |
|---------|-------------|:-----------:|:----------------------:|:--------------:|-------|
| **Auth** | | | | | |
| `auth login` | — | ✅ | ✅ | ❌ | `auth.method` (browser, device-code, service-principal-secret, etc.) |
| `auth logout` | — | ✅ | ❌ | ❌ | Global telemetry sufficient — no command-specific attributes emitted |
| `auth status` | — | ✅ | ❌ | ❌ | Global telemetry sufficient — simple pass/fail check |
| `auth token` | — | ✅ | ❌ | ❌ | Global telemetry sufficient |
| **Config** | | | | | |
| `config` | `show`, `list`, `get`, `set`, `unset`, `reset`, `list-alpha`, `options` | ✅ | ❌ | ❌ | Redundant — command name in global span captures operation |
| **Environment** | | | | | |
| `env` | `set`, `set-secret`, `select`, `new`, `remove`, `refresh`, `get-values`, `get-value` | ✅ | ❌ | ❌ | Redundant — command name in global span captures operation |
| `env list` | — | ✅ | ✅ | ❌ | `env.count` (measurement — number of environments) |
| `env config` | `get`, `set`, `unset` | ✅ | ❌ | ❌ | Thin wrappers — global telemetry sufficient |
| **Hooks** | | | | | |
| `hooks run` | — | ✅ | ✅ | ✅ | `hooks.name` (hashed unless built-in lifecycle name), `hooks.type` (project/service/**layer**), `hooks.kind` (script runtime — `sh`/`pwsh`/`js`/`ts`/`python`/`dotnet`); `hooks.exec` event emitted by the hooks runner on every lifecycle command |
| **Templates** | | | | | |
| `template` | `list`, `show` | ✅ | ❌ | ❌ | Redundant — command name in global span captures operation |
| `template source` | `list`, `add`, `remove` | ✅ | ❌ | ❌ | Redundant — command name in global span captures operation |
| **Pipeline** | | | | | |
| `pipeline config` | — | ✅ | ✅ | ❌ | `pipeline.provider` (`GitHub` / `Azure DevOps` — emitted as the resolved provider display name after auto-detection), `pipeline.auth` (emitted only when `--auth-type` is set: `federated` / `client-credentials`) |
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
| `provision` | — | ✅ | ✅ | ✅ | Emits `validation.preflight`, 8 `arm.*` events, `aks.postprovision.skip`, and per-layer `provision.layer.*` counts (`count`, `max_parallel`, `safe_fallback_count`, `explicit_dependson_count`) for multi-layer infra |
| `package` | — | ✅ | ✅ | ✅ | Via hooks middleware; container service targets emit `container.credentials`, `container.publish`, `container.remotebuild` events |
| `deploy` | — | ✅ | ✅ | ✅ | App Service zip-deploy emits `deploy.appservice.zip` (`deploy.appservice.linux`, `deploy.appservice.attempt`); container service targets emit `container.*` events |
| `publish` | — | ✅ | ✅ | ✅ | Same as `deploy` (alias behavior) |
| `up` | — | ✅ | ✅ | ✅ | Composes provision+deploy and inherits all their events; the up-graph runner emits `perf.provision_duration_ms`, `perf.deploy_duration_ms`, `perf.total_duration_ms` (`internal/cmd/up_graph.go`) |
| `down` | — | ✅ | ❌ | ❌ | Teardown flow; pre/postdown lifecycle hooks emit `hooks.exec` via the hooks middleware |
| **Add** | | | | | |
| `add` | — | ✅ | ❌ | ❌ | Low priority |
| **Completion** | | | | | |
| `completion` | `bash`, `zsh`, `fish`, `powershell`, `fig` | ✅ | ❌ | ❌ | Low priority — minimal analytical value |
| **VS Server** | | | | | |
| `vs-server` | — | ✅ | ❌ | ❌ | Long-running RPC; covered by `vsrpc.*` events |
| **Copilot Consent** | | | | | |
| `copilot consent` | `list`, `revoke`, `grant` | ✅ | ❌ | ❌ | Low priority |
| **Extension Management** | | | | | |
| `extension` | `list`, `show`, `install`, `uninstall`, `upgrade` | ✅ | ✅ | ✅ | Covered by `extension.*` fields and `ext.install`, `ext.upgrade`, `ext.promote` events |
| `extension source` | `list`, `add`, `remove`, `validate` | ✅ | ❌ | ❌ | Subcommand name in the global span captures the operation; `extension.source*` attributes are recorded by `extension upgrade` / `extension promote`, not by this subcommand |
| **Init** | | | | | |
| `init` | — | ✅ | ✅ | ✅ | Comprehensive coverage via `appinit.*` fields |
| **Update** | | | | | |
| `update` | — | ✅ | ✅ | ✅ | Covered by `update.*` fields |
| **MCP** | | | | | |
| `mcp start` | — | ✅ | ✅ | ✅ | Per-tool spans via `mcp.*` |
| **Tool Management** | | | | | |
| `tool list` | — | ✅ | ✅ | ❌ | `tool.ids` listed for visibility into per-row outputs |
| `tool install` | — | ✅ | ✅ | ✅ | `tool.id`, `tool.install.strategy`, `tool.install.success`, `tool.install.success_count`, `tool.install.failure_count`, `tool.install.failed_ids`, `tool.install.duration_ms`, `tool.dry_run`; `tools.pack.build` for pack-based tools |
| `tool upgrade` | — | ✅ | ✅ | ✅ | All `tool.install.*` plus `tool.upgrade.from_version`, `tool.upgrade.to_version` |
| `tool check` | — | ✅ | ✅ | ❌ | `tool.check.updates_available` (count) |
| `tool show` | — | ✅ | ✅ | ❌ | `tool.id` |
| **Copilot (Agent)** | | | | | |
| `copilot` | — | ✅ | ✅ | ✅ | `copilot.initialize` event captures model + reasoning config; `copilot.session` event tracks session create/resume |
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
| Hooks name | `hooks.name` | `hooks run` | Identifies which hook script ran (hashed — user-defined name) |
| Hooks type | `hooks.type` | `hooks run` | Distinguishes project / service / **layer** hooks |
| Hooks kind | `hooks.kind` | `hooks run` | Distinguishes the script runtime used to execute the hook (`sh`, `pwsh`, `js`, `ts`, `python`, `dotnet`) |
| Pipeline provider | `pipeline.provider` | `pipeline config` | Distinguishes GitHub vs Azure DevOps |
| Pipeline auth | `pipeline.auth` | `pipeline config` | Distinguishes federated vs client-credentials |
| Infra provider | `infra.provider` | `infra generate`, `infra synth` | Distinguishes Bicep vs Terraform |
| Tool ID | `tool.id` / `tool.ids` | `tool *` | Identifies which managed tool (e.g., bicep, gh, kubectl) the command acted on |
| Tool install metrics | `tool.install.*` | `tool install`, `tool upgrade`, first-run middleware | Success count, failure count, duration, strategy — quantitative install health |
| Tool upgrade versions | `tool.upgrade.from_version`, `tool.upgrade.to_version` | `tool upgrade` | Tracks adoption of new tool versions |
| Preflight outcome | `validation.preflight.outcome` (+ peer fields) | `provision` | Distinguishes passed / warnings-accepted / aborted local validation |
| ARM deployment events | `arm.deploy.*`, `arm.stack.deploy.*`, `arm.whatif.*`, `arm.validate.*` | `provision` | Distinguishes deployment scope (subscription vs resource-group) and operation kind (deploy / stack / what-if / validate) |
| Container events | `container.credentials`, `container.publish`, `container.remotebuild` | `package`, `deploy` | Per-stage container lifecycle for container-based services |
| Multi-layer provision | `provision.layer.*` | `provision` | Layer-graph shape counts for multi-layer infra: `count`, `max_parallel`, `safe_fallback_count`, `explicit_dependson_count` (integer measurements; no duration or outcome) |
| Performance durations | `perf.provision_duration_ms`, `perf.deploy_duration_ms`, `perf.total_duration_ms` | `up` | Per-phase and total wall-clock durations emitted by the up-graph runner (`internal/cmd/up_graph.go`); provision/deploy durations set only when those phases run |

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

## Cross-Cutting Subsystems

These telemetry surfaces are not tied to a single command — they emit from middleware
or shared infrastructure invoked by many commands. They are included here so the
privacy review covers every emission point.

| Subsystem | Trigger | Events | Key Attributes | Notes |
|-----------|---------|--------|----------------|-------|
| **Tool first-run middleware** | Wraps every interactive command | (none — enriches the active span) | `tool.firstrun.outcome`, `tool.firstrun.skip_reason`, `tool.firstrun.opt_in`, `tool.firstrun.tools_detected`, `tool.firstrun.tools_offered`, `tool.firstrun.tools_selected`, `tool.firstrun.tools_selected_names`, `tool.firstrun.tools_deselected_names`, `tool.firstrun.install_success_count`, `tool.firstrun.install_failure_count`, `tool.firstrun.install_failed_ids`, `tool.firstrun.install_duration_ms` | Records the first-run consent + tool-install flow; outcome key replaces deprecated boolean `tool.firstrun.completed` |
| **Hooks execution middleware** | Every lifecycle command (provision/deploy/up/down/restore/build/package/publish) | `hooks.exec` | `hooks.name` (hashed unless built-in lifecycle name), `hooks.type` (project / service / layer), `hooks.kind` (script runtime — `sh` / `pwsh` / `js` / `ts` / `python` / `dotnet`) | Layer-scope hooks added with multi-layer provision; pre/post is encoded in `hooks.name` (e.g., `prebuild` / `postbuild`), not in `hooks.kind` |
| **Preflight validation** | `provision` (prior to ARM deploy) | `validation.preflight` | `validation.preflight.outcome`, plus 4 peer fields covering warnings/errors counts and abort reason | Local-only validation; outcome captures passed / warnings-accepted / aborted |
| **ARM deployment client** | `provision` (any Bicep flow) | `arm.deploy.subscription`, `arm.deploy.resourcegroup`, `arm.stack.deploy.subscription`, `arm.stack.deploy.resourcegroup`, `arm.whatif.subscription`, `arm.whatif.resourcegroup`, `arm.validate.subscription`, `arm.validate.resourcegroup` | ARM operation status + duration | Per-call instrumentation in the ARM client; covers regular + stack deployments at both scopes |
| **Multi-layer provision** | `provision` (when `infra.layers[]` is configured in `azure.yaml`) | (none — enriches the `provision` span) | `provision.layer.count`, `provision.layer.max_parallel`, `provision.layer.safe_fallback_count`, `provision.layer.explicit_dependson_count` | All four are integer measurements emitted from `internal/cmd/provision_graph.go`; no per-layer duration or outcome attribute is emitted |
| **Execution graph (scheduler)** | `up`, `provision`, `deploy`, `package`, `publish`, `down` | `exegraph.run`, `exegraph.step` | `exegraph.step.count`, `exegraph.max_concurrency`, `exegraph.error_policy`, `exegraph.step.name` (hashed), `exegraph.step.deps` (hashed slice), `exegraph.step.tags` (raw — hardcoded literals only), `exegraph.step.timeout_s` | Step names embed user-defined service / layer names from `azure.yaml`; both `name` and `deps` use `fields.StringHashed` / `fields.StringSliceHashed` |
| **Container lifecycle** | `package`, `deploy` (container service targets) | `container.credentials`, `container.publish`, `container.remotebuild` | `container.publish` sets `container.remotebuild` (bool) only; `container.credentials` and `container.remotebuild` set no attributes (span status carries success/failure and duration) | The hashed `pack.builder.image` / `pack.builder.tag` attributes are emitted on the separate `tools.pack.build` span, not the `container.*` spans |
| **App Service deploy** | `deploy`, `publish` (App Service targets) | `deploy.appservice.zip` | `deploy.appservice.linux` (bool), `deploy.appservice.attempt` (retry attempt number) | Zip-deploy path only; outcome / duration are carried by the span status and span timing, not by dedicated attributes |
| **AKS service target** | `provision` (AKS preprovision/postprovision) | `aks.postprovision.skip` | Skip reason | Recorded when cluster is not yet available for context setup |
| **Agent troubleshoot middleware** | Triggered on command failure when troubleshooting is engaged | `agent.troubleshoot` | Error chain attributes, hashed error fields | Emitted from `cmd/middleware/error.go` |
| **Up-graph performance** | `up` (graph execution) | (none — enriches the `up` command span) | `perf.provision_duration_ms`, `perf.deploy_duration_ms`, `perf.total_duration_ms` | Emitted from `internal/cmd/up_graph.go` after the graph completes; provision/deploy durations set only when those phases run |
| **VS RPC** | `vs-server` long-running session | `vsrpc.*` (event prefix) | Per-RPC attributes documented in `telemetry-schema.md` | Long-running RPC server for VS integration |
| **Project config load** | Any command that loads `azure.yaml` (`provision`/`deploy`/`up`/`down`/etc.) | (none — enriches the active command span) | `foundry.agent.legacy_config_shape` (bool) | Emitted from `pkg/project/project.go` `Load`; set to `true` only when a `host: azure.ai.agent` service still carries a populated `config:` block (the deprecated config-nested Foundry agent shape). Tracks migration onto the unified `azure.yaml` shape |
