---
short: Edit azure.yaml service config (models, connections, toolboxes), patch endpoints, manage files and evals.
order: 20
---
# Configure: shape the agent before deploying

Operational surface around a deployed agent: `azure.yaml` service config, connection management, file uploads, endpoint patches, eval init. Every command here is idempotent or gated by a confirmation envelope -- safe to script.

## Two files

* `<service-dir>/agent.yaml` -- the flat agent definition. See `extend`.
* `azure.yaml services.<name>.config` -- service config (models, connections, toolboxes, etc.). **This topic.**

`azd deploy` reads `agent.yaml` and creates a new agent version. `azd provision` reads `config.deployments[]` and `config.connections[]` and applies them via Bicep.

## Service config (`azure.yaml services.<name>.config`)

```yaml
services:
  my-agent:
    project: ./src/my-agent
    host: ai.agent
    language: docker            # or "python" / "csharp" for code deploy
    docker:
      remoteBuild: true         # omit for code deploy
    config:
      startupCommand: "python -m main"
      container:
        resources:
          cpu: "0.5"
          memory: "1Gi"
      deployments:
        - name: AZURE_AI_MODEL_DEPLOYMENT_NAME   # azd env var the agent reads
          model:
            name: gpt-4.1-mini
            format: OpenAI
            version: "2024-04-09"
          sku:
            name: GlobalStandard
            capacity: 50
      connections:
        - name: github-mcp-conn
          category: RemoteTool
          target: https://api.githubcopilot.com/mcp
          authType: CustomKeys
          credentials:
            Authorization: ${PARAM_GITHUB_MCP_CONN_AUTHORIZATION}
      toolboxes:
        - name: agent-tools
          description: "MCP toolset bundling GitHub + web search."
          tools:
            - type: web_search
            - type: mcp
              server_label: github
              project_connection_id: github-mcp-conn
      toolConnections:
        # Auto-extracted from toolbox tools with target+authType. Same shape as connections[].
        - name: extra-mcp
          category: RemoteTool
          target: https://example.com/mcp
          authType: ApiKey
          credentials:
            key: ${PARAM_EXTRA_MCP_KEY}
      resources:
        # Built-in tools that need a named connection. Only bing_grounding and azure_ai_search are recognized here.
        - resource: azure_ai_search
          connectionName: my-search-conn
```

Per field:

* `startupCommand` -- command `azd ai agent run` uses locally. Auto-detected at init.
* `container.resources` -- container cpu/memory. Mirrors the tiers init offers.
* `deployments[]` -- model deployments to create via Bicep. `name` is the azd env var the deployed `agent.yaml` references.
* `connections[]` -- Foundry project connections to create. See `connection add` for the full shape.
* `toolboxes[]` -- reusable tool bundles. Each has `name`, `description`, and a `tools[]` array. See "Toolbox shape" below; for recipes and lifecycle deep-dive see `azd ai doc toolbox`.
* `toolConnections[]` -- same shape as `connections[]`. Init hoists these out of toolbox `tools[]` entries that had `target:` + `authType:`. For new manual edits, prefer `connections[]`.
* `resources[]` -- built-in tools needing a pre-existing connection name. Only `bing_grounding` and `azure_ai_search` belong here; everything else goes in a toolbox.

### Connection shape

See `connection add` for recipes and `connection auth-types` for credentials. Quick form:

```yaml
- name: <connection-name>            # tools reference this as project_connection_id
  category: <CategoryKind>           # ARM-canonical: RemoteTool, CognitiveSearch, ApiKey, OAuth2, ...
  target: <url-or-arm-id>
  authType: <AuthType>               # ApiKey | CustomKeys | OAuth2 | UserEntraToken | AgenticIdentity | ProjectManagedIdentity | None
  credentials:                       # shape depends on authType
    <key>: ${PARAM_<CONN>_<KEY>}     # always env-var references, never raw secrets
  metadata:
    <key>: <value>                   # e.g. indexName for CognitiveSearch
```

### Toolbox shape

A toolbox is a curated bundle of tools exposed as a single MCP-compatible endpoint -- the recommended grouping per the Foundry tool catalog.

```yaml
toolboxes:
  - name: my-toolbox
    description: ...
    tools:
      # Built-in (no connection): type + optional name
      - type: web_search
      - type: code_interpreter
      - type: file_search
      # Custom (external endpoint): type + project_connection_id pointing at a connections[] / toolConnections[] entry
      - type: mcp
        server_label: github
        project_connection_id: github-mcp-conn
      - type: openapi
        project_connection_id: my-api-conn
      - type: a2a_preview
        project_connection_id: my-agent-conn
```

Tool taxonomy:

| Category                  | Tool types                                                    | Connection?                          |
| ------------------------- | ------------------------------------------------------------- | ------------------------------------ |
| Built-in                  | `web_search`, `code_interpreter`, `file_search`, `function`   | No                                   |
| Built-in, needs connection| `bing_grounding`, `azure_ai_search`                           | Yes -- via `resources[]` or toolbox + `project_connection_id` |
| Custom                    | `mcp`, `openapi`, `a2a_preview`                               | Yes -- via `project_connection_id`   |

