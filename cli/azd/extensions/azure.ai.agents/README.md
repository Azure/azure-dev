# Azure Developer CLI (azd) Agents Extension

## Preview feature opt-ins

Four capabilities are off by default. Each one is turned on separately.

| Feature ID | Scope | Environment variable |
| --- | --- | --- |
| `agents.managed_harness` | Prompt agents using `microsoft_copilot_preview` or legacy `github_copilot_preview` harnesses | `AZD_ALPHA_ENABLE_AGENTS_MANAGED_HARNESS` |
| `agents.voice` | Voice / prompt-voice agents, including hosted voice bridges with `invocations_ws` | `AZD_ALPHA_ENABLE_AGENTS_VOICE` |
| `agents.long_running` | Starting background Responses invocations with `--long-running` / `--no-wait` | `AZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING` |
| `agents.a2a` | A2A init selection, explicit wire versions, and the 1.0 message default; existing A2A 0.3 invocation remains available without opt-in | `AZD_ALPHA_ENABLE_AGENTS_A2A` |

Set the desired variable in your shell or CI before running azd:

```bash
export AZD_ALPHA_ENABLE_AGENTS_MANAGED_HARNESS=true
export AZD_ALPHA_ENABLE_AGENTS_VOICE=true
export AZD_ALPHA_ENABLE_AGENTS_LONG_RUNNING=true
export AZD_ALPHA_ENABLE_AGENTS_A2A=true
```

In PowerShell, for example:

```powershell
$env:AZD_ALPHA_ENABLE_AGENTS_VOICE = 'true'
```

Only `1`, `t`, `T`, `true`, `TRUE`, or `True` enable a feature. False, empty,
missing, and invalid values leave it off. The evaluator reads the current
process environment without querying azd config or loading environment files.
Values already injected by the azd host count; the selected azd environment
can override the same key from the shell or CI.

User config keys such as `alpha.agents.voice` and `alpha.all`, and the former
`AZD_FOUNDRY_EXT_ENABLE_*` variables, do not enable these previews. Ordinary
hosted and plain prompt agents need no opt-in.

Root help always lists the canonical switches and their scopes. While a
feature is off, its command-specific flags and help sections are hidden.
Enabling a feature preserves the existing help without adding new text or
revealing flags that were already hidden, including `--harness`.
This covers `invoke --long-running`, `invoke --no-wait`, the
`invocations` group, `init --voice`, and the `prompt-voice` choice of
`init --kind`. Hidden commands still run. Using a disabled preview fails with a
`feature_not_enabled` error that names the preview feature ID and suggests
setting its canonical variable to `true` in the shell or CI.

Checks cover preview flags on init and invoke, `agent add` definitions,
existing and staged project definitions, the selected service for invoke, run,
optimize submissions, optimize deploy, optimize apply, and endpoint update,
provisioning, and package, publish, and deploy. The init
picker omits voice while it is off. Hosted init also omits `invocations_ws`
from protocol help and selection while voice is off. Explicit `--protocol`
values, including repeated flags and comma-separated lists, require the voice
opt-in for `invocations_ws`. Protocol selection completes before project or
environment creation; ordinary protocols and the no-prompt Responses default
need no additional feature check. Template catalogs do not identify
capabilities, so templates are checked after staging and before adoption.
Explicit remote `--template` inputs are staged and checked before Azure
authentication, including `$ref` definitions. Adoption reuses that directory.

### Enforcement limits

- An explicit HTTP `--agent-endpoint` stays unclassified. The extension does
  not query its remote definition, so the managed harness check does not cover
  that route. The long-running check still applies. Text invoke does not
  accept voice WebSocket endpoints.
- Read and cleanup commands stay available with features off. This includes
  show, endpoint show, invocations show, follow, and cancel, delete, and down.
- These checks do not govern requests from the evaluations or routines
  extensions.

See [the design](docs/specs/preview-feature-flags.md) for source and error semantics.

## Hosted agent deployment preview

With azd 1.37.0 or later and an existing environment bound to a Microsoft Foundry
project, use the core deployment command:

```bash
azd deploy <service> --preview --no-prompt
azd deploy <service> --preview --no-prompt --output json
```

Preview supports `host: azure.ai.agent`, `kind: hosted` definitions declared at
service level in unified `azure.yaml`. Legacy `agent.yaml`/`agent.manifest.yaml`
projects, whole agent-file references, deprecated nested `config`, and
`AGENT_DEFINITION_PATH` overrides return an unsupported error with migration
guidance. Local references to individual fields or fragments remain supported.
Unused legacy files do not override an inline definition. Prompt, voice, and
workflow agent definitions are outside this preview's scope. There is no
standalone `azd ai agent deploy` or `--dry-run` command.

The provider reads the latest remote agent version and compares the declared,
in-scope properties with its configuration, using normal deployment normalization. It
reports `create` on a missing agent, `update` for known differences, `noChange`
when the in-scope known configuration matches, or `unknown` when it matches but
an in-scope input or image-source choice is unresolved.

Comparison and output are limited to six groups: metadata (name, description,
and `metadata` tags), protocols, resources (CPU/memory), environment variables,
model deployment reference, and container image/build-push intent. Code settings,
session settings, content safety, endpoint settings, agent cards, and unavailable
artifact contents are not compared or reported, and do not affect change counts
or statuses. Additions show the desired value, updates show `before -> after`,
and removals show the old value followed by `(removed)`.

Absent properties and empty groups are omitted from both readable and structured
changes. Field/fragment references retain their effective authored presence.
Normalization-only defaults, including undeclared CPU/memory, protocols, and
`enableVnextExperience` metadata, do not create differences. Declared zero, false,
and empty values are not treated as absent; normal value normalization and
credential sanitization still apply. Remote-only optional metadata/environment/image properties
remain real removals when the new request removes them.

Ordinary in-scope values are visible, including descriptions, metadata, protocol
versions, CPU/memory, and literal service environment bindings. There is no
blanket redaction based on a property's name or free-text type. Tags are shown
as logical lists or scalar strings; false, zero, and empty values keep their types.
Fields outside the six groups remain excluded.

Credential protection is targeted at environment-resolution boundaries.
Preview reads the effective authored source, including references, to distinguish
literal environment bindings from `${VAR}` substitutions. Substituted bindings
and remote-only bindings without authored provenance remain protected; the
public model deployment identifier remains visible. Foundry `${{...}}`
expressions stay unevaluated and visible. Known resolved values and protected
previous remote binding values are also removed if embedded in descriptions,
metadata, tags, or other displayed strings. Current source cannot recover
historical secret provenance when a binding is now literal; it does not guess
sensitivity from names.
Canonical field paths, map keys, and service/agent identifiers are not subjected
to secret-substring replacement; a short credential cannot corrupt their names.
URL usernames/passwords, query strings, and fragments are always stripped, and
terminal control characters are neutralized. Keep credentials in environment
substitutions rather than inline configuration. Sanitization affects display
only, not comparison.

Agent tags belong under the service's `metadata.tags`, not project/resource
provisioning tags. Both ordinary deployment and preview accept a string or a
list of strings:

<!-- azd:doc-example partial -->
```yaml
services:
  my-agent:
    host: azure.ai.agent
    metadata:
      tags:
        - customer-support
        - responses
```

Foundry agent metadata is a string-valued dictionary. The extension stores a
tag list as a JSON array string at `metadata.tags`, preserving commas and empty
items; existing string values are unchanged. List additions/removals therefore
appear as an `update: metadata.tags` change. Removing the entire `tags` property
reports `remove: metadata.tags` if the latest remote version contains it.
Explicit `tags: []` and `tags: ""` remain present values, not missing properties.
Invalid tag types fail instead of silently disappearing. Preview decodes the
known JSON string-list form for display: additions show all desired tags,
updates show the old and new lists, and removals show the previous tags. JSON
uses the same sanitized logical arrays or scalar strings. Explicit empty lists
and strings remain `[]` and `""`. Comparison uses the actual values before
sanitization, so different credentials still produce a change even when their
display values are both redacted.

Preview compares against the latest deployed version, not a previous local
file revision. A deleted local tag that was never deployed cannot appear as a
remote removal; a missing remote agent reports creation and only additions.
If the remote version has no tags, preview reports `add` with the full desired
list, not individual removals from a previous local edit.

For example:

