# Release History

## Unreleased

- **Breaking:** `--kind managed` is no longer accepted, because "managed" was never a kind. A managed agent is a prompt agent that names an execution harness — both scaffold `kind: prompt`, and the harness is the only difference — so it is now spelled `--kind prompt --harness github_copilot_preview`. Passing the old value fails with that replacement rather than a bare "unknown value". The interactive picker is unchanged: choosing "Prompt agent with GitHub Copilot harness" still selects the harnessed flavor in one keystroke, it just no longer routes through a kind that nothing downstream understood.
- The prompt-agent preview notice now appears on every path into `azd ai agent init`, not only the interactive picker. `--kind prompt` and manifest adoption previously scaffolded a preview feature with no indication it was one.
- `azd ai agent init` now suggests a different default agent name for the harnessed flavor (`my-copilot-agent`) than for the plain one (`my-prompt-agent`). Accepting the default for both in the same folder previously produced one project overwriting the other, and — because the name is the Foundry agent identity — a second `azd up` silently versioned the first agent instead of creating a second one.

- Prompt and managed agents can now be bound to a Responsible AI policy during `azd ai agent init`, instead of the policy being something you hand-write into `agent.yaml` afterwards. Init lists the policies already on the selected Foundry account and lets you pick one; `--rai-policy` takes `none`, a policy name, or a full ARM resource ID for scripted runs. With `--no-prompt` and no flag, nothing is attached, and a `--manifest` that already declares `policies:` is never prompted over. `azd` attaches an existing policy — creating one stays with whoever owns the account, and the docs carry a worked `az` and Bicep example.
  - The scaffold writes `raiPolicyName: ${RAI_POLICY_ID}` rather than the resource ID itself, and records the concrete ID in the azd environment, so a project can be copied to another subscription and deployed unchanged. `azd deploy` now expands `${VAR}` references in `policies[].raiPolicyName`; an unresolved reference fails naming the variable instead of silently publishing an agent without the guardrails its manifest declares.
  - Before publishing, `azd deploy` verifies the policy exists on the target account and names the policy and the account when it does not — the create call reports a missing policy as a generic bad request that mentions neither. Verification is best-effort: a missing read permission produces a warning rather than blocking a deploy the service would have accepted.
  - A create rejected on an agent that declares a policy now says so, and distinguishes "the policy is wrong" from "this harness does not accept policies yet".

- **Breaking:** a `skills/` folder is now created by the `azure.ai.skills` extension rather than by this one. Creating and versioning a Foundry skill belongs to whoever owns `host: azure.ai.skill`, so `azd ai agent init` now writes one `azure.ai.skill` service per `skills/<name>/` folder — with `archive:` pointing at the folder so the scripts and references a skill needs travel with its instructions — and lists it in the agent's `uses:`. At deploy time this extension only *attaches* the version that service published, read from the `SKILL_<NAME>_VERSION` marker it records in the azd environment. A bundle with no such service now fails the deploy naming the `azure.yaml` entry to add, instead of azd quietly uploading a second copy of the skill under its own lifecycle. Existing projects: re-run `azd ai agent init` to have the entries written for you, then `azd deploy --all`.
- Prompt and managed agents now emit the same Foundry sibling services hosted agents already did. A `connections:` block in `agent.yaml` becomes one `azure.ai.connection` service per connection, and a `toolbox:` reference is added to the agent's `uses:` when a toolbox service of that name exists, so provisioning and deploy order are expressed in `azure.yaml` rather than implied.
- Deploy now prefers what the sibling services published over what it can infer on its own. A toolbox's MCP endpoint is taken from the `TOOLBOX_<NAME>_MCP_ENDPOINT` marker the `azure.ai.toolboxes` extension records, instead of being synthesized from the toolbox name, and a connection listed in `AZURE_AI_PROJECT_CONNECTION_NAMES` is used as-is instead of being re-created — the data-plane listing can lag a connection that was just provisioned. Both markers are checked against the project they were recorded for, so one left over from another Foundry project fails the deploy rather than pointing the agent somewhere it cannot reach. Toolboxes with no sibling service keep working through the previous lookup.
- `azure.ai.routine` is now recognized in a `uses:` list. A routine names the agent it dispatches, so the dependency runs routine → agent and there is nothing for the agent to wait on; previously the host fell through to the generic case and produced a misleading "provision the dependency first" suggestion.

- **Breaking:** agents that name a harness now reject fields and tool types the harness cannot honor, matching the Foundry GitHub Copilot harness spec. The service fails these at the API rather than ignoring them, so azd now catches them at deploy time and names the offending key:
  - `temperature`, `top_p`, `tool_choice` and `text` are rejected — the harness supplies its own sampling parameters and response format.
  - `reasoning` accepts only `effort`; any other property is rejected.
  - Tool types with no representation in the platform-managed toolbox a harness dispatches through are rejected: `function`, `azure_function`, `bing_grounding`, `capture_structured_outputs`, `image_generation`, `local_shell`, `shell`, `custom`, `computer`, `apply_patch`, `namespace` and `programmatic_tool_calling`.

  None of this narrows what a **harness-less** prompt agent accepts — every field and tool type above still works without `harness:`. The rejection lists are authoritative (taken from the spec), but a tool type absent from them is still passed through, so types newer than your azd build continue to deploy.
- `reminder_preview`, `toolbox_search` and `web_iq_preview` are now recognized tool types, so declaring one no longer produces a spurious "unrecognized tool type" warning.
- **Breaking:** `harness:` in `agent.yaml` is now a block rather than a bare string, matching the managed-agent API: `harness:` with a required `type`, plus optional `skills`, `environment` (`cpu`/`memory`/`idle_timeout_seconds`) and `builtin_tools` (`allowed`/`excluded`). `cpu` and `memory` must be set together, and `builtin_tools` entries are checked against the harness capabilities (`filesystem_read`, `filesystem_write`, `shell`, `subagents`, `web`) so a typo fails locally instead of silently widening what the agent can do. A string value is rejected with the replacement block in the error text.
- **Breaking:** the managed-agent harness type is now spelled `github_copilot_preview` in `agent.yaml` and on `--harness` (was `ghcp`). The old abbreviation is rejected with an error naming the replacement rather than being silently upgraded, so a manifest never disagrees with what is sent to the service. Update `harness: ghcp` to a `harness:` block with `type: github_copilot_preview`.
- The link from an `azure.yaml` service to its agent definition file is now explicit, using the same `$ref` file-include directive every other Foundry resource already uses: `$ref: ./agent.yaml` on the service entry. The referenced file's contents are merged onto the service entry, and a declared file that does not exist is a hard error rather than a silent fallback to the `agent.yaml`/`agent.yml` convention. `AGENT_DEFINITION_PATH` still wins over everything.
- `azd ai agent init` now writes that reference out instead of leaving it to convention: `$ref: ./agent.yaml` on the service entry in `azure.yaml`. Behavior is unchanged for projects that omit it — the convention still applies — but the scaffold now shows the `azure.yaml` → `agent.yaml` edge in the files themselves, so the file can be renamed by editing one line.
- Prompt (kind: prompt) agents now support a convention-over-configuration deploy pipeline. `azd up` resolves an internal dependency graph before publishing the agent and validates the whole graph first so a failure never leaves a half-wired agent:
  - A non-empty `vector-assets/` folder is uploaded to a vector store and wired into an auto-added `file_search` tool (content-hash dedupe; existing `file_search` tools are merged, not duplicated).
  - A non-empty `skills/` folder attaches the agent's skills, by one of two mechanisms depending on whether a harness is named. A **managed** agent (`harness:`) has each `SKILL.md` bundle published as a Foundry skill version and pinned onto the `harness` block by name and version, which is where the harness spec puts them: a skill is instructions plus the scripts they reference, so it needs the harness sandbox to run. Foundry provisions it into the harness's own service-owned toolbox — azd creates no toolbox, and a skill never becomes a tool. The version is always sent explicitly, because the service rejects a reference that omits it. A **plain** prompt agent has no sandbox, so its bundles are referenced by name on the definition's own `skills` field and made runnable by an injected `shell` tool. The separate `toolbox:` key is unrelated to skills — it attaches an *existing* shared toolbox as an `mcp` tool, and requires a harness, since a toolbox is only reachable from inside a harness sandbox; `toolbox:` on a harness-less agent is a validation error rather than deploying an agent whose tools never run.
  - Folder-authored skills are merged onto the `harness.skills` list alongside any entries authored there by hand, de-duplicated by name with the published (versioned) reference winning.
  - A `connections:` block resolves through a precedence ladder (use existing, create-if-missing with Entra default, auto-fill target from provisioning outputs, or provision/fail-fast), and each tool's required role is surfaced for assignment.
  - The model deployment is create-if-missing, and container-only fields (`image`, `protocols`, `code_configuration`, …) are rejected for prompt agents.
  - The manifest parser recognizes `skill` and `file` resource kinds.