`mcp` here means your agent calls out to an MCP server. It is unrelated to `azd ai agent mcp start`, which exposes the CLI itself to IDEs over MCP.

## Manifest -> azure.yaml transform

`azd ai agent init -m <manifest>` splits the manifest's outer `resources[]` across `azure.yaml services.<name>.config.*`. Mirror this when editing manually:

| Manifest fragment                                          | Lands in                                                                                                |
| ---------------------------------------------------------- | ------------------------------------------------------------------------------------------------------- |
| `template.environment_variables[]`                         | `<service>/agent.yaml` `environment_variables[]` (NOT azure.yaml)                                       |
| `resources[]` `kind: model`                                | `deployments[]`                                                                                         |
| `resources[]` `kind: tool, id: bing_grounding` / `azure_ai_search` | `resources[] { resource, connectionName }` (init prompts for the connection name)              |
| `resources[]` `kind: toolbox`                              | `toolboxes[]`. External tools (with `target` + `authType`) get hoisted into `toolConnections[]`; the tool entry gets a `project_connection_id`. |
| `resources[]` `kind: connection`                           | `connections[]`                                                                                         |
| Any connection `credentials.<key>: <value>` (string leaf)  | `${PARAM_<CONN>_<KEY>}` in azure.yaml; raw value stored via `azd env set`. Nested maps preserve structure; only string leaves are externalized. |
| Connection with `credentials.type:` but no `authType:`     | `authType:` promoted to top-level before externalization.                                               |

Adding a connection post-init: edit `azure.yaml`, `azd env set PARAM_<...>`, then `azd provision && azd deploy`. See `connection add` for end-to-end recipes.

## Connection management

Three ways a connection exists:

1. **Declarative in `azure.yaml`** -- created at `azd provision` via Bicep. Recommended for project-owned connections.
2. **Pre-existing on Foundry** -- created out-of-band; reference by name only.
3. **Imperative via CLI** -- `azd ai agent connection create/update/delete`. Lives only on Foundry.

For mental model + recipes, see `azd ai doc connection overview` / `add`. For the CLI reference, see `connection manage`.

### Inspect

```bash
azd ai agent connection list --output json     # name, kind, authType, target
azd ai agent connection show <name> --output json   # full record (with credentials when allowed)
```

## File uploads

```bash
azd ai agent files upload ./data/input.csv
azd ai agent files upload ./input.csv --target-path /data/input.csv
azd ai agent files list --output json
```

Delete is gated by the confirmation envelope (see `operate`).

## Endpoint and card patches

When only `agentEndpoint` or `agentCard` changed in `agent.yaml`, skip the full redeploy:

```bash
azd ai agent endpoint update --dry-run     # preview
azd ai agent endpoint update --force       # apply
```

Exit 2 + JSON envelope means non-interactive mode needs `--force`. Show the `changes` to the developer and re-run.

## Eval init

`eval init` shapes the eval suite (generates `eval.yaml`, dataset, evaluator). End-to-end eval lifecycle lives in `evaluate`.

```bash
azd ai agent eval init --dry-run     # preview
azd ai agent eval init --force       # apply
azd ai agent eval show --output json
azd ai agent eval list --output json
```

Billed jobs -- gated by the confirmation envelope.

## State

| Variable                       | Read by                                                                |
| ------------------------------ | ---------------------------------------------------------------------- |
| `AZURE_AI_PROJECT_ENDPOINT`    | Every command that resolves the project endpoint.                      |
| `FOUNDRY_PROJECT_ENDPOINT`     | Host-shell fallback when no azd env value.                             |
| `AZURE_AI_PROJECT_ID`          | `show` for the playground URL.                                         |
| `AGENT_<SVC>_<PROTO>_ENDPOINT` | `show` / `invoke` for per-protocol deployed endpoints.                 |
| `AGENT_<SVC>_ENDPOINT`         | Legacy single-endpoint fallback.                                       |
| `PARAM_<CONN>_<KEY>`           | Connection credentials referenced from `azure.yaml`.                   |
| `AI_AGENT_PENDING_PROVISION`   | Internal next-step resolution.                                         |

Manage with `azd env get|set|list|new|select`.

## Common error codes

* `invalid_agent_manifest` -- `agent.yaml` is malformed. Run `azd ai agent doctor --output json` and check `local.agent-yaml-valid`.
* `invalid_connection` -- Foundry rejected a connection. Inspect with `azd ai agent connection show <name>`.
* `missing_connection_field` -- `connection update` needs `--target`, `--key`, or `--custom-key`.
* `invalid_agent_request` -- `endpoint update` patch was rejected. Re-read `agent.yaml`.

## Confirmation envelope

Every write here accepts `--dry-run` (prints envelope, exits 0) and `--force` (applies). Without `--force` in non-interactive mode, the command exits 2 with the envelope. See the SKILL.md envelope rules.
