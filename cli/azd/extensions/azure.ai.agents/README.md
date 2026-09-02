# Azure Developer CLI (azd) Agents Extension

## Composing Agent Dependencies

Use the Agent command surface to attach existing Toolbox or Connection services
to an Agent service in `azure.yaml`:

```bash
azd ai agent add toolbox support-tools --agent research-agent
azd ai agent add connection search-connection --agent research-agent
```

These commands add the dependency service key to `services.<agent>.uses`. They
do not create or deploy the dependency. Toolbox and Connection configuration and
lifecycle behavior remain owned by the `azure.ai.toolboxes` and
`azure.ai.connections` extensions.

If a toolbox is declared inline on an agent, move its definition to an
independent `azure.ai.toolbox` service before deployment. If the new service key
differs from the original toolbox name (for example, `My Tools` becomes
`MyTools`), replace the inline entry in the agent's `toolboxes` list with the
new service key. Then attach that service with the command above and run `azd deploy`.
The add command only updates the agent's `uses` list; it does not rewrite
`toolboxes`, create the service, or deploy it.

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

## Migrating Legacy Agent Configuration

New Foundry agent projects keep the agent definition directly on the
`azure.ai.agent` service entry in `azure.yaml`. Older projects may still have the
definition in an `agent.yaml` file or under the service's `config:` block. Those
legacy shapes continue to work during the migration window, but azd prints a
deprecation warning when it loads them.

To migrate, re-run `azd ai agent init` from the project root and keep the
generated `azure.yaml` service entry. After confirming `azd deploy` still works,
remove the old `agent.yaml` or nested `config:` definition.

Before:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    config:
      kind: hosted
      name: my-agent
      description: My hosted agent
```

After:

```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: .
    kind: hosted
    name: my-agent
    description: My hosted agent
```

### Environment variables under `config:`

Older projects could also set environment variables in an `env:` block nested
under the service's `config:`. That position is no longer read: azd takes the
service environment only from the service-level `env:`. A service that still
carries `config: env:` gets a warning naming the affected variables on both
`azd ai agent run` and `azd deploy`.

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

## Content safety policies

A hosted agent can be bound to an Azure AI Content Safety (RAI) policy so every
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

`policies` applies to both deploy modes — container images and code deploys
(`codeConfiguration`) alike. It is optional; agents without it deploy exactly as
before.

Details:

- `type` is required. `rai_policy` is currently the only supported value.
- `raiPolicyName` is required for `rai_policy` and takes the **full ARM resource
  ID** of the policy, not its short name. Built-in policies such as
  `Microsoft.DefaultV2` still need the full ID, with the account that hosts them
  in the path.
- Create or list policies on the Foundry account first — azd does not create the
  policy, it only associates the agent with an existing one.

> **Note:** In the deprecated on-disk `agent.yaml` shape the key is snake_case
> (`rai_policy_name`). In `azure.yaml` it is camelCase (`raiPolicyName`), like
> the other inline agent properties such as `codeConfiguration` and
> `environmentVariables`.

### Moderating invocations-protocol traffic

For agents that expose the `invocations` protocol, the RAI policy alone is not
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
              textField: $.delta
```

Fields:

| Field | Required | Description |
| --- | --- | --- |
| `responseMode` | yes | `non_streaming`, `streaming`, or `both`. |
| `inputContentType` | no | `json` (default) or `text`. |
| `outputContentType` | no | `json` (default) or `text`. |
| `inputPaths` | when `inputContentType` is `json` or omitted (it defaults to `json`) | JSONPath expressions selecting the request text. |
| `outputPaths` | when `responseMode` includes non-streaming and `outputContentType` is `json` or omitted (it defaults to `json`) | JSONPath expressions selecting the buffered response text. |
| `streamSelectors` | when `responseMode` includes streaming and `outputContentType` is `json` or omitted (it defaults to `json`) | `eventType` (required) and `textField` per server-sent event frame. |

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

As with `raiPolicyName`, the deprecated on-disk `agent.yaml` shape uses snake_case
keys throughout this block (`invocations_moderation`, `response_mode`,
`input_paths`, `stream_selectors`, `event_type`, and so on). The **values**
(`non_streaming`, `streaming`, `both`, `json`, `text`) are the same in both.

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

- `idleTimeoutSeconds` must be between **300 and 3600** seconds (inclusive).
  Values outside that range are rejected at deploy time and by schema
  validation.
- In the deprecated on-disk `agent.yaml` shape the keys are snake_case
  (`session_configuration.idle_timeout_seconds`). In `azure.yaml` they are
  camelCase, like the other inline agent properties.

## Session carry-over across deploys

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

To let `azd provision` create the connection, declare an
`azure.ai.connection` sibling. For this declarative path, both the agent's
`registryConnectionId` and `uses` identify the sibling's azure.yaml service key,
which is also the Foundry connection name provisioned by the current Projects
extension:

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

Set the referenced credential environment values, run `azd provision`, and then
run `azd deploy`. Omitting the sibling from `uses`, disabling it with a deployment
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