- Prompt agents now support **memory** via a new `memory:` block in `agent.yaml`. `azd` creates the named Foundry memory store if it does not exist (reusing it if it does) and appends a `memory_search_preview` tool bound to it, since the prompt-agent API has no memory field of its own. `scope` defaults to `{{$userId}}` so a shared agent cannot surface one user's memories in another user's conversation. Available on managed agents (`harness: github_copilot_preview`) too; a switch (`harnessedPromptFeatures` in `internal/pkg/agents/agent_yaml/prompt_features.go`) can fail the deploy fast if a harness is ever confirmed to ignore a capability.
- Documented that the portal's **guardrails** and **knowledge** capabilities are already supported through existing keys — `policies:` (a `rai_policy` entry becomes the definition's `rai_config`) and the `vector-assets/` folder plus retrieval entries in `tools:` respectively. Neither is a field on the prompt-agent API, so no new keys were added.
- Prompt agents now support the `temperature:`, `top_p:`, `text:`, and `reasoning:` keys in `agent.yaml`, which previously had no binding and so could not be set at all. `temperature` and `top_p` are nullable, so an explicit `temperature: 0` is sent as `0` rather than collapsing into "unset" and picking up the service default. Together with the existing keys, all eleven fields the prompt-agent API's definition accepts are now reachable from `agent.yaml`.
- `tools:` entries are now validated. The service ignores a tool whose `type` it cannot identify **without reporting an error**, so a typo previously deployed "successfully" and produced an agent silently missing a capability its manifest claimed. Entries that are unambiguously malformed — not a mapping, no `type`, a non-string or blank `type`, or a type the API has removed (`memory_search`, replaced by `memory_search_preview`) — now fail validation before anything is provisioned, naming the offending index. A merely *unrecognized* type is reported as a warning and still deployed, since it may be newer than your azd build; hard-failing would make every new service tool type a breaking change.
- `azd deploy` now warns when it reuses an existing memory store whose live definition differs from what `agent.yaml` declares. Stores are create-if-missing and never updated, so editing `memory.chat_model` for a store that already exists silently had no effect. The warning names both the declared and actual values. It does not fail the deploy, because the store may be shared with another agent whose definition this manifest does not own.
- Deploy now records `AGENT_<SERVICE>_MEMORY_STORE_NAME` in the azd environment when a `memory:` block contributed a store, alongside the existing `AGENT_<SERVICE>_VECTOR_STORE_ID`.
- `azd ai agent init` now warns that prompt agents are a preview feature of the azd CLI experience when the plain (harness-less) prompt agent is selected.
- `azd ai agent init` now scaffolds the prompt-agent authoring layout: instructions written inline into `agent.yaml` plus empty `skills/` and `vector-assets/` folders so the deploy conventions are discoverable from a fresh init.
- `azd ai agent init` now carries `displayName:` and `metadata:` from a supplied prompt-agent manifest into the scaffolded `agent.yaml`, alongside the tools, skills, connections and policies it already copied. A hosted agent's template writes these catalog labels into `azure.yaml` and they reach the same fields on the agent-create request for a prompt agent, so a prompt agent scaffolded from a template no longer silently loses them.
- **Breaking:** prompt-agent authoring now matches hosted agents. There are no compatibility fallbacks — existing prompt agents must be updated:
  - `instructions` are declared inline in `agent.yaml` and are required. The instructions sidecar (`instructions.md`, later `AGENTS.md`) and the `instructions_file:` key are gone; the manifest now carries the same shape the prompt-agent API accepts, so what you author is what is sent.
  - The `version:` key has been removed from `agent.yaml`. It was written back after each deploy and ignored as an input, which made it look editable when it was not. The published version is still recorded in the azd environment as `AGENT_<SERVICE>_VERSION`.
  - The conventional vector-store folder is now `vector-assets/` (was `files/`), naming what it is for rather than what it contains.
  - Model deployments now live on a sibling `azure.ai.project` service that the agent service `uses:`, instead of under the agent service's `config.deployments`. `azd ai agent init` emits this shape, so a prompt agent's `azure.yaml` is now structurally identical to a hosted agent's. Projects that already have their deployments under the agent service and no `azure.ai.project` service continue to work.
- **Breaking:** `azd ai agent init` now writes a portable `azure.yaml` that contains no subscription, resource-group, workspace, or endpoint values, so an agent folder can be copied to another machine or subscription and deployed with `azd up` unchanged. Existing projects keep working — values already in the file still win — but newly generated files differ:
  - The `azure.ai.project` service key is always the generic `ai-project` (an existing key in the project is still reused). It was previously derived from the Foundry project name, which baked a tenant-specific identifier into the file.
  - The project's `endpoint:` is written as `${AZURE_AI_PROJECT_ENDPOINT}` and the concrete URL is stored in the azd environment. The reference is expanded before azd decides whether to reuse an existing Foundry project or create one, so setting the variable reuses a project and leaving it unset provisions a new one from the same `azure.yaml`.
  - The `config.promptAgent` block is now written entirely as environment references — `baseUrl: ${AZD_MANAGED_AGENT_BASE_URL}`, `subscriptionId: ${AZURE_SUBSCRIPTION_ID}`, `resourceGroup: ${AZURE_RESOURCE_GROUP}`, `workspace: ${AZURE_AI_WORKSPACE}`, and `projectEndpoint: ${AZURE_AI_PROJECT_ENDPOINT}` — instead of the resolved literals. The references are expanded against the azd environment at deploy time, and a reference whose variable is unset falls back to the built-in default, so a block that cannot be resolved no longer blocks deploy. `init` writes `AZURE_AI_WORKSPACE` into the azd environment alongside `AZURE_AI_PROJECT_ENDPOINT`. Blocks containing literal values keep working unchanged.
- `azd ai agent init` now offers a plain **prompt agent** alongside the harnessed one. Both scaffold `kind: prompt`; the difference is the new optional `harness` field in `agent.yaml`:
  - *Prompt agent (no code, Foundry-managed)* — omits `harness`. Foundry runs the model, instructions, and tools directly; there is no Brain+Hand sandbox to provision.
  - *Prompt agent with GitHub Copilot harness (preview)* — writes `harness: github_copilot_preview`, the previous behavior.
  Non-interactively, `--kind prompt` scaffolds the plain flavor and `--harness github_copilot_preview` adds the harness. Previously every prompt agent was published with a hard-coded harness, and the field was never written to the scaffolded `agent.yaml`.
- **Breaking:** a prompt agent that names a `harness:` may no longer declare `memory:` or any knowledge/grounding tool (`file_search`, `azure_ai_search`, `bing_grounding`, `sharepoint_grounding_preview`, and the other retrieval types, plus the `file_search` entry azd synthesizes from a `vector-assets/` folder). The harness spec documents RAI policy attachment but puts grounding out of scope and never describes memory, so these are now rejected at deploy time with a message naming the capability, instead of being published and silently dropped. `policies:` (guardrails) is unaffected, and a prompt agent without `harness:` still supports all three. Move an agent that needs memory or its own corpus off the harness by removing the `harness:` key.
- **Breaking:** the `agent.yaml` discriminator for prompt agents is now `kind: prompt` (was `kind: managed`). Existing `agent.yaml` files must be updated; the scaffolded schema annotation now points at `PromptAgent.yaml`. The `--kind managed` init flag value is still accepted, and now selects the GitHub Copilot-harnessed prompt agent.
- **Breaking:** removed `connections[].provision` from `agent.yaml`. The field was reserved but never implemented, and setting it always failed the deploy — a declaration carries only a name, auth type, and metadata, with no resource kind, SKU, or region to create anything from, and creating resources belongs to `azd provision` rather than `azd deploy`. A connection that matches no existing connection and has no resolvable `target` now fails with a single message telling you to provision the resource with infrastructure and set `connections[].target`. Remove the key from any manifest that sets it; nothing else changes.
- Fixed two gaps in memory store handling for prompt agents, caused by `agent.yaml`'s `memory:` block and `azure.yaml`'s `memoryStores:` list carrying independent copies of the same logic. An `options:` block whose fields were all unset was sent to the service as an empty object instead of being omitted, overriding the service defaults the author intended to keep; and drift against an existing store was only reported for `chat_model` and `embedding_model`, so a changed `options:` value was silently ignored. Both surfaces now share one request builder and one drift check, and the drift wording is consistent between them.
- Fixed a bug where only `SKILL.md` was uploaded when registering a skill under `skills/<name>/` — any other files in the bundle (e.g. `references/`, `assets/`, `scripts/`, at any nesting depth) were silently dropped. Skill registration now uploads the entire bundle via multipart upload instead of sending just the parsed `SKILL.md` body inline.
- Fixed a bug where a toolbox attached to a prompt agent (via a `skills/` folder or a `toolbox:` reference) was wired into the agent's `mcp` tool without a `project_connection_id`, leaving the agent with no credential to reach the toolbox MCP endpoint so its skills were never invoked. Deploy now creates (or updates) a `RemoteTool` project connection — via the Microsoft.CognitiveServices control plane, since the data-plane connections API is read-only — that fronts the toolbox endpoint and sets it as the tool's `project_connection_id`.
- Fixed a bug where `azd up` re-prompted for an Azure region for a prompt agent even after an existing Foundry project was selected during init. Selecting an existing project now seeds `AZURE_LOCATION` from the project's region (in addition to `AZURE_AI_DEPLOYMENTS_LOCATION`), so the model is deployed to the project's region without a redundant prompt.
- `azd ai agent show` now lists the toolbox tools attached to a prompt agent — each `mcp` tool's server URL and its backing `project_connection_id` — so the toolbox created during deploy is discoverable without inspecting the deployed definition. Also fixed the `Harness` field, which previously printed the harness API base URL instead of the actual execution harness (e.g. `GitHub Copilot (ghcp)`), and added a `Project Endpoint` row showing where the agent is served.

## 1.0.0-beta.12 (2026-08-24)

### Features Added