```text
  Metadata:
    update: description: "A basic responses agent." -> "A helpful responses agent."
    update: metadata.tags: ["retained","removed"] -> ["retained","added"]
  Resources:
    update: definition.cpu: "0.5" -> "2"
  Environment variables:
    update: definition.environment_variables.MODE: "production" -> "development"
    add: definition.environment_variables.API_KEY: "[redacted]"
  Container image:
    build: true
    push: true
```

Image passthrough compares the configured image reference, including a private
registry connection. The Container image group and optional JSON `containerImage`
intent are change-only: creating a container-based agent or changing its known
image/registry configuration can include relevant build/push intent. Unchanged
container configuration is omitted, even if normal deployment would always
rebuild. A changed passthrough image shows its sanitized reference diff and
`build: false` / `push: false`; a relevant container build reports true/true,
including remote builds, without inventing a resulting tag/digest.

Code-only services have no container intent or synthetic false/false section;
code packaging and upload content remain excluded. Switching from a deployed
container definition to code can still report removal of the old image reference.
If a normal interactive deployment would ask whether to build or use a configured
image that differs from the deployed reference, the image choice and build/push
intent remain unknown even when there are no other differences. An unchanged
configured reference still omits container intent. With `--no-prompt`, the normal default is
build. The legacy `AZD_AGENT_SKIP_ACR=true` marker selects a configured pre-built
image.

Equal image tags do not prove their mutable content is unchanged. Unset `${VAR}`
inputs in service `env` or a selected passthrough `image` remain unknown rather
than empty-value changes; explicit empty values and `${VAR:-default}` retain
their ordinary meanings. Foundry `${{...}}` expressions are preserved, not
evaluated or resolved to credentials.

The existing host JSON envelope contains `timestamp` and `services`. Provider
results are at `services.<service>.data`, with `service`, `agent`, `status`,
`changes` (each has `group`, `path`, `operation` and the applicable sanitized
`before`/`after` values), `unknown`, and `notes`. Optional `containerImage` has
boolean `build`/`push` intent for relevant container changes (null when a relevant
image-source choice is unresolved); it is absent for code-only and unchanged
container configurations. JSON
uses the same sanitized values as readable output.
Create, update, no-change, and unknown previews succeed; configuration,
authentication, permission, connectivity, and malformed-response errors fail.
`noChange` describes only the declared, in-scope configuration and actual optional
removals, not normalization-only defaults or artifact content equivalence:
ordinary deploy still creates a new agent version.

### Read-only boundary and inherited host limitations

The provider runs on a fresh instance without `Initialize`, uses only project,
environment, and tenant reads plus the Foundry agent GET, and returns output to
the host. Before credential creation or remote calls, it requires an HTTPS project
endpoint whose parsed hostname is exactly `ai.azure.com` or a subdomain ending in
`.ai.azure.com`; suffix lookalikes and other domains are rejected without echoing
the endpoint. It never calls deployment writes, prompts, builds, uploads, provisioning,
or extension-owned state persistence. Normal deployment remains on the stable
service-target lifecycle. Sensitive provider values are redacted and
credential-bearing URLs are sanitized; provider errors do not echo raw API bodies
or authored values.

The `azure.ai.project` provider returns a silent deployment no-op preview, so a
project dependency does not produce an unsupported-preview warning. The host
still displays its per-service progress line; project JSON explicitly marks
deployment as a no-op, not an infrastructure comparison.

The unchanged azd host skips package/publish/deploy and deployment hooks, but its
ordinary project/environment-loading path still runs. Environment selection or
loading can prompt, download remote environment state, or save host-managed
environment defaults before the provider is called. This is not a host-wide
no-filesystem-writes guarantee; use an already initialized, selected local
environment with `--no-prompt`. Services filtered out by conditions are not
previewed. Hosts without a preview provider skip that service with a warning;
skipped services are absent from JSON results. Preview does not evaluate
infrastructure/dependency changes or deployment readiness, and compares the latest
version, not necessarily the version receiving endpoint traffic. The host rejects
`--timeout` and non-empty `--from-package` with `--preview`.

Offline regression coverage includes an
[integration runner against the unchanged main host](tests/host-preview/README.md).
The optional [live preview scenario](tests/cli-interactive-tester-scenarios/tier2/2.17a-deploy-preview.yaml)
is driven through `foundry-extension-scenario-orchestrator`; its setup incurs Azure
cost and is not run automatically.

## Developer Foundry access

Agent predeploy uses the shared core `pkg/foundry` access preparation, also used
by toolbox writes. A non-mutating agent-list probe checks effective data-plane
access first, so working custom or externally managed permissions do not
require role-assignment writes. On a confirmed authorization denial, it checks
inherited Foundry roles and attempts a project-scoped Foundry User assignment
only when needed, then waits for bounded propagation before deployment.
Optional role-inspection or assignment failures warn and let deployment report
the service result. Cancellation or an exhausted post-grant readiness check
stops predeploy; wait and retry if propagation takes longer.

Preparation requires a matching `AZURE_AI_PROJECT_ID` and
`FOUNDRY_PROJECT_ENDPOINT` (falling back to `AZURE_AI_PROJECT_ENDPOINT`), with no
conflicting `AZURE_SUBSCRIPTION_ID`. Guest users and service principals use the
host's user-access tenant for credentials and resource-tenant principal for
assignments. Missing or inconsistent project context warns without granting.

`AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true` disables developer access preparation
and hosted-agent permission checks. A non-empty active azd environment value
takes precedence over the process environment; absent or empty values fall
back to the process. Hosted agents retain their separate role-assignment-write
and ACR classic/ABAC checks. Provisioning and read-only diagnostics do not
automatically grant developer roles.

This shared API must be released in core before updating the independently
published extension's azd SDK dependency. Local cross-module builds can use a
Go workspace; do not commit local module replacements.

## Extension telemetry API

Requires azd 1.37.0 or later. Telemetry, deployment preview, and developer RBAC
checks use stable v1 APIs, including `Account().GetCurrentPrincipal`.

Extension code reports best-effort usage events through the shared
`pkg/foundry/telemetry` reporter. Extension-owned event builders remain in
`internal/telemetry`:

```go
reporter := foundryTelemetry.NewReporter(azdClient.Telemetry(), nil)
reporter.Report(ctx, extensionTelemetry.LocalClientRouteSelected(route))
```

`Report` has no return value and never changes the command result. It applies a
one-second timeout, never retries, and writes only the event name and gRPC status
code to the debug log when reporting fails. Attribute values and transport error
details are not logged.

Define event names, attribute keys, and bounded values in
`internal/telemetry/events.go`. Do not call `ReportUsage` directly from command
or provider code. Events must contain low-cardinality product metadata only;
never include prompts, responses, resource or service names, IDs, paths, URLs,
connection values, or other customer content. The azd host records events only
for extensions installed from the official registry.