- [[#9596]](https://github.com/Azure/azure-dev/pull/9596) Add invocation moderation configuration for RAI policies used with the `invocations` protocol.
- [[#9610]](https://github.com/Azure/azure-dev/pull/9610) Add end-to-end Digital Worker deployment, packaging, and Microsoft 365 publishing workflows.

### Bugs Fixed

- [[#9596]](https://github.com/Azure/azure-dev/pull/9596) Reject agent manifests that declare multiple `rai_policy` entries instead of silently discarding additional policies.
- [[#9679]](https://github.com/Azure/azure-dev/pull/9679) Prompt users to provision after agent initialization adds a standalone Foundry connection service.

## 1.0.0-beta.11 (2026-08-20)

### Features Added

- [[#9444]](https://github.com/Azure/azure-dev/pull/9444) Add the `max_concurrent_agent_runs` optimize YAML option to run agent evaluations in parallel.
- [[#9472]](https://github.com/Azure/azure-dev/pull/9472) Add `initialization_parameters` to evaluator configuration in optimize YAML files.
- [[#9612]](https://github.com/Azure/azure-dev/pull/9612) Support `sessionConfiguration.idleTimeoutSeconds` for hosted agent services in `azure.yaml`.

### Bugs Fixed

- [[#9563]](https://github.com/Azure/azure-dev/pull/9563) Fix Doctor and next-step guidance for toolboxes declared as standalone `azure.ai.toolbox` services.
- [[#9600]](https://github.com/Azure/azure-dev/pull/9600) Report local agent process failures instead of reporting that the agent stopped successfully.
- [[#9636]](https://github.com/Azure/azure-dev/pull/9636) Preserve actionable structured errors returned through nested azd host calls.

## 1.0.0-beta.10 (2026-08-13)

### Features Added

- [[#9332]](https://github.com/Azure/azure-dev/pull/9332) Add `azd ai agent pack` and `azd ai agent publish` commands for packaging and publishing Teams activity agents. Thanks @v1212 for the contribution!
- [[#9457]](https://github.com/Azure/azure-dev/pull/9457) Allow `azd ai agent init --infra` to add isolated Foundry infrastructure alongside existing project infrastructure by using layers.

### Bugs Fixed

- [[#9517]](https://github.com/Azure/azure-dev/pull/9517) Fix `azd ai agent monitor <agent-name>` outside an azd project while preserving project-aware resolution when available.
- [[#9531]](https://github.com/Azure/azure-dev/pull/9531) Fix inconsistent Doctor and next-step environment diagnostics across inline `azure.yaml`, deprecated `config:`, and legacy agent manifests.
- [[#9543]](https://github.com/Azure/azure-dev/pull/9543) Preserve specific Activity Agent deployment failures for endpoint, Azure Bot, and Teams channel operations. Thanks @jayzhang for the contribution!
- [[#9497]](https://github.com/Azure/azure-dev/pull/9497) Fix Activity Agent deployments to reuse the Azure Bot already bound to the agent identity and persist the resolved bot name. Thanks @jayzhang for the contribution!
- [[#9491]](https://github.com/Azure/azure-dev/pull/9491) Preserve actionable hosted-agent deployment errors and remediation guidance.

### Other Changes

- [[#9370]](https://github.com/Azure/azure-dev/pull/9370) Update agent guidance to use the renamed `azd extension update` command. Thanks @hyoshis for the contribution!

## 1.0.0-beta.9 (2026-08-06)

### Features Added

- [[#9079]](https://github.com/Azure/azure-dev/pull/9079) Add service-scoped environment support for Foundry agent services while preserving project-wide fallback behavior.
- [[#9366]](https://github.com/Azure/azure-dev/pull/9366) Add an `--inspector-port` option to `azd ai agent run` so multiple local agents can use separate Inspector ports.

### Bugs Fixed

- [[#9326]](https://github.com/Azure/azure-dev/pull/9326) Validate Foundry dependencies before creating an agent version and provide actionable guidance when resources are not ready.
- [[#9367]](https://github.com/Azure/azure-dev/pull/9367) Fix Foundry network environment references to apply shared defaults, escaping, and unresolved-variable validation consistently during synthesis.
- [[#9397]](https://github.com/Azure/azure-dev/pull/9397) Stop agent lifecycle hooks from rewriting user-authored `azure.yaml` while preserving resolved deployment defaults.
- [[#9404]](https://github.com/Azure/azure-dev/pull/9404) Fix `azd ai agent init` re-prompting for agent settings when an existing `azure.yaml` already defines the agent.
- [[#9407]](https://github.com/Azure/azure-dev/pull/9407) Allow `azd ai agent init --infra` to continue through existing projects without a Foundry service and reject unsupported infrastructure layouts before mutation.
- [[#9422]](https://github.com/Azure/azure-dev/pull/9422) Restrict unified manifest adoption in `azd ai agent init` to manifests that declare an `azure.ai.agent` service.
- [[#9438]](https://github.com/Azure/azure-dev/pull/9438) Clarify agent initialization output by naming the agent added to `azure.yaml`.
- [[#9439]](https://github.com/Azure/azure-dev/pull/9439) Validate hosted-agent environment variable names before deployment and report actionable errors for invalid names.

## 1.0.0-beta.8 (2026-07-30)

### Features Added

- [[#9314]](https://github.com/Azure/azure-dev/pull/9314) Add `max_stalls` early-stopping option to the prompt-optimization YAML config and API. When N consecutive full validation-set evaluations produce no improvement, the optimizer stops early to save cost. Omitting `max_stalls` uses the service default (5). This is a YAML-only setting; no CLI flag is exposed. Thanks @imatiach-msft for the contribution!
- [[#9327]](https://github.com/Azure/azure-dev/pull/9327) Default new agents to `invocations` protocol version `2.0.0` (previously `1.0.0`). Existing manifests that pin `1.0.0` are unaffected.

### Bugs Fixed

- [[#9365]](https://github.com/Azure/azure-dev/pull/9365) Fix error message suggesting the removed `azd ai agent project set` command; the suggestion now correctly directs to `azd ai project set` (provided by the `azure.ai.projects` extension).
- [[#9328]](https://github.com/Azure/azure-dev/pull/9328) Fix RAI policy validation error referencing the legacy `rai_policy_name` key instead of the unified `azure.yaml` key `raiPolicyName`.
- [[#9291]](https://github.com/Azure/azure-dev/pull/9291) Fix `azd ai agent init --infra` not generating infrastructure after unified-manifest adoption or bare-definition reuse, including when invoked below the project root.
- [[#9290]](https://github.com/Azure/azure-dev/pull/9290) Fix default agent init model still pointing to deprecated `gpt-4.1-mini`; the interactive model-selection default is now `gpt-5.4-mini`.
- [[#9212]](https://github.com/Azure/azure-dev/pull/9212) Fix `azd ai agent init` not prompting for unset `${VAR}` environment references in adopted Foundry service configuration; prompted values are now persisted to the active azd environment with credential-like inputs masked.
- [[#9211]](https://github.com/Azure/azure-dev/pull/9211) Fix `azd ai agent init` replacing the full service block when resolving container defaults, which discarded service hooks and image templates in `azure.yaml`.
- [[#9280]](https://github.com/Azure/azure-dev/pull/9280) Fix `azd ai agent init` not preserving executable permissions on downloaded `.sh` files.
- [[#9237]](https://github.com/Azure/azure-dev/pull/9237) Fix `azd ai agent run` ignoring `uv.lock`; locked Python agent projects now use `uv sync --locked` instead of falling through to pip.

## 1.0.0-beta.7 (2026-07-23)

### Features Added

- [[#9009]](https://github.com/Azure/azure-dev/pull/9009) `azd ai agent init` now offers `invocations_ws` as a selectable agent protocol (including for bring-your-own-image `--image` init), while `responses` remains the default.
- [[#9204]](https://github.com/Azure/azure-dev/pull/9204) Add `eastus`, `italynorth`, `uaenorth`, `southcentralus`, `switzerlandwest`, `ukwest`, `westeurope`, `westcentralus`, and `japanwest` to the list of supported hosted agent regions.

### Bugs Fixed

- [[#9149]](https://github.com/Azure/azure-dev/pull/9149) Fix `azd ai agent run` and `azd deploy` not consistently resolving agent definitions declared inline in `azure.yaml`, via the deprecated `config:` block, or through local `$ref` files.
- [[#9171]](https://github.com/Azure/azure-dev/pull/9171) Fix modern Python agent projects (using `pyproject.toml`) being incorrectly routed to container deployment and prompted for an unnecessary Azure Container Registry during code deploy.
- [[#9205]](https://github.com/Azure/azure-dev/pull/9205) Fix `azd deploy` failing with HTTP 403 when the signed-in user's role (for example a subscription-inherited Owner or Azure AI Developer) lacked Cognitive Services data-plane access; the deploy-time RBAC check now recognizes only roles that grant it and auto-assigns the Foundry User role when needed.
- [[#9225]](https://github.com/Azure/azure-dev/pull/9225) Fix `azd ai agent init` accepting stale Azure Container Registry connections from an existing Foundry project; init now validates discovered registries against ARM and clears missing ones instead of deferring the failure until publish.
- [[#9254]](https://github.com/Azure/azure-dev/pull/9254) Fix `azd ai agent doctor` failing valid projects whose agent definition is declared inline in `azure.yaml`; the definition check now uses the same resolver as run and deploy.
- [[#9264]](https://github.com/Azure/azure-dev/pull/9264) Fix `azd ai agent doctor` reporting a false pass for an inline or `$ref` agent definition with an unsupported kind; resolved non-hosted definitions are now validated, while valid `workflow` definitions still pass.

### Other Changes

- [[#9133]](https://github.com/Azure/azure-dev/pull/9133) Foundry project provisioning has moved to the `azure.ai.projects` extension. Update `azure.ai.agents` and `azure.ai.projects` together, since mixing versions can cause both extensions to register the same `microsoft.foundry` provider.

## 1.0.0-beta.6 (2026-07-16)

### Features Added

- [[#9046]](https://github.com/Azure/azure-dev/pull/9046) `azd provision` now creates Foundry connections declared as `host: azure.ai.connection` services in `azure.yaml` at provision time via the `microsoft.foundry` synthesizer, for both greenfield and brownfield projects. Connection category, target, authentication type, credentials, and metadata are all supported. `azd deploy` for `host: azure.ai.connection` services is now a no-op; provision is the single source of truth.
- [[#9107]](https://github.com/Azure/azure-dev/pull/9107) Add `centralus` region to the list of supported hosted agent regions.
- [[#8942]](https://github.com/Azure/azure-dev/pull/8942) `azd deploy` can now carry over the current hosted-agent session across deploys. When `AZD_AGENT_RESUME_SESSION_ON_DEPLOY` is set to a truthy value, the session is stopped before deploy and re-pointed at the newly deployed version so the next invocation resumes with its `/home/session` volume intact instead of minting a fresh session.

### Bugs Fixed

- [[#9114]](https://github.com/Azure/azure-dev/pull/9114) Fix `azd ai agent invoke` failing with "agent name is required" on brownfield projects where the hosted agent name is written inline in the `azure.ai.agent` service config rather than emitted as a deployed environment output. The agent name is now seeded from the inline/config definition, with the deployed environment variable retaining precedence.
- [[#9007]](https://github.com/Azure/azure-dev/pull/9007) Add a preflight validation check that detects an immutable resource-group region conflict before provisioning. When the target resource group already exists in a different region than `AZURE_LOCATION`, the check surfaces the mismatch during azd's validation phase with clear remediation guidance instead of a slow deploy-time ARM `InvalidResourceGroupLocation` error.

### Other Changes

- [[#9112]](https://github.com/Azure/azure-dev/pull/9112) Terraform infrastructure eject now synthesizes `azure.ai.connection` services into Foundry project connection resources, preserving their category, target, authentication type, credentials, and metadata.
- [[#9049]](https://github.com/Azure/azure-dev/pull/9049) Switch the `invocations_ws` agent endpoint from the preview dispatcher form to the GA path-based route. `azd deploy` now registers `AGENT_{KEY}_INVOCATIONS_WS_ENDPOINT` (and `azd ai agent show` displays `Endpoint (invocations_ws)`) as `wss://<account>.services.ai.azure.com/api/projects/<project>/agents/<agent>/endpoint/protocols/invocations_ws?api-version=v1`, carrying the project and agent as path segments to mirror the HTTP `invocations` route. The previous form embedded them as `project_name`/`agent_name` query parameters on a single literal `/api/projects/agents/...` path.
- [[#9103]](https://github.com/Azure/azure-dev/pull/9103) Pin internal azd module dependency to released version.

## 1.0.0-beta.5 (2026-07-09)

### Features Added

- [[#9043]](https://github.com/Azure/azure-dev/pull/9043) Add `--client-header` to `azd ai agent invoke` for sending custom `x-client-*` request headers in `"Name: Value"` format (repeatable). The responses and invocations protocols forward the `x-client-*` header family to the agent; other header names are rejected, and the flag is not supported with the `a2a` protocol (which does not propagate `x-client-*` headers). Managed headers (`Authorization`, `Content-Type`, user identity) always take precedence.
- [[#8939]](https://github.com/Azure/azure-dev/pull/8939) Add native support for the Activity protocol to `azd ai agent`. `azd ai agent init` can now scaffold an Activity-protocol agent (defaulting to the service-recommended version `2.0.0`), `azd deploy` provisions a companion Azure Bot Service registration authenticated with `BotServiceRbac` and prints Microsoft Teams setup guidance, and `azd down` tears the bot down. Both init-from-code and init-from-manifest flows are supported.
- [[#8983]](https://github.com/Azure/azure-dev/pull/8983) `azd ai agent run` now supports activity-protocol agents in a pure-local inner loop — no Foundry deploy, no Azure Bot, no Teams sideload required. An M365 Agents Playground integration is available for local testing. Thanks @v1212 for the contribution!
- [[#8989]](https://github.com/Azure/azure-dev/pull/8989) Add `a2a` protocol support to `azd ai agent invoke`. A plain message is wrapped in a JSON-RPC 2.0 `message/send` request, `--input-file` sends a complete JSON-RPC request, and `--output raw` dumps the response verbatim. A2A is remote-only (not available with `--local`).
- [[#9003]](https://github.com/Azure/azure-dev/pull/9003) Improve `azd ai agent optimize` with live-updating candidate rows, phase-aware progress indicators, azure.yaml inline/config agent detection, and `--output json` support for `optimize status`. Thanks @Zyysurely for the contribution!

### Bugs Fixed

- [[#9012]](https://github.com/Azure/azure-dev/pull/9012) Fix `azd ai agent run` failing with `Python 3.13+ is required` when a compatible Python is installed but not first on `PATH`. When falling back to `pip`, azd now probes multiple interpreters (including the Windows `py -3` launcher, which selects the newest installed Python 3) and checks each one's version, selecting the first that satisfies the runtime instead of hard-failing on whichever `python` appears first on `PATH`.
- [[#9044]](https://github.com/Azure/azure-dev/pull/9044) Fix `azd ai agent invoke --local` failing with `could not connect to localhost:<port>` when run immediately after `azd ai agent run`, because the agent's listener binds a few seconds after `run` starts. `invoke --local` now retries connection-refused errors with backoff for up to 60s, and `azd ai agent run` waits up to 90s (covering slow interpreter startup and agent-stack imports before the server binds) for the port to accept connections before printing its "Agent ready" signal.
- [[#9041]](https://github.com/Azure/azure-dev/pull/9041) Update RBAC callouts in developer role checks and `azd ai doctor` to use the renamed Foundry built-in role names (`Foundry User`, `Foundry Project Manager`, `Foundry Account Owner`; formerly `Azure AI User`/`Project Manager`/`Account Owner`). The suggested `az role assignment create` commands now reference the role by its GUID so they keep working regardless of the display-name rollout.
- [[#9022]](https://github.com/Azure/azure-dev/pull/9022) Fix `azd down` on a Foundry (`microsoft.foundry`) project failing outright without `--force`. It now prompts for confirmation (naming the resource group to be deleted, defaulting to "no") like the built-in Bicep provider, and only falls back to requiring `--force` when there is no interactive terminal (for example under `--no-prompt` or in CI).
- [[#8987]](https://github.com/Azure/azure-dev/pull/8987) Fix `azd ai agent init -m <manifest>` not prompting for the agent name. The prompt default and project folder are now derived from the manifest's `template.name` (falling back to the top-level `name`), matching the interactive and template flows.
- [[#8981]](https://github.com/Azure/azure-dev/pull/8981) Fix `azd ai agent init -m <azure.yaml> --deploy-mode container` not resolving a container registry when adopting a unified Foundry `azure.yaml` on an existing Foundry project, which made `azd deploy` fail with `could not determine container registry endpoint`. The deploy mode is now resolved before Foundry project setup, so a container agent wires `AZURE_CONTAINER_REGISTRY_ENDPOINT` (or is signaled to create one on provision) while code deploy and `--image` still skip ACR.
- [[#9051]](https://github.com/Azure/azure-dev/pull/9051) Fix `azd ai agent init` with "Use an existing Foundry project" not stamping the `endpoint:` field on the `azure.ai.project` service in `azure.yaml`, which caused `azd up` to provision a brand-new account/project instead of reusing the selected one.

### Other Changes

- [[#8866]](https://github.com/Azure/azure-dev/pull/8866) Remove `Foundry-Features: *=V1Preview` opt-in headers now that Foundry hosted-agents, code-agents, and toolbox APIs are GA.

## 1.0.0-beta.4 (2026-07-03)

### Bugs Fixed

- [[#8947]](https://github.com/Azure/azure-dev/pull/8947) Fix brownfield Foundry provisioning failing with `InvalidTemplate` when a deploy reconciled model deployments without creating a container registry. `projectName` is now always passed to the brownfield template, so the existing project reference stays valid on the non-ACR path.

## 1.0.0-beta.3 (2026-07-03)

### Features Added

- [[#8852]](https://github.com/Azure/azure-dev/pull/8852) Provision Foundry memory stores during `azd deploy`. Declare one or more memory stores under the agent service's `memoryStores` list in `azure.yaml` (with `chatModel`, `embeddingModel`, and optional extraction/retention `options`), and azd creates them in the Foundry project before deploying the agent. Provisioning is idempotent: existing stores are left unchanged, so deployments are safe to re-run. azd does not update an existing store; if a declared definition diverges from the live store, deploy warns which `azure.yaml` change(s) were not applied.
- [[#8952]](https://github.com/Azure/azure-dev/pull/8952) `azd ai agent init` now routes unified `azure.yaml` templates selected from the template picker through the Foundry adoption flow, so choosing one downloads the `azure.yaml` and its sibling files and scaffolds the project instead of failing while trying to `git clone` a file URL.

### Bugs Fixed

- [[#8941]](https://github.com/Azure/azure-dev/pull/8941) Fix hosted agent deploys failing for users who lack `Microsoft.Authorization/roleAssignments/write`: the extension no longer assigns the redundant `Azure AI User` role to each per-agent managed identity after deploy, since Microsoft Foundry now grants that permission internally. Thanks @m5i-work for the contribution!
- [[#8926]](https://github.com/Azure/azure-dev/pull/8926) Fix `--deploy-mode`, `--runtime`, and `--entry-point` being silently ignored when `azd ai agent init -m <azure.yaml>` adopts a unified Foundry `azure.yaml`; the flags now apply `code_configuration` to the agent service, and an explicit `--deploy-mode` overrides a sample's pre-configured deploy mode.
- [[#8933]](https://github.com/Azure/azure-dev/pull/8933) Fix `azd ai agent init -m <azure.yaml>` returning early after scaffolding without running subscription selection, Foundry project setup, or model deployment verification, which left an environment that could not provision without manual configuration.

## 1.0.0-beta.2 (2026-07-01)

### Bugs Fixed

- [[#8901]](https://github.com/Azure/azure-dev/pull/8901) Remove duplicate service-target provider claims from the `azure.ai.agents` extension manifest for hosts now owned by the split Foundry extensions (`azure.ai.projects`, `azure.ai.connections`, `azure.ai.toolboxes`). Thanks @huimiu for the contribution!
- [[#8586]](https://github.com/Azure/azure-dev/issues/8586) `azd ai agent files upload` now accepts `[agent] [file]` positional arguments, mirroring `azd ai agent invoke [agent] [message]`. The first positional is the agent name and the second is the file to upload (with a single positional, it is the file, or the agent when `--file/-f` already supplies the file). This fixes the previous trap where passing the agent name as the positional left the agent unset and, in multi-service projects without `-n/--agent-name`, hung silently on the interactive agent picker in non-TTY contexts.

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8885]](https://github.com/Azure/azure-dev/pull/8885) `azd ai agent init -m <pointer>` now adopts a sample's unified `azure.yaml` as the project manifest when the pointer (local path or GitHub URL) resolves to one — that is, a manifest whose `services:` declare Foundry hosts (`azure.ai.project` / `azure.ai.agent` / `azure.ai.connection` / `azure.ai.toolbox`). The sample's `azure.yaml` and the files it references are placed at the project root via azd's native template adoption, and the services it already declares are not re-derived or duplicated under `src/<agent>/`. Pointing `-m` at an agent manifest (top-level `template:`) keeps the existing generate-from-manifest behavior, and adoption falls back to that path when a sample ships no `azure.yaml`. Adoption requires an empty target directory; adopting into a directory that already has a project `azure.yaml` is not yet supported.
- [[#8818]](https://github.com/Azure/azure-dev/pull/8818) `azd ai agent init` now writes each Foundry resource as its own `azure.yaml` service entry instead of bundling everything into the agent service. Model deployments become a single `azure.ai.project` service, each connection becomes an `azure.ai.connection` service, and each toolbox becomes an `azure.ai.toolbox` service, all wired to the agent through `uses:`. The `azure.ai.project`, `azure.ai.connection`, and `azure.ai.toolbox` hosts are now owned by their sibling extensions (`azure.ai.projects`, `azure.ai.connections`, `azure.ai.toolboxes`) as real deploy-time service targets. The agents extension no longer registers them as no-op hosts, and toolboxes are reconciled at `azd deploy` by the `azure.ai.toolbox` target rather than created during `azd provision`.
- [[#8780]](https://github.com/Azure/azure-dev/pull/8780) Add a `--call-id` flag to `azd ai agent invoke` that sends the `x-agent-foundry-call-id` header on `--local` invocations only. It is ignored for remote Foundry requests.
- [[#8879]](https://github.com/Azure/azure-dev/pull/8879) `azd deploy`/`azd up` now warn when two or more `azure.ai.agent` services resolve to the same Foundry agent `name`. Foundry identifies an agent by its name, so such services deploy to the same agent and overwrite each other; the warning names the colliding services so each can be given a unique name in `azure.yaml`. Deploy still proceeds.
- [[#8881]](https://github.com/Azure/azure-dev/pull/8881) Add `azd ai agent sessions stop <session-id>` to stop a running hosted agent session while preserving its persistent filesystem. Unlike `sessions delete`, the session is retained and can be resumed by a later invocation. Stopping an already-stopped session is idempotent and succeeds without error. Thanks @harsheet-shah for the contribution!
- [[#8869]](https://github.com/Azure/azure-dev/pull/8869) Add option to select an existing deployment when choosing a different model during `azd ai agent init`.
- [[#8874]](https://github.com/Azure/azure-dev/pull/8874) Increase default model deployment capacity from 10 to 50 for agents.
- [[#8754]](https://github.com/Azure/azure-dev/pull/8754) Add PR gate tests for the `azd ai agent` extension. Thanks @v1212 for the contribution!
- [[#8758]](https://github.com/Azure/azure-dev/pull/8758) Add live golden-path (Tier 2) pipeline for the `azd ai agent` extension. Thanks @v1212 for the contribution!
- [[#8788]](https://github.com/Azure/azure-dev/pull/8788) Migrate predeploy/postdeploy to service-level event handlers in the agents extension.
- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0` for all AI/Foundry extensions.

### Breaking Changes

- [[#8868]](https://github.com/Azure/azure-dev/pull/8868) `azd ai agent init` now defaults to **code deploy** (ZIP upload) instead of container deploy for Python and .NET projects. This affects `--no-prompt` runs without an explicit `--deploy-mode` flag. To preserve the previous behavior, pass `--deploy-mode container`. When code deploy is selected from a GitHub sample template, any Dockerfile and .dockerignore from the sample are removed from the scaffolded directory.
- [[#8780]](https://github.com/Azure/azure-dev/pull/8780) Replace the per-command Foundry isolation-key flags (`--user-isolation-key`, `--chat-isolation-key`, and the session-ownership `--isolation-key`) with a single `--user-identity` flag with no backward-compatible flag retention.

### Bugs Fixed

- [[#8883]](https://github.com/Azure/azure-dev/pull/8883) `azd up` now prompts for an Azure subscription and location when `AZURE_SUBSCRIPTION_ID` or `AZURE_LOCATION` is not set, matching core `azd up`, instead of failing. Under `--no-prompt` it still returns an actionable `azd env set ...` error. Fixes [[#8859]](https://github.com/Azure/azure-dev/issues/8859).
- [[#8880]](https://github.com/Azure/azure-dev/pull/8880) Fix ACR not created/linked for hosted container agents on existing Foundry projects. `azd provision` now connects to an existing Foundry project when the `azure.ai.project` service sets `endpoint:` (bring-your-own) instead of failing with a brownfield error, and `azd down` leaves a bring-your-own project in place because azd did not create it.
- [[#8769]](https://github.com/Azure/azure-dev/pull/8769) Reprompt on invalid agent name instead of crashing.
- [[#8770]](https://github.com/Azure/azure-dev/pull/8770) Avoid double agent service prompt in `azd ai agent invoke`.
- [[#8771]](https://github.com/Azure/azure-dev/pull/8771) Allow `--local` with a named agent in `azd ai agent invoke`.
- [[#8787]](https://github.com/Azure/azure-dev/pull/8787) Use venv for pip fallback in `azd ai agent run`.
- [[#8829]](https://github.com/Azure/azure-dev/pull/8829) Update container deploy schema to use `protocol_versions` and `container_configuration`. Thanks @v1212 for the contribution!
- [[#8867]](https://github.com/Azure/azure-dev/pull/8867) Fix placeholder warning to reference `azure.yaml` instead of `agent.yaml`.
- [[#8876]](https://github.com/Azure/azure-dev/pull/8876) Fix `azd ai agent init --image` azure.yaml output. Thanks @m5i-work for the contribution!
- [[#8789]](https://github.com/Azure/azure-dev/pull/8789) Update agent models to match TypeSpec definition.

## 0.1.41-preview (2026-06-19)

- [[#8731]](https://github.com/Azure/azure-dev/pull/8731) Improve the post-deploy `Next:` guidance with a stacked layout that puts each command on its own line above its description, adds a blank line between suggestions, and highlights `azd` commands. The new layout applies across deploy, `azd ai agent show`, `init`, and `doctor`. Thanks @therealjohn for the contribution!
- [[#8645]](https://github.com/Azure/azure-dev/pull/8645) Detect VNET-injected Foundry accounts during `azd ai agent init` and skip remote builds up front so hosted container agents use local builds without a failing remote-build attempt first. Thanks @m5i-work for the contribution!
- [[#8714]](https://github.com/Azure/azure-dev/pull/8714) Show a tracing disclaimer when `azd ai agent init` connects or adds an Application Insights connection. Thanks @therealjohn for the contribution!
- [[#8685]](https://github.com/Azure/azure-dev/pull/8685) Default `azd ai agent run` local Python virtual environments to Python >= 3.13 so local runs match the minimum supported Foundry runtime. Thanks @therealjohn for the contribution!
- [[#8732]](https://github.com/Azure/azure-dev/pull/8732) Update the Application Insights tracing disclaimer shown during `azd ai agent init` with revised wording and a `Learn more` link. Thanks @therealjohn for the contribution!

## 0.1.40-preview (2026-06-15)

- [[#8641]](https://github.com/Azure/azure-dev/pull/8641) Fix optimize/eval handling for array-valued mutations, resolve `dataset.local_uri` relative to the agent project, and align optimize test schema data with the current API format. Thanks @Zyysurely for the contribution!
- [[#8625]](https://github.com/Azure/azure-dev/pull/8625) Show server response timing after successful `azd ai agent invoke` calls, including total latency and time to first byte, while keeping failure and `--output raw` flows unchanged. Thanks @VBhadauria for the contribution!
- [[#8624]](https://github.com/Azure/azure-dev/pull/8624) Add Python bundled-mode guidance after `azd ai agent init` and block `azd deploy` with clear remediation when bundled dependencies were not installed into the source directory. Thanks @v1212 for the contribution!

## 0.1.39-preview (2026-06-11)

- [[#8602]](https://github.com/Azure/azure-dev/pull/8602) Support Foundry `${{...}}` server-side expressions during `azd ai agent` environment-variable expansion, so values that mix azd `${VAR}` references with Foundry `${{...}}` expressions (and `${VAR:-default}` forms) resolve correctly instead of being dropped. Thanks @huimiu for the contribution!
- [[#8589]](https://github.com/Azure/azure-dev/pull/8589) Align `azd ai agent optimize` and `eval` with the V2Preview optimization API, including snake_case payloads, the new `agent_optimization_jobs` endpoints, a required `optimization_model`, and a Strategy column in the results table. Thanks @Zyysurely for the contribution!
- [[#8576]](https://github.com/Azure/azure-dev/pull/8576) Add `azd ai agent code download` command to download (and optionally extract) the deployed source code of a code-based hosted agent, with SHA-256 verification. Thanks @v1212 for the contribution!
- [[#8574]](https://github.com/Azure/azure-dev/pull/8574) Add `azd ai agent endpoint show` command to display the live endpoint configuration, and warn before `azd ai agent endpoint update` applies a breaking authorization isolation-key-source change. Thanks @v1212 for the contribution!
- [[#8566]](https://github.com/Azure/azure-dev/pull/8566) Rename `azd ai agent eval init` to `eval generate` (keeping a hidden, deprecated `init`), honor the `-e`/`--environment` flag in optimize/eval flows, resolve relative `--dataset` paths against the current directory, and reconcile stale agent versions from the environment. Thanks @Zyysurely for the contribution!
- [[#8519]](https://github.com/Azure/azure-dev/pull/8519) Add `azd ai agent delete` command to delete a hosted agent and all of its versions, with `--force` to terminate active sessions. Thanks @v1212 for the contribution!

## 0.1.38-preview (2026-06-05)

- [[#8532]](https://github.com/Azure/azure-dev/pull/8532) Fix Agent Inspector auto-launch for slow-starting local agents by waiting for the local agent port instead of timing out after 30 seconds. Thanks @anchenyi for the contribution!
- [[#8529]](https://github.com/Azure/azure-dev/pull/8529) Update `azd ai agent init` to write a salted `AZURE_RESOURCE_GROUP` value to `.env`, reducing resource group collisions when recreating projects. Thanks @banrahan for the contribution!
- [[#8521]](https://github.com/Azure/azure-dev/pull/8521) Add generic `policies` (`rai_policy`) support to the `agent.yaml` manifest for hosted agents so users can attach governance policies via `rai_policy_name`. Thanks @amitbhave10 for the contribution!
- [[#8522]](https://github.com/Azure/azure-dev/pull/8522) Fix `azd ai agent init` from a manifest in the current directory when the target project is created in a subdirectory. Thanks @v1212 for the contribution!

## 0.1.37-preview (2026-06-01)

- [[#8512]](https://github.com/Azure/azure-dev/pull/8512) Normalize connection auth `AgenticIdentity` values to the ARM-required `AgenticIdentityToken`.
- [[#8508]](https://github.com/Azure/azure-dev/pull/8508) Add `Foundry-Features: HostedAgents=V1Preview` on all Foundry data-plane endpoint requests to prevent preview feature failures.
- [[#8488]](https://github.com/Azure/azure-dev/pull/8488) Add a resource-token salt to avoid 404 failures when recreating AI agents.

## 0.1.36-preview (2026-05-30)

- [[#8500]](https://github.com/Azure/azure-dev/pull/8500) workaround: api-version query param for non-conformant openai agent endpoints

## 0.1.35-preview (2026-05-29)

- [[#8406]](https://github.com/Azure/azure-dev/pull/8406) Add `--output raw` (`-o raw`) flag to `azd ai agent invoke` to dump the unmodified server response (status line, headers, and body verbatim) to stdout. Useful for debugging server behavior and inspecting response headers such as the agent version. Friendly summary lines (`Session:`, `Invocation:`, `Trace ID:`, `Version:`) are suppressed in raw mode.
- [[#8403]](https://github.com/Azure/azure-dev/pull/8403) Add `invocations_ws` as a displayable agent protocol. `azd deploy` now registers the callable Foundry data-plane WebSocket URL (`wss://<account>.services.ai.azure.com/api/projects/agents/endpoint/protocols/invocations_ws?api-version=v1&project_name=<project>&agent_name=<agent>`) as `AGENT_{KEY}_INVOCATIONS_WS_ENDPOINT`, and `azd ai agent show` displays it as `Endpoint (invocations_ws)`. Previously, `invocations_ws` agents fell back to the legacy resource URL labeled `Endpoint (Agent)`.
- [[#8358]](https://github.com/Azure/azure-dev/pull/8358) Add full OAuth2 fields and connector-name support to `azd ai agent connection create`, including validation for managed-connector vs bring-your-own OAuth2 flows.
- [[#8326]](https://github.com/Azure/azure-dev/pull/8326) Reuse an existing local `agent.yaml` definition during `azd ai agent init` instead of prompting to overwrite or failing in no-prompt mode.
- [[#8364]](https://github.com/Azure/azure-dev/pull/8364) Adapt `azd ai agent optimize` to the updated optimize service request/response contract, including the new `optimizationConfig` model.
- [[#8389]](https://github.com/Azure/azure-dev/pull/8389) Honor `.azdignore` during `azd ai agent init` template and manifest materialization flows.
- [[#8378]](https://github.com/Azure/azure-dev/pull/8378) Update environment-variable parsing behavior for `azd ai agent run`.
- [[#8394]](https://github.com/Azure/azure-dev/pull/8394) Remove the broken `doctor` model-deployments check that produced false failures from manifest alias names.
- [[#8393]](https://github.com/Azure/azure-dev/pull/8393) Improve post-init/run `Next:` guidance for toolbox scenarios and standardize local invoke examples.
- [[#8398]](https://github.com/Azure/azure-dev/pull/8398) Add `germanywestcentral` and `canadaeast` to the hosted-agent supported regions list.
- [[#8400]](https://github.com/Azure/azure-dev/pull/8400) Show connection metadata key/value pairs in `azd ai agent connection show` table output.
- [[#8405]](https://github.com/Azure/azure-dev/pull/8405) Fix YAML tags to use snake_case (`agent_endpoint`, `agent_card`) so agent endpoint settings are correctly loaded from `agent.yaml`.
- [[#8363]](https://github.com/Azure/azure-dev/pull/8363) Allow `azd ai agent init --no-prompt` to defer Azure/model setup when Azure context variables are missing.
- [[#8347]](https://github.com/Azure/azure-dev/pull/8347) Use `api-version=v1` for hosted agent endpoint protocol and session requests.
- [[#8422]](https://github.com/Azure/azure-dev/pull/8422) Remove the hardcoded code-deploy region allowlist and use dynamic hosted-agent region resolution.
- [[#8392]](https://github.com/Azure/azure-dev/pull/8392) Improve optimize config YAML deserialization/UX and align generation endpoint calls to `2025-11-15-preview`.
- [[#8426]](https://github.com/Azure/azure-dev/pull/8426) Add opinionated defaults for manifest-driven `azd ai agent init -m` to reduce interactive prompts.
- [[#8441]](https://github.com/Azure/azure-dev/pull/8441) Fix hosted-agent deploy failures on `CreateAgentVersion` by including `Foundry-Features: HostedAgents=V1Preview` on v1 requests.
- [[#8479]](https://github.com/Azure/azure-dev/pull/8479) Add dependency on `azure.ai.inspector`, for handling with `azd ai agent run`.
- [[#8482]](https://github.com/Azure/azure-dev/pull/8482) Improve ACR publish error handling by classifying permission-denied failures and surfacing actionable RBAC/code-deploy remediation guidance.

### Breaking Changes
- [[#8210]](https://github.com/Azure/azure-dev/pull/8210) Update sample-based init flow to create a new folder during `azd ai agent init`.
- [[#8357]](https://github.com/Azure/azure-dev/pull/8357) Migrate connection CRUD commands from `azure.ai.agents` to the `azure.ai.connections` extension.

## 0.1.34-preview (2026-05-22)

- [[#8264]](https://github.com/Azure/azure-dev/pull/8264) Launch Agent Inspector automatically on `azd ai agent run`. Use `--no-inspector` to opt out. Requires the `azure.ai.inspector` extension.
- [[#8327]](https://github.com/Azure/azure-dev/pull/8327) Add `RemoteA2A` connection kind and expand auth type support for `azd ai agent connection create`, including OAuth2, user Entra token, project managed identity, and agentic identity token auth types.
- [[#8321]](https://github.com/Azure/azure-dev/pull/8321) Introduce `AZURE_AI_DEPLOYMENTS_LOCATION` to decouple model/project deployment location from resource group location (`AZURE_LOCATION`), fixing provisioning failures when a Foundry project and resource group are in different regions.
- [[#8324]](https://github.com/Azure/azure-dev/pull/8324) Add `--deploy-mode`, `--runtime`, `--entry-point`, and `--dep-resolution` flags to `azd ai agent init` for non-interactive code deploy support in CI/CD pipelines.
- [[#8198]](https://github.com/Azure/azure-dev/pull/8198) Add `azd ai agent doctor` diagnostics command with checks for project setup, environment variables, authentication, Foundry reachability, hosted agent status, and more. Add context-aware `Next:` guidance across init, run, invoke, show, and deploy-hook flows.
- [[#8306]](https://github.com/Azure/azure-dev/pull/8306) Add `azd ai agent eval` and `azd ai agent optimize` command families for evaluating and iteratively optimizing AI agents.
- [[#8332]](https://github.com/Azure/azure-dev/pull/8332) Add handling to update agent endpoint details when an agent is redeployed to a new endpoint.

## 0.1.33-preview (2026-05-21)

- [[#8299]](https://github.com/Azure/azure-dev/pull/8299) Don't fail `azd ai agent init` when Foundry agent existence checks error.
- [[#8298]](https://github.com/Azure/azure-dev/pull/8298) Use the selected agent name for service entry when resolving Foundry name conflicts.
- [[#8292]](https://github.com/Azure/azure-dev/pull/8292) Decouple Foundry project selection from model configuration during agent init.
- [[#8271]](https://github.com/Azure/azure-dev/pull/8271) Remove the 0.25 CPU option for hosted agents.
- [[#8266]](https://github.com/Azure/azure-dev/pull/8266) Add `azd ai agent sample list` and improve non-interactive `azd ai agent init`.
- [[#8245]](https://github.com/Azure/azure-dev/pull/8245) Rename the project endpoint environment variable to `FOUNDRY_PROJECT_ENDPOINT`.
- [[#8242]](https://github.com/Azure/azure-dev/pull/8242) Skip ACR creation and startup command configuration for code deploy.
- [[#8233]](https://github.com/Azure/azure-dev/pull/8233) Allow `azd ai agent invoke` to target specific versions.
- [[#8206]](https://github.com/Azure/azure-dev/pull/8206) Support header isolation keys for agent sessions.
- [[#8189]](https://github.com/Azure/azure-dev/pull/8189) Add naming safeguards for `azd ai agent init`.
- [[#7898]](https://github.com/Azure/azure-dev/pull/7898) Remove the hardcoded invoke suggestion from `azd ai agent run`.

### Breaking Changes

- [[#8293]](https://github.com/Azure/azure-dev/pull/8293) Remove deprecated runtimes (Python 3.11/3.12 and .NET 8/9) from `azd ai agent init`.
- [[#8243]](https://github.com/Azure/azure-dev/pull/8243) Migrate project endpoint commands to the new scaffold.

## 0.1.32-preview (2026-05-18)

- [[#8223]](https://github.com/Azure/azure-dev/pull/8223) Add `.agentignore` support for controlling which files are excluded from agent code-deploy ZIP packaging. Uses `.gitignore` syntax with sensible defaults generated during `azd ai agent init`.
- [[#8222]](https://github.com/Azure/azure-dev/pull/8222) Add post-init validation to check .NET runtime compatibility with project TargetFramework and show guidance when mismatched.
- [[#7865]](https://github.com/Azure/azure-dev/pull/7865) Improve `azd ai agent invoke` trace ID handling for consistent responses, including deduping comma-folded request IDs.
- [[#8184]](https://github.com/Azure/azure-dev/pull/8184) Default `azd ai agent show` output to table format.
- [[#8182]](https://github.com/Azure/azure-dev/pull/8182) Add guidance for deploying with private ACR images.
- [[#8181]](https://github.com/Azure/azure-dev/pull/8181) Increase timeout used by `azd ai agent invoke`.
- [[#8175]](https://github.com/Azure/azure-dev/pull/8175) Wait for deployed agents to reach active state before command completion.
- [[#8174]](https://github.com/Azure/azure-dev/pull/8174) Add `azd ai agent connection` commands and credential resolution for local run. (will be removed in a future release)
- [[#8162]](https://github.com/Azure/azure-dev/pull/8162) Add `azd ai agent project` commands for managing Foundry project endpoints. (will be removed in a future release)
- [[#8161]](https://github.com/Azure/azure-dev/pull/8161) Add .NET code deploy support (dotnet 8/9/10 runtimes).
- [[#8146]](https://github.com/Azure/azure-dev/pull/8146) Support code deploy zip uploads.
- [[#8104]](https://github.com/Azure/azure-dev/pull/8104) Add support for deploying from an existing ACR image.
- [[#8075]](https://github.com/Azure/azure-dev/pull/8075) Show featured templates first during `azd ai agent init`.

## 0.1.31-preview (2026-05-07)

- [[#8096]](https://github.com/Azure/azure-dev/pull/8096) Fix for bug introduced with #8034. Properly storing root agent endpoint since sessions are independent of protocol.
- [[#8038]](https://github.com/Azure/azure-dev/pull/8038) Fix MCP tool field mapping to correctly include `url` values from tool definitions. Fixes mapping for toolbox tools to connections.

## 0.1.30-preview (2026-05-06)

- [[#8028]](https://github.com/Azure/azure-dev/pull/8028) Add `--agent-endpoint` flag to `azd ai agent invoke` to invoke a deployed agent from any directory without needing an azd project or environment. Thanks @antriksh30 for the contribution!
- [[#7999]](https://github.com/Azure/azure-dev/pull/7999) Add A2A endpoint protocol and agent card metadata support for agent deployments. Thanks @adamra-msft for the contribution!
- [[#8027]](https://github.com/Azure/azure-dev/pull/8027) Add playground URL and per-protocol endpoint URLs to `azd ai agent show` output. Thanks @Nathandrake229 for the contribution!
- [[#8034]](https://github.com/Azure/azure-dev/pull/8034) Move session and conversation ID tracking to the global azd user config, enabling session state to persist across directories and project relocations.
- [[#7947]](https://github.com/Azure/azure-dev/pull/7947) Fix `flag redefined` panics on `azd ai agent show`, `azd ai agent files list`, and `azd ai agent files stat` caused by duplicate `--output`/`-o` flag registration.
- [[#7968]](https://github.com/Azure/azure-dev/pull/7968) Fix agent templates URL used by `azd ai agent init` to use the correct `aka.ms` redirect after release.

### Breaking Changes

- [[#8040]](https://github.com/Azure/azure-dev/pull/8040) Remove prompt agent and `azureml://` registry support; prompt agent configurations in `azure.yaml` are no longer recognized.

## 0.1.29-preview (2026-04-30)

- [[#7984]](https://github.com/Azure/azure-dev/pull/7984) Fix `postdeployHandler` to skip post-deploy processing when the project has no hosted agent services, preventing errors on non-agent projects.
- [[#7974]](https://github.com/Azure/azure-dev/pull/7974) Update post-deploy output to display the agent invocation endpoint URL.
- [[#7966]](https://github.com/Azure/azure-dev/pull/7966) Update the `aka.ms` redirect URL used to fetch the agent templates list.
- [[#7921]](https://github.com/Azure/azure-dev/pull/7921) Update `azd ai agent init` to load agent templates from the unified awesome-azd `templates.json` manifest, filtered by the `extension.ai.agent` type discriminator.

## 0.1.28-preview (2026-04-28)

- [[#7930]](https://github.com/Azure/azure-dev/pull/7930) Fetch the hosted-agent supported regions list at runtime from a remote JSON manifest with an embedded fallback, replacing the hardcoded list; region data can now be updated without cutting an extension release.

## 0.1.27-preview (2026-04-22)

- [[#7880]](https://github.com/Azure/azure-dev/pull/7880) Remove ACR endpoint pre-check from the package step; packaging no longer fails early when `AZURE_CONTAINER_REGISTRY_ENDPOINT` is absent, allowing provisioning to create the registry first for new projects.

## 0.1.26-preview (2026-04-21)

- [[#7843]](https://github.com/Azure/azure-dev/pull/7843) When `azd ai agent init` uses an existing Azure AI project, set `USE_EXISTING_AI_PROJECT=true` so downstream Bicep provisioning skips creating the project, roles, and connections again.
- [[#7835]](https://github.com/Azure/azure-dev/pull/7835) Add validation for missing container registry endpoints in agent service configuration.
- [[#7790]](https://github.com/Azure/azure-dev/pull/7790) Improve `azd ai agent monitor` output: render each SSE log event as a single compact, color-coded line (`HH:MM:SS  <stream>  <message>`) with session-metadata events rendered as `session <state> (v<version>, last accessed: ...)`. Add `--utc` flag to display timestamps in UTC instead of local time, and `--raw` flag to preserve the previous raw SSE output.
- [[#7834]](https://github.com/Azure/azure-dev/pull/7834) Implement flexible timestamp parsing for `modified_time` values in JSON responses.

### Breaking Changes

- [[#7764]](https://github.com/Azure/azure-dev/pull/7764) Remove `container.scale` configuration (`minReplicas`/`maxReplicas`) from `azure.yaml`. Scale settings are no longer supported for hosted agents. Remove any `container.scale` section from your service configuration.

## 0.1.25-preview (2026-04-20)

- [[#7811]](https://github.com/Azure/azure-dev/pull/7811) Fix agent deployment RBAC checks to show warnings instead of blocking deployment when role assignment issues are encountered.
- [[#7808]](https://github.com/Azure/azure-dev/pull/7808) Add Azure AI Project Manager and Azure AI Account Owner as accepted roles in the developer RBAC role-assignment-write preflight check.
- [[#7807]](https://github.com/Azure/azure-dev/pull/7807) Fix `azd ai agent invoke` to use the correct endpoint for creating conversations.

## 0.1.24-preview (2026-04-17)

- [[#7765]](https://github.com/Azure/azure-dev/pull/7765) Improve invalid manifest error messaging to guide users to check for a required `template` field.
- [[#7763]](https://github.com/Azure/azure-dev/pull/7763) Fix developer RBAC pre-flight gaps by auto-assigning Azure AI User when missing, adding an explicit role-assignment-write check, and handling ABAC-enabled ACR registries.
- [[#7747]](https://github.com/Azure/azure-dev/pull/7747) Update agent identity RBAC resolution to read identity information from the agent version instead of relying on graph lookup.

### Breaking Changes

- [[#7741]](https://github.com/Azure/azure-dev/pull/7741) Remove `-e` shorthand for `--environment` on `azd ai agent init`; use `--environment` instead to avoid collision with azd global `-e/--environment`.

## 0.1.23-preview (2026-04-16)

- [[#7753]](https://github.com/Azure/azure-dev/pull/7753) Fix `azd ai agent init` to pass the current directory as a positional argument to `azd init`, resolving failures caused by a missing `cwd` assumption in the underlying azd call.
- [[#7729]](https://github.com/Azure/azure-dev/pull/7729) Fix `azd ai agent run` for .NET agents.
- [[#7574]](https://github.com/Azure/azure-dev/pull/7574) Add `azd ai agent sessions` command group for listing, creating, and deleting agent sessions; improve `azd ai agent files` commands with positional argument support; rename `--session` flag to `--session-id` across all commands.
- [[#7725]](https://github.com/Azure/azure-dev/pull/7725) Improve `--protocol` flag handling in `azd ai agent invoke` to correctly resolve the protocol when multiple protocols are configured.
- [[#7640]](https://github.com/Azure/azure-dev/pull/7640) Add toolbox (MCP) support: provision MCP toolbox connections via `FOUNDRY_TOOLBOX_*` environment variables and add OAuth connection fields (`authorizationUrl`, `tokenUrl`, `refreshUrl`, `scopes`, `audience`, `connectorName`) to connection resources in `azure.yaml`.
- [[#7614]](https://github.com/Azure/azure-dev/pull/7614) Fail fast on `azd ai agent init` when the user is not logged in, before any file-modifying operations begin.
- [[#7679]](https://github.com/Azure/azure-dev/pull/7679) Add `--protocol` flag to `azd ai agent invoke` to explicitly select between `responses` and `invocations` protocols when multiple are configured; return a clear error when `invocations` is requested but not enabled.
- [[#7675]](https://github.com/Azure/azure-dev/pull/7675) Add unit tests and testdata for the extension, covering agent YAML mapping, registry API helpers, API model serialization, and command utilities.

## 0.1.22-preview (2026-04-10)

- [[#7633]](https://github.com/Azure/azure-dev/pull/7633) Fix `azd ai agent init` to correctly set `AZURE_AI_MODEL_DEPLOYMENT_NAME` when initializing from a manifest, template, or `--model`/`--model-deployment` flags.
- [[#7635]](https://github.com/Azure/azure-dev/pull/7635) Fix `azd ai agent invoke` to persist an explicitly passed `--session-id` so that subsequent `azd ai agent monitor` calls can reuse the session without error.
- [[#7636]](https://github.com/Azure/azure-dev/pull/7636) Add positional argument support to `azd ai agent init`; passing a URL, manifest path, or source directory is now auto-disambiguated and equivalent to using `--manifest` or `--src`.
- [[#7645]](https://github.com/Azure/azure-dev/pull/7645) Fix `azd ai agent init -m` when adding to an existing azd project: reuse the Foundry project from the environment, show a message when an existing azd project is detected, and prompt to resolve service name collisions.

### Breaking Changes

- [[#7651]](https://github.com/Azure/azure-dev/pull/7651) Switch agent identity RBAC from a shared project-level identity to per-agent identities (`{account}-{project}-{agentName}-AgentIdentity`), add developer RBAC pre-flight checks before deploy, and remove Cognitive Services OpenAI User and Monitoring Metrics Publisher role assignments; set `AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true` to skip all role assignments in CI/CD environments.

## 0.1.21-preview (2026-04-09)

- [[#7484]](https://github.com/Azure/azure-dev/pull/7484) Detect an `agent.manifest.yaml` in the current directory and prompt to use it when running `azd ai agent init`.
- [[#7464]](https://github.com/Azure/azure-dev/pull/7464) Prompt for agent communication protocol (responses or invocations) when using `azd ai agent init` with local code.
- [[#7415]](https://github.com/Azure/azure-dev/pull/7415) Filter `azd ai agent init` prompts to only show locations and models supported for agent scenarios.
- [[#7410]](https://github.com/Azure/azure-dev/pull/7410) Fix `azd ai agent init --project-id` when `agent.yaml` does not contain a model resource.
- [[#7545]](https://github.com/Azure/azure-dev/pull/7545) Update agent endpoint handling to use the latest Foundry agent service endpoints.
- [[#7538]](https://github.com/Azure/azure-dev/pull/7538) Fix `azd ai agent invoke` streaming output to print each SSE data object on its own line.
- [[#7576]](https://github.com/Azure/azure-dev/pull/7576) Add validation to `azd ai agent init` to ensure the manifest path points to a file, not a directory.
- [[#7553]](https://github.com/Azure/azure-dev/pull/7553) Update `azd ai agent init` to stop writing `AZURE_AI_PROJECT_ENDPOINT` and `AZURE_OPENAI_ENDPOINT` to `agent.yaml`; `azd ai agent run` now translates `AZURE_AI_*` env vars to `FOUNDRY_*` equivalents for local agent processes.
- [[#7596]](https://github.com/Azure/azure-dev/pull/7596) Reduce noisy output during `azd ai agent init` by redirecting internal log statements to the debug log file; verbose details are now only visible when `--debug` is used.
- [[#7607]](https://github.com/Azure/azure-dev/pull/7607) Fix `azd ai agent init` container resource selection to save the chosen CPU/memory tier into `agent.yaml` and pre-select the existing tier on reruns; remove stale replica mention from post-init message.
- [[#7589]](https://github.com/Azure/azure-dev/pull/7589) Fix `azd ai agent init` to prompt for an existing Foundry project when the agent manifest contains no model resources.

## 0.1.20-preview (2026-04-02)

- [[#7422]](https://github.com/Azure/azure-dev/pull/7422) Add `/invocations` API support to `azd ai agent invoke`, enabling agents to accept arbitrary input passed directly to the agent.
- [[#7341]](https://github.com/Azure/azure-dev/pull/7341) Fix `azd ai agent init` writing unnecessary `scale` configuration for vnext-hosted agents, which is now skipped when vnext is enabled. Thanks @spboyer for the contribution!

## 0.1.19-preview (2026-03-31)

- [[#7327]](https://github.com/Azure/azure-dev/pull/7327) Fix `azd ai agent init` reruns to reuse an existing azd environment instead of failing when a previous attempt already created it.
- [[#7332]](https://github.com/Azure/azure-dev/pull/7332) Improve `azd ai agent init` performance when discovering existing Foundry projects, including faster `--project-id` validation.
- [[#7355]](https://github.com/Azure/azure-dev/pull/7355) Update generated Application Insights environment variables to reuse the selected connection and avoid redundant connection creation during deployment.
- [[#7373]](https://github.com/Azure/azure-dev/pull/7373) Fix `postdeploy` hook failures in projects without hosted agents so unrelated azd projects no longer error during deploy.

## 0.1.18-preview (2026-03-23)

- [[#7147]](https://github.com/Azure/azure-dev/pull/7147) Add `azd ai agent init` support for initializing from an agent template.

## 0.1.17-preview (2026-03-20)

- [[#7214]](https://github.com/Azure/azure-dev/pull/7214) Add ASCII art banner with Foundry branding and version info displayed at extension startup.
- [[#7217]](https://github.com/Azure/azure-dev/pull/7217) Update container settings to use discrete CPU and memory options, and remove min/max replicas prompts.

## 0.1.16-preview (2026-03-18)

- [[#7141]](https://github.com/Azure/azure-dev/pull/7141) Add `azd ai agent files` command group with `upload`, `download`, `list`, and `remove` subcommands for managing session-scoped files on hosted agent sandboxes.
- [[#7175]](https://github.com/Azure/azure-dev/pull/7175) Improve input validation, error handling, and path safety across the extension, including hardened path resolution, sensitive data redaction in error paths, and WebSocket origin validation.

### Breaking Changes

- [[#7181]](https://github.com/Azure/azure-dev/pull/7181) Update `azd ai agent show` and `azd ai agent monitor` commands to read agent name and version from the azd service entry, removing the requirement to pass them as parameters.

## 0.1.15-preview (2026-03-13)

- [[#7080]](https://github.com/Azure/azure-dev/pull/7080) Fix authentication failures (`AADSTS70043`/`AADSTS700082`) for multi-tenant and guest users by using `UserTenantId` for credential resolution

## 0.1.14-preview (2026-03-10)

- [[#7026]](https://github.com/Azure/azure-dev/pull/7026) Add `azd ai agent run` and `azd ai agent invoke` commands for running agents locally and invoking agents via a /responses call
- [[#6980]](https://github.com/Azure/azure-dev/pull/6980) Add `--model-deployment` parameter to `azd ai agent init` and fix agent init in copilot/CI scenarios
- [[#6979]](https://github.com/Azure/azure-dev/pull/6979) Detect and classify auth errors from azd core for improved error telemetry

## 0.1.13-preview (2026-03-04)

- [[#6957]](https://github.com/Azure/azure-dev/pull/6957) Fix unmarshal error during `azd ai agent init`

## 0.1.12-preview (2026-02-27)

- [[#6892]](https://github.com/Azure/azure-dev/pull/6892) Fix selected model check during `azd ai agent init` from code to correctly handle existing versus new model deployments
- [[#6909]](https://github.com/Azure/azure-dev/pull/6909) Add `AZURE_AI_PROJECT_ENDPOINT` to default agent environment variables and improve `AZURE_AI_MODEL_DEPLOYMENT_NAME` env var handling
- [[#6895]](https://github.com/Azure/azure-dev/pull/6895) Add `azd ai agent logs` and `azd ai agent status` commands for viewing agent run logs and deployment status
- [[#6901]](https://github.com/Azure/azure-dev/pull/6901) Add structured error handling with improved service error mapping for more informative error messages

## 0.1.11-preview (2026-02-24)

- [[#6828]](https://github.com/Azure/azure-dev/pull/6828) Add new "init from code" flow allowing users to run `azd ai agent init` without an existing project, template, or manifest
- [[#6867]](https://github.com/Azure/azure-dev/pull/6867) Add default model selection for the basic init flow

## 0.1.10-preview (2026-02-19)

- [[#6749]](https://github.com/Azure/azure-dev/pull/6749) Add "Choose a different model (all regions)" option during model selection recovery
- [[#6749]](https://github.com/Azure/azure-dev/pull/6749) Display quota availability info in model deployment prompts
- [[#6749]](https://github.com/Azure/azure-dev/pull/6749) Improve `AZURE_AI_PROJECT_ID` and deployment capacity validation

## 0.1.9-preview (2026-02-05)

- [[#6631]](https://github.com/Azure/azure-dev/pull/6631) Add support for downloading manifests from public repositories without authentication
- [[#6665]](https://github.com/Azure/azure-dev/pull/6665) Fix manifest download path handling when path contains slashes
- [[#6670]](https://github.com/Azure/azure-dev/pull/6670) Simplify `azd ai agent init` to use `--minimal` flag, reducing prompts
- [[#6672]](https://github.com/Azure/azure-dev/pull/6672) Block attempts to use extension with prompt agents (not yet supported)
- [[#6683]](https://github.com/Azure/azure-dev/pull/6683) Fix panic when parsing `agent.yaml` files without a `template` field
- [[#6693]](https://github.com/Azure/azure-dev/pull/6693) Fix unsafe DefaultAzureCredential usage
- [[#6695]](https://github.com/Azure/azure-dev/pull/6695) Display agent endpoint as plain text with documentation link instead of clickable hyperlink
- [[#6730]](https://github.com/Azure/azure-dev/pull/6730) Improve model selection handling when model is unavailable in current region

## 0.1.8-preview (2026-01-26)

- [[#6611]](https://github.com/Azure/azure-dev/pull/6611) Statically link the Linux amd64 binary for compatibility with older Linux versions

## 0.1.6-preview (2026-01-22)

- [[#6541]](https://github.com/Azure/azure-dev/pull/6541) Add metadata capability
- [[#6541]](https://github.com/Azure/azure-dev/pull/6541) Support `AZD_EXT_DEBUG=true` for debugging

## 0.1.5-preview (2026-01-12)

- [[#6468]](https://github.com/Azure/azure-dev/pull/6468) Add support for retrieving existing Application Insights connections when using `--project-id`
- [[#6482]](https://github.com/Azure/azure-dev/pull/6482) Improve `azd ai agent init -m` validation

## 0.1.4-preview (2025-12-15)

- [[#6326]](https://github.com/Azure/azure-dev/pull/6326) Fix correlation ID propagation and improve tracing for API calls
- [[#6343]](https://github.com/Azure/azure-dev/pull/6343) Improve `azd ai agent init` completion message to recommend `azd up` first
- [[#6344]](https://github.com/Azure/azure-dev/pull/6344) Rename `AI_FOUNDRY_PROJECT_APP_ID` environment variable to `AZURE_AI_PROJECT_PRINCIPAL_ID`
- [[#6366]](https://github.com/Azure/azure-dev/pull/6366) Fix manifest URL path when branch name contains "/"

## 0.1.3-preview (2025-12-03)

- Improve agent service debug logging via `AZD_EXT_DEBUG` env var and `--debug` flag

## 0.1.2-preview (2025-11-20)

- Update extension name and descriptions
- Update user facing text to use Microsoft Foundry

## 0.1.1-preview (2025-11-17)

- Fix min and max replicas not being set during agent deployment
- Fix `azd show` not displaying agent endpoint
- Polish user prompts and messages

## 0.1.0-preview (2025-11-14)

- Apply defaults instead of prompting in event handlers
- Process model resources as parameters
- Update env var generation to support multi-agent projects
- Polish error messages
- Improve local manifest handling
- Fix agent playground URL generation
- Fix panic when container settings is nil

## 0.0.7 (2025-11-13)

- Add prompting for container resources
- Add "preview" label to extension name and command descriptions
- Show agent playground URL post-deploy
- Support fetching ACR connections from existing AI Foundry projects
- Fix environment variable references
- Improve agent name validation

## 0.0.6 (2025-11-11)

- Add support for using existing AI model deployments
- Add `--project-id` flag for initializing using existing AI Foundry projects
- Fix agent definition handling for saved templates

## 0.0.5 (2025-11-06)

- Add support for tools
- Improve defaulting logic and --no-prompt support
- Fix remote build support

## 0.0.4 (2025-11-05)

- Add support for --no-prompt and --environment flags in `azd ai agent init`
- Include operation ID in timeout error
- Fix env vars not being included in agent create request

## 0.0.3 (2025-11-04)

- Add support for latest MAML format
- Fix agent endpoint handling for prompt agents

## 0.0.2 (2025-10-31)

- Add --host flag to `azd ai agent init`
- Rename host type to `azure.ai.agent`
- Store model information in service config
- Display agent endpoint on successful deploy
- Improve error handling
- Fix panic when no default model capacity is returned

## 0.0.1 (2025-10-28)

- Initial release