Init also records [business-step elapsed times](docs/init-telemetry.md), including
loading and interaction together. The other events are documented under
[Agent context telemetry](#agent-context-telemetry),
[Operation classification markers](#operation-classification-markers),
[Remote invoke adoption telemetry](#remote-invoke-adoption-telemetry), and
[Local client route telemetry](#local-client-route-telemetry).

### Agent context telemetry

When azd telemetry is enabled, the extension reports `agent.context.resolved`
for each distinct agent classification involved in an invocation. The event
contains only bounded classifications (including a container mode for hosted agents):

| Attribute | Values | Description |
|---|---|---|
| `ext.agent.kind` | `hosted`, `prompt`, `prompt-voice`, `voice`, `workflow`, `unknown` | Resolved agent kind. |
| `ext.agent.operation` | Extension command path | Operation sharing the event's trace. |
| `ext.agent.container.mode` | `build`, `code`, `passthrough`, `passthrough_auth`, `unknown` | Optional for hosted agents. Classifies the configured deployment path; `passthrough_auth` means a registry connection is configured for auth. |

The event is correlated with other telemetry from the same azd invocation by
the OpenTelemetry operation ID. A project with multiple agent classifications
reports one row for each distinct classification; it does
not count individual agents or prove a deployment succeeded. `build` includes
both azd-created and existing ACR destinations. Ambiguous legacy or invalid
hosted configurations, and those with an `AGENT_DEFINITION_PATH` override,
are reported as `unknown`. The event never includes agent names, service keys,
registry connections, image references, paths, URLs, prompts, or other customer content.

### Remote invoke adoption telemetry

`agent.invoke.selected` reports the selected mode once a remote invoke has
resolved its protocol and target. For project-backed routes, only hosted-agent
services are counted; an explicit `--agent-endpoint` has no project service kind
to verify. The event runs before the invoke request, not necessarily before
authentication: protocol or target resolution can check whether a brownfield
agent exists in Foundry. Failures before resolution are not counted, while
later request failures do not prevent the usage report. Local and non-hosted
project routes (prompt, voice, workflow) are excluded.

| Attribute | Values | Description |
|---|---|---|
| `ext.agent.invoke.protocol` | `responses`, `invocations`, `a2a` (currently) | Resolved invocable protocol. |
| `ext.agent.invoke.long_running` | `true`, `false` | String-encoded choice of `--long-running`; supported for remote Responses only. |
| `ext.agent.invoke.no_wait` | `true`, `false` | String-encoded choice of `--no-wait`; requires `--long-running`. |

This records command-path adoption, not whether the service accepted or
completed work. No prompt, agent name, endpoint, or service response is sent.

### Operation classification markers

Init, provision and deploy also emit bounded
`agent.operation.v1.<operation>.<category>.<telephony>` values in the existing
`extension.event` field of `ext.usage`, with no additional attributes. Existing
`agent.context.resolved` and command results are unchanged. See
[operation statistics](docs/operation-telemetry.md) for the vocabulary, query and
coverage limits. Marker success must not be used as command success.

## Non-interactive automation

See the shared [AI extension non-interactive input reference](../ai-non-interactive.md)
for every prompt's flag, environment/configuration input, or deterministic
no-prompt behavior.

## Project storage diagnostics

Run `azd ai agent doctor` to check project managed identity permissions for the
Storage connections named in the project capability host's `storageConnections`.
The `Project storage permissions` check resolves those names from project
connections or account connections shared with the project, using Storage
resource IDs rather than probing arbitrary endpoints. Unbound connections are
ignored. Projects without a capability host or Storage bindings are skipped.
Unreadable or incomplete capability host metadata produces a warning. See
[capability hosts](https://learn.microsoft.com/azure/foundry/agents/concepts/capability-hosts)
for the project storage binding model.

The check recognizes direct assignments of Storage Blob Data Contributor,
Storage Blob Data Owner, and equivalent built-in roles with Blob read, write,
and delete data permissions, accounting for `NotDataActions`. Assignments can
be inherited from an ancestor scope. Supported project-identity authentication
includes AAD and ProjectManagedIdentity connections.

Connections using account keys, SAS, or a separate service principal are skipped.
Missing metadata, unsupported identity selection, unreadable assignments, and
unresolved custom or conditional permissions produce a warning instead
of a missing-permission claim. Container-scoped Blob grants also produce a warning
when the project's exact container access cannot be verified; the check does not
recommend expanding those grants to the entire account. Unrecognized role
definitions are read as needed. Managed identity group memberships are not
resolved by this check: when sufficient direct permissions are absent, it warns
that group access remains unverified instead of claiming permissions are missing.
Invalid bound connection or identity configuration still fails the check.

The check never reads connection secrets, accesses
blob data, or creates role assignments. A pass does not verify network access.

Use `--debug` for per-connection findings and `--unredacted` to include identity
and resource identifiers when sharing them is safe. `--local-only` skips this
remote check along with the other remote diagnostics.

## Choosing a Foundry project name

During interactive `azd ai agent init`, azd prompts for the name of a new
Microsoft Foundry project. If the current azd environment name is valid for
the generated infrastructure, it is offered as the default. The name must be
3-32 characters, start with a letter or number, and contain only letters,
numbers, or hyphens.

To configure the name, set it in the active azd environment before running
init:

```bash
azd env set AZURE_AI_PROJECT_NAME my-foundry-project
```

An existing `AZURE_AI_PROJECT_NAME` value is offered as the default during
interactive new-project setup. `--no-prompt` remains non-interactive and keeps
its existing automatic environment-name fallback.

## Composing Agent Dependencies

Use the Agent command surface to attach existing Toolbox or Connection services
to an Agent service in `azure.yaml`:

```bash
azd ai agent toolbox add support-tools --agent research-agent
azd ai agent connection add search-connection --agent research-agent
```

These commands add the dependency service key to `services.<agent>.uses`. They
do not create or deploy the dependency. Toolbox and Connection configuration and
lifecycle behavior remain owned by the `azure.ai.toolboxes` and
`azure.ai.connections` extensions.

**Breaking change:** the dependency type now precedes the verb:
`azd ai agent <toolbox|connection> add <service> --agent <agent>`.
The previous `azd ai agent add <type> ...` form is no longer supported for
dependency attachment. The root `azd ai agent add <service> --file <path>`
command adds or updates one Agent service. Attach dependencies with the
Toolbox or Connection commands above.

If a toolbox is declared inline on an agent, move its definition to an
independent `azure.ai.toolbox` service before deployment. If the new service key
differs from the original toolbox name (for example, `My Tools` becomes
`MyTools`), replace the inline entry in the agent's `toolboxes` list with the
new service key. Then attach that service with the command above and run `azd deploy`.
The add command only updates the agent's `uses` list; it does not rewrite
`toolboxes`, create the service, or deploy it.

Bundled `connections` and full `toolboxes` definitions on `azure.ai.agent` are
not supported, including definitions loaded through `$ref`. Move connections
to `azure.ai.connection` services and attach them through `uses`. Agent
`toolboxes` accepts strings or name-only objects referencing local
`azure.ai.toolbox` services, including name-only objects loaded through local
`$ref` files (with an optional `name` override). Referenced full definitions
remain unsupported. To reuse an external toolbox, set `endpoint` on
its split toolbox service instead of setting a legacy MCP environment marker.
Run `azd deploy --all` to reconcile these dependencies before their agents;
`azd provision` does not create Connections or Toolboxes. Unified projects must
declare Connection and Toolbox resources as sibling services. Agent runtime
`toolConnections` and environment references remain agent-owned.

The legacy singular `toolbox` field is not supported; use `toolboxes` to
reference local `azure.ai.toolbox` service keys.

For harnessed prompt agents, each Toolbox service key becomes its generated MCP
`server_label`. To customize approval or tool options, add an MCP entry under
`tools` with that service key as its `server_label` and omit `server_url`; azd
fills the deployed endpoint and authenticated connection. An existing MCP entry
with the exact deployed URL keeps its authored label and options, while azd sets
its authentication connection. A different explicit URL is not automatically
repinned: remove it to follow the Toolbox service, or use a distinct label to
keep it as an independent tool. Conflicting labels fail before connection setup.
References to the same endpoint share one attachment. Configure at most one MCP
entry for that endpoint or its service keys; competing overrides are rejected
before connection setup rather than silently merging their options.

Prompt agents (`kind: prompt`) may also declare `connections` as a list of
local `azure.ai.connection` service keys or effective resource names. An
effective name comes from the service's `name` after resolving local `$ref`
files; absent or blank names fall back to the service key. An exact service-key
match takes precedence over a resource-name match. These are references, not
resource definitions: the matching service key must be in `uses` and deployed
to the same project before the prompt agent. Connection objects remain
unsupported on any agent.

## Deploying Agents

Deploy Agents through the normal azd project lifecycle:

- `azd deploy <service>` deploys the selected `azure.ai.agent` service.
- `azd deploy --all` deploys all services, with ordering defined by `uses`.
- `azd up` provisions and deploys the project.

After a successful deployment, endpoint guidance recommends the standalone
`azd extension install azure.ai.evaluations` followed by `azd ai eval init`
when `azure.yaml` does not
declare an `azure.ai.eval` service. When an evaluation service is already
declared, the setup guidance is omitted. This note never recommends any
nested `azd ai agent eval` subcommand. A service README replaces the invocation
documentation link, not the evaluation setup guidance.

**Deprecated:** `azd ai agent eval` commands only display migration guidance
and exit non-zero; `--help` remains available. Use `azd ai eval` from
`azure.ai.evaluations` instead. Old arguments are not forwarded, and legacy
evaluation configuration is not automatically converted.

`azd ai eval init` scaffolds evaluation configuration; follow the
[evaluations guide](../azure.ai.evaluations/README.md) for deployment and run steps.
The `azd ai agent show <service>` and `azd ai agent invoke <service>` next steps
use the local service key from `azure.yaml`, which can differ from the deployed
agent name shown in the endpoint.

For adopted prompt agents, deploy and invoke resolve the template model against
the active environment's deployment choices. `AI_PROJECT_DEPLOYMENT_BINDINGS` is
an environment-local JSON object mapping each template deployment name to its
selected deployment name. An empty target records an explicit skip. These
names are matched case-insensitively, and updates preserve existing declaration-key
spelling. Targets that differ only by case are equivalent; genuinely conflicting
targets for keys that differ only by case are rejected. Bindings
preserve independent choices for multiple declarations and take
precedence over the project-wide runtime default. A skipped model, an ambiguous
legacy replacement, or malformed deployment configuration returns an error
rather than silently using another model. Model-resolution failures do not block
`azd ai agent show` or `azd ai agent delete`.

When adopting a template with `azd ai agent init --model-deployment`, init selects
the template declaration to replace before copying project files or creating an
environment. An ambiguous `--no-prompt` request or a cancelled replacement picker
leaves the target unchanged. Template variable references in the selected
declaration name are expanded against the final environment when applying the
binding, with persisted values taking precedence over process values.

For on-disk Bicep or Terraform infrastructure, deployment parameters are
authoritative, not `AI_PROJECT_DEPLOYMENTS`. Ejection seeds deployment parameters
from the adoption choices, and explicit per-declaration bindings remain effective
after ejection. Without such a binding, deploy and invoke expand the agent's
authored model without inferring replacements from the managed deployment list.
Set the agent's model explicitly or use
`${AZURE_AI_MODEL_DEPLOYMENT_NAME}` for an environment-specific choice.

**Breaking change:** `azd ai agent deploy [path]` has been removed. The extension
still implements Agent deployment as a service target invoked by core azd;
there is no separate definition-file deployment or sibling-Toolbox orchestration
path in the Agent command tree.

For an existing agent source project, declare an `azure.ai.agent` service in
`azure.yaml` with its source directory and deployment settings. The definition
can be inline or referenced using `$ref`, following the service schema; declare
core-owned fields such as `host`, `project`, `language`, and `uses` in
`azure.yaml`. Deploy by **service name**, not by a definition-file path. A
sibling `toolbox.yaml` is not automatically deployed: declare a Toolbox service
and add it to `uses`. Deploy dependencies first or use `azd deploy --all`; a
targeted Agent deployment does not deploy its dependencies automatically.

### Endpoint command support

Endpoint behavior depends on the agent `kind` declared inline or through `$ref`:

| Agent kind | `endpoint show` | `endpoint update` | Deployment endpoint reporting |
| --- | --- | --- | --- |
| `hosted` | Shows the live endpoint/card configuration | Updates endpoint/card configuration | Reports configured protocol URLs |
| `prompt` | Shows the deployed Responses endpoint | Not supported | Reports the deployed Responses endpoint |
| `voice`, `prompt-voice` | Shows the deployed voice WebSocket endpoint | Not supported | Reports the deployed voice WebSocket endpoint |
| `workflow` | Not supported | Not supported | Not supported |

For JSON output, `endpoint show` includes `name` and `kind`. Hosted agents retain
the `agent_endpoint` and `agent_card` fields; prompt and voice agents return their
callable URLs in `endpoints`.

The command validates the agent definition before reading deployment state.
Missing, unknown, or malformed kinds return a structured validation error rather
than falling back to hosted-agent behavior. Hosted Activity and session
capabilities do not change the agent kind and follow the hosted behavior above.

## Initializing an A2A hosted agent

Enable `AZD_ALPHA_ENABLE_AGENTS_A2A=true` in your shell or CI before initializing
an A2A hosted agent. With the preview off, init hides the `a2a` choice and rejects
explicit `--protocol a2a` selections (including repeated or comma-separated flags)
before authentication or writes. Responses remains the no-prompt default.
`invocations_ws` independently requires the Voice preview. Existing declarations
are not gated during deployment or lifecycle operations by this init selection check.

Foundry forwards requests directly to A2A hosted agents instead of converting them
to Responses requests. Select `a2a` during init and deselect `responses` if your
agent does not support it. For non-interactive initialization:

```bash
azd ai agent init --src . --agent-name my-a2a-agent --protocol a2a \
  --deploy-mode code --runtime python_3_13 --entry-point app.py \
  --dep-resolution remote_build --no-prompt
```

Selecting only `a2a` generates:

<!-- azd:doc-example partial -->
```yaml
services:
  my-a2a-agent:
    host: azure.ai.agent
    kind: hosted
    name: my-a2a-agent
    protocols:
      - protocol: a2a
        version: 1.0.0
    agentEndpoint:
      protocols: [a2a]
```

`protocols` declares the agent's implementation; `agentEndpoint.protocols` enables
its A2A endpoint. Foundry uses declaration `1.0.0`; public A2A requests use `1.0`.

Your agent must serve:

- `POST /a2a` and `POST /invocations/a2a` (hosted routing varies by runtime)
- `GET /invocations/a2a/.well-known/agent-card.json`
- `GET /readiness`

Init writes configuration, not an A2A server. Service onboarding and region support
are required separately.

## Invoking agents over A2A

Without the `agents.a2a` preview, messages and files use A2A 0.3. File bytes
are sent unchanged; omit `--a2a-version`, which requires the preview:

```bash
azd ai agent invoke -p a2a "Hello!"
azd ai agent invoke -p a2a -f a2a-request-0.3.json
```

Enable `AZD_ALPHA_ENABLE_AGENTS_A2A=true` in your shell or CI for the
version-selection behavior below. This changes the message default to 1.0
and requires an explicit supported version for every A2A file invocation.

Use `-p a2a` (`--protocol a2a`) with `--a2a-version` to invoke a deployed
agent using public A2A version `1.0` or `0.3` through its Foundry A2A endpoint:

```bash
# Explicitly select the public A2A request version
azd ai agent invoke -p a2a --a2a-version 1.0 "Hello!"
azd ai agent invoke -p a2a --a2a-version 0.3 "Hello!"
```

These are public A2A protocol versions, not Foundry API versions or deployed
agent versions. See the pinned [A2A 1.0 versioning specification](https://a2a-protocol.org/v1.0.0/specification/#36-versioning)
and [A2A 0.3 specification](https://a2a-protocol.org/v0.3.0/specification/).

For plain messages, azd composes both the version-specific JSON-RPC body and
the `A2A-Version` header. Because azd controls both, `1.0` is a convenience
default when `--a2a-version` is omitted. A deployed hosted A2A implementation must
support `1.0` (`1.0.0` in deployment YAML) and match the requested wire version,
regardless of other protocols. Local working-tree definitions are not authoritative
for the deployed target, even when `--version` selects the current deployment.
Local inspection failures and compatibility findings are advisory diagnostics
shown with `--debug`; Foundry enforces deployed target support. Request and
file-argument validation remain blocking. Translation availability and restrictions
can vary by service rollout.

```bash
azd ai agent invoke -p a2a "Hello!"
```

With the preview disabled, plain messages use A2A 0.3. To preserve that behavior after enabling it, use
`azd ai agent invoke -p a2a --a2a-version 0.3 "Hello!"`.

Generated 0.3 requests leave execution and output modes unspecified, preserving the
non-blocking default. A Task may still be in progress; azd prints the initial response
without polling. A2A 1.0 uses its [synchronous default](https://a2a-protocol.org/v1.0.0/specification/#322-sendmessageconfiguration),
waiting for a terminal state or an input/authentication request.

**File input requires an explicit version.** When using `--input-file` (`-f`)
with A2A, always pass `--a2a-version 0.3` or `--a2a-version 1.0`, even when
A2A hosted agent YAML declares a version. No heuristic infers the version
from agent configuration or the request body.

The file must contain a complete public JSON-RPC request for the selected A2A
version, not a version envelope. The CLI sends the file bytes unchanged; it
does not wrap, rewrite, or convert the body. The caller must match the body
to `--a2a-version`, which selects the `A2A-Version` header.

Formatted output supports non-streaming JSON-RPC responses. For streaming methods
(`message/stream` or `SendStreamingMessage`), use `--output raw` to stream the SSE
response without parsing it.

For example, save this complete A2A 1.0 request as `a2a-request-1.0.json`:

```json
{
  "jsonrpc": "2.0",
  "id": "hello-1",
  "method": "SendMessage",
  "params": {
    "message": {
      "messageId": "hello-message-1",
      "role": "ROLE_USER",
      "parts": [{"text": "Hello!"}]
    },
    "configuration": {
      "acceptedOutputModes": ["text/plain"],
      "returnImmediately": false
    }
  }
}
```

```bash
azd ai agent invoke -p a2a --a2a-version 1.0 --input-file a2a-request-1.0.json
```

For A2A 0.3, save this request as `a2a-request-0.3.json` instead:

```json
{
  "jsonrpc": "2.0",
  "id": "hello-1",
  "method": "message/send",
  "params": {
    "message": {
      "kind": "message",
      "messageId": "hello-message-1",
      "role": "user",
      "parts": [{"kind": "text", "text": "Hello!"}]
    }
  }
}
```

```bash
azd ai agent invoke -p a2a --a2a-version 0.3 --input-file a2a-request-0.3.json
```

`--a2a-version` is valid only for A2A; do not use it with Responses or
Invocations. It is separate from `--version`, which continues to select a
**deployed agent version**, not an A2A protocol version. This CLI does not
support A2A invocation with `--local`; this is a CLI limitation, not a
restriction of the A2A protocol.

For completed Tasks in **both A2A 0.3 and 1.0**, default output prefers answer
artifacts over generic status messages such as “Done.” Other Tasks prefer status
messages so prompts, progress, and failure details remain visible, falling back to
artifacts when no status text is present. History is the final text fallback.
Use `--output raw` to inspect the complete HTTP response and JSON-RPC envelope.

A2A hosted-agent session affinity is service-controlled, even with a session or
context ID. In-memory task stores may return `TaskNotFound` on later requests.

## Invoke latency diagnostics

Remote Hosted Agent `azd ai agent invoke` calls using Responses or Invocations
request platform latency diagnostics by default. Successful calls show a compact
summary after the client timing line, for example:

```text
Client elapsed: 9.172s
Platform latency (cold): response headers 8859 ms
  preprocess 178 ms | infra 1439 ms | readiness 4493 ms | container 2749 ms
```

Use `azd ai agent invoke --debug-latency=false "Hello"` to disable collection and
the summary. The setting is independent of the global `--debug` logging flag.
`--output raw` includes the returned HTTP headers without adding a formatted
summary. Local, prompt-agent, and A2A invokes do not request platform diagnostics.
Explicitly enabling diagnostics with `--debug-latency` or `--debug-latency=true`
on these routes is rejected after route resolution. Omit the flag or use
`--debug-latency=false` to invoke them without platform diagnostics.

`Client elapsed` measures the client-observed invocation duration, including
response reading. It replaces the previous `Server responded in ... (first byte: ...)`
line and remains available when platform diagnostics are disabled or unavailable.
Neither timing includes CLI startup, token acquisition, or separate
conversation/session creation. The platform values describe the original
invocation up to response headers. Response headers are not
the first response body byte or the first model token. Container response time
also includes request forwarding, connections, retries, and policy buffering; it
is not a model-only inference measurement.

Warm requests omit infrastructure setup and container readiness instead of
reporting zero. Missing fields are not synthesized. Background Responses
(`--long-running`, including `--no-wait`) and `202` Invocations show platform
overhead only. With `--no-wait`, the summary describes request setup, not completion
of background work. Existing invocation polling can pick up the original POST's
persisted metrics, without an additional request. Unavailable or invalid diagnostics
do not turn a successful agent call into an error. This summary does not wait for
trailers or change SSE termination.

## Running Local Agents

`azd ai agent run` starts the selected agent locally and, by default, opens the
Agent Inspector after the local agent port is accepting connections. The
inspector launch is best-effort: if `azure.ai.inspector` is not installed or
fails to start, the agent process keeps running and azd prints install guidance.

Use `--no-inspector` to run only the local agent process:

```bash
azd ai agent run --no-inspector
```

## Publishing a Digital Worker

An Activity-protocol hosted agent can be published as a Microsoft 365 Digital
Worker. Declare the Digital Worker settings on the `azure.ai.agent` service in
`azure.yaml`, deploy the agent, and then publish it:

```yaml
services:
  my-digital-worker:
    host: azure.ai.agent
    project: src/my-digital-worker
    language: python
    kind: hosted
    name: my-digital-worker
    protocols:
      - protocol: activity
        version: 2.0.0
    activity:
      digitalWorkerType: m365
      publish:
        publishScope: tenant
        agentDisplayName: My Digital Worker
        optionalPermissionScopes:
          - resourceAppId: ea9ffc3e-8a23-4a7d-836d-234d7c7565c1
            scopes:
              - McpServers.Mail.All
              - McpServers.Calendar.All
        accessBoundaries:
          - read.1on1.developers
          - write.1on1.developers
```

The `activity.publish` block is shared Microsoft 365 app publish metadata for
Activity agents. When `digitalWorkerType` is `m365`, azd enforces tenant scope
and sends `digital_worker_type: m365` when creating the agent. Omit
`digitalWorkerType` for simple mode.
After deployment, the service-returned Digital Worker type controls pack and
publish behavior. The publish request sets `publishAsAutopilot` automatically;
the publish block itself is optional.

`optionalPermissionScopes` selects additional Microsoft 365 permissions such as
WorkIQ MCP scopes. `accessBoundaries` accepts the supported
`read.1on1.developers`, `write.1on1.developers`,
`read.group.developers`, and `write.group.developers` values. Omitting
`accessBoundaries` preserves the current service configuration; an explicit
empty array clears it.

```bash
azd deploy
azd ai agent publish
```

For simple Activity agents, `publishScope` accepts `shared` or `tenant`. For an
`m365` Digital Worker, `publishScope` is always `tenant`.
An explicit `azd ai agent publish --scope <scope>` overrides the configured
value where allowed by the use case. Use `--display-name` and `--app-version`
to override the corresponding configured publish metadata for one command
invocation. Repeat `--optional-permission-scope <resource-app-id>=<scope>` or
`--access-boundary <boundary>` to replace the configured values for one
publication. Use `--clear-access-boundaries` to send an explicit empty array and
clear existing boundaries.

The Agent Inspector UI binds port `8087` by default. Use `--inspector-port` to
move it, which is what you need when running two agents side by side or when a
stale process still holds the default port:

```bash
azd ai agent run --port 9091 --inspector-port 9002
```

`--inspector-port` is rejected when it cannot be honored:

- with `--no-client` (or the deprecated `--no-inspector`), since no local client
  is opened and the port would go unused; and
- for agents that use Agent Inspector, when it matches `--port`, since the agent
  binds that address first and the inspector would then fail to bind it.

azd also warns, without failing the run, when `--inspector-port` cannot take
effect: activity-protocol agents open the Microsoft 365 Agents Playground rather
than the Agent Inspector, and `--port 8087` on its own collides with the
inspector's own default UI port.

### Local client route telemetry

When installed from the official registry, the extension reports the
`local_client.route.selected` usage event after `azd ai agent run` resolves the
service and protocol profile. Its `ext.route` attribute is exactly one of:

- `inspector` for a non-activity agent;
- `playground` for an activity-protocol agent; or
- `suppressed` when `--no-client` or the deprecated `--no-inspector` is set.

The event is emitted before checking client availability, starting the local
agent, or launching a client. It records route selection, not successful client
launch.

## Initializing a Project

When `azd ai agent init` scaffolds into a subdirectory, the `Next:` block starts
with `cd <folder>` before the provisioning command, even if that folder already
existed. Run the `cd` command first: init does not change your shell's working
directory. Initializing in the current directory does not need a `cd` hint.

For paths requiring shell-specific quoting, the block labels alternatives for
POSIX shells, PowerShell, and cmd.exe. Run only the navigation command for your
shell, then continue with the remaining steps. If cmd.exe cannot safely express
the path (for example, it contains `%` or `!`), use the PowerShell alternative.
Drive-qualified paths also show these alternatives so cmd.exe switches drives.
PowerShell alternatives preserve both straight and smart apostrophes literally.
Paths containing smart double quotes also use the shell-specific alternatives.
The same applies to paths starting with a Unicode dash that PowerShell treats
as a parameter prefix.

## Migrating Legacy Agent Configuration

New Foundry agent projects keep the agent definition directly on the
`azure.ai.agent` service entry in `azure.yaml`. Older projects may still have the
definition in an `agent.yaml`/`agent.yml` file, an AgentManifest file, or under
the service's `config:` block. Runtime commands reject those implicit and nested
sources with migration guidance.

For an existing azd project, `azd ai agent add <name> --file <path>`
validates a direct definition and adds or updates only that Agent service.
The positional service key is not the Foundry Agent identity in the definition.
The command does not adopt another project, authenticate to Azure, create
resources, copy source files, or deploy. For full sample or project adoption,
use the `azd ai agent init --template <path>` workflow.

The definition may be YAML or JSON and must use a supported direct Agent kind:
`prompt`, `hosted` (including code deployment), `voice`, or the `prompt-voice`
compatibility name. The service stores a root `$ref` relative to the directory
containing `azure.yaml`; nested references resolve relative to the file that
declares them. The project must declare exactly one `azure.ai.project` service
before the command writes. `--project` can confirm that service key, and
`--source` overrides the Agent service's project directory with an existing
directory inside the current project.

When adding or updating a service, the command rejects numeric scalar tokens
that cannot survive the project API's double-precision representation without
changing their value. Quote YAML dates and timestamps, such as
`apiVersion: "2025-01-01"`,
to keep them as strings. Scalar token checks cover the entire project document,
including top-level extension fields and dependency service properties, because
the project API saves the whole file. Validation failures and unchanged
declarations leave the project tree untouched; the shared Agent authoring lock
lives in the user cache.

Move a direct agent definition to service-level properties in `azure.yaml`:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    kind: hosted
    name: my-agent
    description: My hosted agent
```

Alternatively, keep a direct definition in a separate file and reference it
explicitly from the service with a root `$ref`, for example
`$ref: ./definitions/agent.json`. Prompt, hosted (including code deploy), voice,
and prompt-voice agents support YAML or JSON definitions. The basename can be
anything, including a legacy-looking name such as `agent.yaml`; prompt-agent
references accept `.yaml`, `.yml`, and `.json` extensions. The file content must
be a supported direct agent definition. An `agent.manifest.yaml` template wrapper
must first be converted or extracted.

Prompt-agent root references must stay within the project directory and resolve
relative to the directory holding `azure.yaml`, not the service's `project`
directory. Prompt `skills/` and `vector-assets/` convention folders remain next
to the referenced definition. Nested `$ref` includes and relative instructions
paths inside a definition resolve relative to that file. Hosted code packaging
still uses the service's `project` directory. Keep core service fields such as
`project`, `language`, `image`, `docker`, and `env` in `azure.yaml`, not in the
root reference target.

### Environment variables under `config:`

Older projects could also set environment variables in an `env:` block nested
under the service's `config:`. That position is no longer read: azd takes the
service environment only from the service-level `env:`. Runtime commands fail
when an agent service still carries a non-empty `config:` block. Move those
environment values to the service-level `env:` before running the agent.

Move them up one level to fix it:

<!-- azd:doc-example partial -->
```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    env:
      API_KEY: ${SECRET}
      LOG_LEVEL: debug
```

Hosted Agent Service environment variable names must start with a letter or
underscore and contain only letters, digits, or underscores. For example,
`API_KEY` is valid, while `api-key` is not. `azd deploy` validates these names
before contacting Foundry Agent Service.

## Prompt agent memory

Prompt agents can declare one memory store for azd to provision and attach
through a memory-search tool. Prompt memory properties use camelCase:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    kind: prompt
    name: my-agent
    model: gpt-5-mini
    instructions: Remember useful details from earlier conversations.
    memory:
      store: conversation-memory
      chatModel: gpt-5-mini
      embeddingModel: text-embedding-3-small
      scope: "{{$userId}}"
      updateDelay: 300
      maxMemories: 5
      options:
        chatSummaryEnabled: true
        userProfileEnabled: true
        proceduralMemoryEnabled: false
        defaultTtlSeconds: 2592000
        userProfileDetails: Remember stable preferences.
```

## Content safety policies

A hosted or prompt agent can be bound to an Azure AI Content Safety (RAI) policy so every
request and response it handles is screened by that policy. Declare it with a
`policies` list on the `azure.ai.agent` service entry in `azure.yaml`:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    kind: hosted
    name: my-agent
    policies:
      - type: rai_policy
        raiPolicyName: /subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account-name>/raiPolicies/<policy-name>
```

For prompt agents, use the same `policies` entry with `kind: prompt`:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    kind: prompt
    model: gpt-4.1-mini
    instructions: You are a helpful assistant.
    policies:
      - type: rai_policy
        raiPolicyName: /subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account-name>/raiPolicies/<policy-name>
```

`policies` is optional. For hosted agents, it applies to both deploy modes —
container images and code deploys (`codeConfiguration`) alike.

Details:

- `type` is required. `rai_policy` is currently the only supported value.
- `raiPolicyName` is required for `rai_policy` and takes the **full ARM resource
  ID** of the policy, not its short name. Built-in policies such as
  `Microsoft.DefaultV2` still need the full ID, with the account that hosts them
  in the path.
- Create or list policies on the Foundry account first — azd does not create the
  policy, it only associates the agent with an existing one. For prompt and
  managed agents, `azd ai agent init` lists the policies on the selected account
  and can bind one for you; see `--rai-policy`.

## Voice agents (public preview)

Voice initialization needs the `agents.voice` [opt-in](#preview-feature-opt-ins).
Use the existing commands to discover options and create a managed prompt voice
agent:

```bash
azd ai agent --help
azd ai agent init --help
azd ai agent init --kind prompt-voice --agent-name support-voice --model gpt-realtime
```

Run the deployment commands from the generated project directory containing
`azure.yaml`:

```bash
azd provision
azd deploy
```

### Managed prompt voice

The following `azure.yaml` example uses a service-hosted model; no separate model
deployment is required. `kind: voice` and the compatibility alias
`kind: prompt-voice` are both accepted. Initialization retains its existing
`prompt-voice` output; changing the kind is not required to use public preview.

```yaml
name: support-voice
services:
  ai-project:
    host: azure.ai.project
  support-voice:
    host: azure.ai.agent
    kind: voice
    name: support-voice
    uses:
      - ai-project
    modelType: managed
    model:
      id: gpt-realtime
    instructions: You are a concise customer support assistant.
    voice: en-US-AvaNeural
infra:
  provider: microsoft.foundry
```

### Bring your own model deployment (BYOM)

For an existing compatible deployment in your Foundry resource, use
`modelType: self_deployed` and set `model.id` to its **deployment name**, not
necessarily its model name. Replace the endpoint and deployment name below with
your own values. The referenced deployment must exist before agent deployment;
setting these fields does not create a model deployment.

```yaml
name: support-voice-byom
services:
  ai-project:
    host: azure.ai.project
    endpoint: https://example.services.ai.azure.com/api/projects/example-project
  support-voice-byom:
    host: azure.ai.agent
    kind: voice
    name: support-voice-byom
    uses:
      - ai-project
    modelType: self_deployed
    model:
      id: my-realtime-deployment
    instructions: You are a concise customer support assistant.
    voice: en-US-AvaNeural
infra:
  provider: microsoft.foundry
```

Edit existing voice settings in `azure.yaml`; `--voice` applies when initializing
a new prompt voice agent and is rejected when the chosen init flow cannot use it.
Advanced settings such as `audio`, `structuredInputs`,
`tools`, `greeting`, `avatar`, and `handoff` are described by the
[agent service schema](schemas/azure.ai.agent.json).
See [hosted voice wrappers](#hosted-voice-wrapper-preview) for `conversationEngine`
and [telephony bindings](#prompt-voice-telephony-bindings) for phone-provider setup.

To try a prompt voice agent or hosted voice wrapper, open your project in the
[Microsoft Foundry portal](https://ai.azure.com). `azd ai agent invoke` does not
implement voice conversations; recognized voice services receive portal guidance.
Programmatic clients use the voice WebSocket endpoint with a Voice Live client.
Use the project-level `azd deploy` workflow above for voice services.

## Prompt voice telephony bindings

Prompt voice agents can declare Foundry-side telephony bindings in `azure.yaml`.
This lets `azd deploy` bind an existing phone-provider route to the deployed
agent. Telephony is only supported for voice services. Use `kind: voice` for new
managed voice agents; `kind: prompt-voice` remains a compatibility alias.

```yaml
services:
  support-voice:
    host: azure.ai.agent
    kind: voice
    name: support-voice
    model:
      id: gpt-realtime
    telephony:
      bindings:
        - provider: twilio
          identifier: "+14255550123"
          connection: telephony-twilio
        - provider: acs
          identifier: "28:orgid:00000000-0000-0000-0000-000000000001"
          connection: telephony-acs
```

Prerequisites:

- The phone provider account/resource and phone number already exist.
- The Foundry project connection named by `connection` already exists.
- Provider-side callbacks, such as Twilio webhooks or ACS Event Subscriptions,
  are configured by the user/admin.

Supported providers and identifiers:

- `twilio`: use a Twilio phone number in E.164 format, such as `+14255550123`.
- `acs`: use `28:orgid:<guid>` for Teams Phone Extensibility Resource Accounts
  (the YAML provider name remains `acs` for compatibility). azd maps this to the
  canonical `teams_phone_extensibility` provider and sends the GUID as
  `resource_account_object_id`. Legacy `4:+<E.164>` ACS-purchased-number IDs are
  rejected during authoring validation because the canonical GA contract cannot deploy them.

Deploy lists every page of canonical bindings and matches provider identities
without deriving binding IDs. If exactly one active remote binding matches the
YAML, deploy continues. Missing bindings are created with repeatability headers.
Drift or duplicate matches fail safely instead of changing or deleting remote
routing. Existing per-binding `transferTargets` YAML remains migration-compatible:
omitting it leaves the singleton unmanaged, while an explicit empty list manages
an empty singleton. Multiple declarations must describe the same named targets
(order does not matter). azd validates supported `pstn`, `teams`, and `sip`
destinations locally. After binding reconciliation it validates the singleton;
it replaces a drifted singleton only when this deployment created a binding, and
uses the current ETag. Otherwise drift, unknown remote destination kinds, and
unconfirmed writes fail safely with manual-reconciliation guidance.

Cleanup: delete telephony bindings before deleting test agents. The service may
leave bindings behind when an agent is deleted, so do not rely on agent deletion
as binding cleanup.

List bindings to obtain the service-generated opaque ID and ETag, then delete the
binding before deleting the agent:

```bash
curl \
  -H "Authorization: Bearer $TOKEN" \
  "$PROJECT_ENDPOINT/agents/$AGENT_NAME/telephony/bindings?api-version=v1"

curl -X DELETE \
  -H "Authorization: Bearer $TOKEN" \
  -H "If-Match: $BINDING_ETAG" \
  "$PROJECT_ENDPOINT/agents/$AGENT_NAME/telephony/bindings/$BINDING_ID?api-version=v1"
```

Treat `BINDING_ID` as opaque: URL-escape the value returned by the list API and
do not construct it from the provider or phone number. Use the item `etag` from
the list response (or the `ETag` header from binding GET) as `BINDING_ETAG`.

### Moderating invocations-protocol traffic

For hosted agents that expose the `invocations` protocol, the RAI policy alone is not
enough: the content-safety proxy needs to be told **where the text lives** in the
request and response bodies. Without that it has nothing to submit to the policy,
so no content is actually screened. Supply an `invocationsModeration` block on the
`rai_policy` entry:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    kind: hosted
    name: my-agent
    protocols:
      - protocol: invocations
        version: "1.0.0"
    policies:
      - type: rai_policy
        raiPolicyName: /subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account-name>/raiPolicies/<policy-name>
        invocationsModeration:
          responseMode: both
          inputContentType: json
          outputContentType: json
          inputPaths:
            - $.input
          outputPaths:
            - $.output
          streamSelectors:
            - eventType: response.output_text.delta
              textField: delta
```

Fields:

| Field | Required | Description |
| --- | --- | --- |
| `responseMode` | yes | `non_streaming`, `streaming`, or `both`. |
| `inputContentType` | no | `json` (default) or `text`. |
| `outputContentType` | no | `json` (default) or `text`. |
| `inputPaths` | when `inputContentType` is `json` or omitted (it defaults to `json`) | Selector expressions locating the request text. |
| `outputPaths` | when `responseMode` includes non-streaming and `outputContentType` is `json` or omitted (it defaults to `json`) | Selector expressions locating the buffered response text. |
| `streamSelectors` | when `responseMode` includes streaming and `outputContentType` is `json` or omitted (it defaults to `json`) | `eventType` (required) and `textField` per server-sent event frame. See [Selectors and field names](#selectors-and-field-names). |

#### Selectors and field names

`inputPaths` and `outputPaths` are **selector expressions**. They support `$` for
the document root, dotted members, array indexes, and `[*]` wildcards — for
example `$.messages[*].content`. They are not a full JSONPath implementation.

`textField` is **not** a selector: when provided, it is the non-empty, exact
**name of a field** on the matched event payload, with no surrounding whitespace.
Write `delta`, not `$.delta`, `""`, or `" delta"`. It defaults to `delta` when
omitted.

`eventType` is matched exactly, with no surrounding whitespace, against the value
of the `type` field *inside* the event's `data:` payload, not against the SSE
`event:` line. So for a frame like

```text
data: {"type": "response.output_text.delta", "delta": "Hi"}
```

the selector is `eventType: response.output_text.delta` with `textField: delta`.

> **Why this matters:** a `textField` that names no field on the payload yields no
> text, so that event contributes nothing to moderation. A `$.`-prefixed or
> whitespace-padded value therefore silently disables screening for every frame it
> applies to. azd rejects those values for this reason.

`invocationsModeration` is only valid on a `hosted` agent whose `protocols` list
includes `invocations`. Declaring it elsewhere — on another agent kind, or on an
`invocations_ws`-only agent, which does not go through the content-safety HTTP
proxy — fails validation rather than silently deploying a policy that never runs.

> **Understanding `responseMode`:** it declares which response *shapes* the
> container can produce, **not** "input and output". Input is always moderated.
> For the output side the proxy inspects the actual response `Content-Type` and
> runs exactly one gate: the SSE gate for `text/event-stream`, the buffered gate
> otherwise. Use `both` only for containers that genuinely answer both ways —
> if a response arrives in a shape `responseMode` did not declare, the request
> fails closed rather than skipping moderation.

Set `inputContentType`/`outputContentType` to `text` when the body is plain text;
the whole body is then moderated and no paths are needed for that direction.

### Hosted voice wrapper (preview)

A hosted voice wrapper keeps Voice Live responsible for VAD, speech-to-text,
and text-to-speech while routing conversation logic to a hosted agent in the
same Foundry project. Hosted Voice samples use the same sample `azure.yaml`
flow as other current Hosted Agent and `invocations_ws` samples:

```powershell
azd ai agent init -t .\path\to\azure.yaml
```

The local path can be replaced with its public GitHub URL after the sample is
published.

When the sample project is already present with its `azure.yaml`, run
`azd ai agent init` from the project directory to reuse the existing azd
configuration before provisioning and deployment.

The sample `azure.yaml` contains both services and references the target by its
service name:

```yaml
services:
  ai-project:
    host: azure.ai.project

  voice-target:
    host: azure.ai.agent
    project: ./src/voice-target
    language: csharp
    kind: hosted
    name: voice-target
    uses:
      - ai-project
    protocols:
      - protocol: invocations_ws
        version: 1.0.0
    metadata:
      voiceLiveCompatible: "true"
      bridgeProtocolVersion: "1.0"
    container:
      resources:
        cpu: "1"
        memory: 2Gi
    codeConfiguration:
      runtime: dotnet_10
      entryPoint: VoiceHostedAgent.dll
      dependencyResolution: bundled

  voice-target-voice:
    host: azure.ai.agent
    kind: voice
    name: voice-target-voice
    uses:
      - ai-project
      - voice-target
    conversationEngine:
      type: hosted_agent
      name: voice-target
      version: deployed
    store: false
```

The `uses` edge deploys the target before the wrapper. `conversationEngine`
points at the hosted target service, and `version: deployed` pins the wrapper
to the target version produced by the current azd environment.

The target must be active, declare `invocations_ws/1.0.0`, and include
`voiceLiveCompatible=true` and `bridgeProtocolVersion=1.0` metadata. Model,
instructions, tools, and other conversation controls belong to the target;
the wrapper owns audio, voice, store, avatar, and greeting configuration.

## Invocations metadata discovery

For project-backed remote `azd ai agent invoke --protocol invocations` calls,
azd optionally caches `/invocations/docs/openapi.json` **after** the invocation
response has been handled successfully. For SSE and long-running invocations,
discovery follows stream completion or terminal success, respectively. The GET
uses the invocation's effective `agent_session_id`, including a server-assigned
ID from a fresh invocation. If no session ID is available, azd skips discovery
rather than sending a request without one. Explicit `--agent-endpoint` calls
do not use project metadata discovery.

Metadata failures do not replace a completed invocation's result.
Remote metadata redirects are not followed: 3xx responses are optional discovery
failures, so redirects cannot forward caller headers or drop the session binding.
Failure categories appear only with `--debug`. Interactive, non-raw invocations
show `Checking optional OpenAPI metadata...` during discovery; timing and `Next:`
guidance follow discovery. Redirected and raw output contain no metadata progress.
Raw response handling remains unchanged, including its
existing treatment of agent-error bodies.

Remote discovery has separate 10-second bounds for configuration RPCs and the
HTTP request; those waits can total up to 20 seconds after the agent's response.
An earlier caller deadline or cancellation takes precedence. Synchronous
filesystem operations are not covered by those network/RPC bounds. Invocation
timing excludes discovery.

Successful metadata is cached as `.azure/<env>/openapi-<agent>-remote.json`.
A cache hit avoids another metadata request. Missing or failed metadata is not
cached, so a 404 can be probed again on a later invocation. This does not remove
sessions created by earlier azd versions.

Local discovery keeps its existing behavior: a GET without a session ID before the POST,
refreshed on every call, with no new identity or custom headers on the GET.
The local POST still carries its own session and configured headers.

## Session idle timeout

A hosted agent's runtime session sandbox is suspended by Foundry after a period
of inactivity. The default is 900 seconds. Override it with
`sessionConfiguration.idleTimeoutSeconds` on the `azure.ai.agent` service entry
in `azure.yaml`:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    kind: hosted
    name: my-agent
    sessionConfiguration:
      idleTimeoutSeconds: 300
```

`sessionConfiguration` applies to both deploy modes — container images and code
deploys (`codeConfiguration`) alike. It is optional; when omitted, the setting
is left out of the service request and Foundry applies its default (900
seconds).

Details:

- `idleTimeoutSeconds` must be between **120 and 14400** seconds (**2–240 minutes**, inclusive).
  Values outside that range are rejected at deploy time and by schema
  validation.

## State Stores

Use `azd ai agent state-stores` to inspect existing Foundry State Stores and read,
replace, or delete their JSON object items. Select a store once, or supply `--store`
for a one-off item operation. Store creation, updates, and deletion are not included.

See [State Store commands and examples](docs/state-stores.md) for selection,
conditional writes with ETags, and pagination. Editing state does not resume or
stop agent work.

## Session carry-over across deploys

For A2A hosted agents, [session-affinity limitations](#invoking-agents-over-a2a) still apply.

When a hosted agent is redeployed, Foundry assigns the agent a **new version** and
sessions are bound to the version they were created on. By default, the first
`azd ai agent invoke` after a deploy starts a brand-new session on the new
version, dropping the previous session (including any state on its persistent
`/home/session` volume).

Set the `AZD_AGENT_RESUME_SESSION_ON_DEPLOY` environment variable to a truthy
value to opt in to **session carry-over**. When enabled, `azd deploy` captures
the current session before deploying, stops it, and re-points the newly deployed
version's session pointer at it, so the next `azd ai agent invoke` resumes the
same session on the new code with its `/home/session` volume intact.

```bash
# Enable session carry-over for this azd process
export AZD_AGENT_RESUME_SESSION_ON_DEPLOY=true
azd deploy
```

Details:

- **Accepted truthy values** (case-insensitive, surrounding whitespace ignored):
  `1`, `true`, `yes`, `on`. Any other value (or unset) leaves carry-over
  **disabled**, which is the default.
- Carry-over is always **best-effort** and never fails a deploy. If any step
  fails (for example, the previous session was already deleted), azd silently
  falls back to the default behavior and the next invoke starts a fresh session
  on the new version.

## Customize infrastructure

Use `azd ai agent init --infra` to generate editable Foundry Bicep or Terraform. Existing project infrastructure is preserved as a separate layer. See [Customize Foundry infrastructure with `--infra`](docs/infrastructure-eject.md) for migration behavior, file-conflict rules, resource-group ownership, layer dependencies, and limitations.

## Private container registry connections

A hosted agent can reference a pre-built image in a private registry through a
Foundry project connection. The image must be fully qualified, and
`docker.imagePassthrough: true` is required so azd preserves the remote image
reference instead of pulling, building, or publishing it.

The Foundry connection must use metadata understood by the hosted-agent service
and credentials that let the Foundry project identity authenticate to the
registry. For OAuth token exchange, configure the registry's identity provider,
audience, token endpoint, and project-identity binding before deploying.

### External connection

For a connection that already exists in the Foundry project, set
`registryConnectionId` to its Foundry name or resource ID. An external connection
does not belong in `uses`:

```yaml
services:
  existing-project:
    host: azure.ai.project
    endpoint: https://example.services.ai.azure.com/api/projects/example-project

  private-image-agent:
    host: azure.ai.agent
    uses:
      - existing-project
    kind: hosted
    name: private-image-agent
    image: registry.example.com/team/agent:v1
    docker:
      imagePassthrough: true
    registryConnectionId: production-registry
    protocols:
      - protocol: invocations
        version: 1.0.0
```

You can create the external connection before deployment with
`azd ai connection create`, or create it through the Foundry portal or API. The
connection must exist on the selected project before `azd deploy` runs.

### Declarative sibling connection

To let the Connections extension reconcile the connection during `azd deploy`,
declare an `azure.ai.connection` sibling and add its service key to the agent's
`uses` list. Set `registryConnectionId` to the actual Foundry connection name.
In this example, the sibling omits `name`, so its service key is also its Foundry
connection name. If the sibling declares a different `name`, use that name in
`registryConnectionId` while keeping the service key in `uses`:

```yaml
services:
  existing-project:
    host: azure.ai.project
    endpoint: https://example.services.ai.azure.com/api/projects/example-project

  private-registry:
    host: azure.ai.connection
    uses:
      - existing-project
    category: CustomKeys
    target: https://registry.example.com
    authType: CustomKeys
    credentials:
      keys:
        audience: ${REGISTRY_AUDIENCE}
        tokenEndpoint: /oauth/token
        body.provider_name: ${REGISTRY_PROVIDER}
    metadata:
      type: registry_connection
      mode: oauth_token_exchange

  private-image-agent:
    host: azure.ai.agent
    uses:
      - existing-project
      - private-registry
    kind: hosted
    name: private-image-agent
    image: registry.example.com/team/agent:v1
    docker:
      imagePassthrough: true
    registryConnectionId: private-registry
    protocols:
      - protocol: invocations
        version: 1.0.0
```

Set the referenced credential environment values, run `azd provision` for the
Project, then run `azd deploy --all` to deploy the connection before the agent.
Provisioning alone does not create the connection. Omitting the sibling from
`uses`, disabling it with a deployment
condition, or omitting image passthrough causes validation to fail before agent
deployment.

## Private networking for `host: azure.ai.project`

Foundry project services can be provisioned as network-secured, VNet-bound accounts by adding a `network:` block to the `host: azure.ai.project` service in `azure.yaml`. The `azure.ai.projects` extension owns that service and the `microsoft.foundry` provider; this extension still authors the block during agent init. See [Private networking for `host: azure.ai.project`](docs/private-networking.md) for the schema reference, BYO-image requirements, and VNet deployment cheatsheet.

## Local Development

### Prerequisites

1. **Install developer kit extension** (if not already installed):
   ```bash
   azd ext install microsoft.azd.extensions
   ```

   > **Note**: If you encounter an error about the extension not being in the registry, verify you have the default source configured:
   > ```bash
   > azd ext source list
   > ```
   > If missing, add it:
   > ```bash
   > azd ext source add -n azd -t url -l "https://aka.ms/azd/extensions/registry"
   > ```

### Building and Installing

1. **Navigate to the extension directory**:
   ```bash
   cd cli/azd/extensions/azure.ai.agents
   ```

2. **Initial setup** (first time only):
   ```bash
   azd x build
   azd x pack
   azd x publish
   ```

3. **Install the extension**:
   ```bash
   azd ext install azure.ai.agents
   ```

4. **For subsequent development** (after initial setup):
   ```bash
   azd x watch
   ```
   This automatically watches for file changes, rebuilds, and installs updates locally.

   Or for manual builds:
   ```bash
   azd x build
   ```
   This builds and automatically installs the updated extension.

> [!NOTE]
> The `pack` and `publish` steps are only required for the first time setup. For ongoing development, `azd x watch` or `azd x build` handles all updates automatically.
