---
short: Mental model for Foundry connections (declarative vs. imperative, how they wire to tools).
order: 10
---
# Connection overview

A Foundry **connection** is a project-level resource holding the endpoint URL + auth credentials for an external service (MCP server, OpenAPI backend, Azure AI Search index, Bing account, A2A peer, ACR, AI service). Connections live on the Foundry project, not on the agent. Tools reference them by `project_connection_id` (or `connectionName`). At call time, the Foundry runtime injects the credentials -- the agent code never sees the secret.

For step-by-step recipes, see `add`. For the imperative CLI, see `manage`.

## Shape

| Field          | Required? | What it is                                                                |
| -------------- | --------- | ------------------------------------------------------------------------- |
| `name`         | yes       | Unique within the project. Referenced as `project_connection_id`.          |
| `category`     | yes       | What kind of thing it points at (`RemoteTool`, `CognitiveSearch`, ...). See `categories`. |
| `target`       | usually   | URL or ARM resource ID.                                                    |
| `authType`     | yes       | How the runtime authenticates (`ApiKey`, `CustomKeys`, `OAuth2`, `UserEntraToken`, `AgenticIdentity`, `ProjectManagedIdentity`, `None`). See `auth-types`. |
| `credentials`  | varies    | Shape depends on `authType`.                                               |
| `metadata`     | optional  | Category-specific (e.g. `indexName` for CognitiveSearch).                  |
| `audience`     | when needed | Token audience for `UserEntraToken` / `AgenticIdentity` / some `ProjectManagedIdentity`. |
| OAuth2 fields  | OAuth2    | `authorizationUrl`, `tokenUrl`, `refreshUrl`, `scopes`, `connectorName`.   |

## Three ways a connection exists

| Path                          | Where defined                                               | When to use                                                |
| ----------------------------- | ----------------------------------------------------------- | ---------------------------------------------------------- |
| **Declarative (via azd)**     | `azure.yaml services.<name>.config.connections[]`           | Project-owned, reproducible across environments. Default. Init scaffolds this from manifest `kind: connection`. |
| **Pre-existing on Foundry**   | Nowhere local -- created out-of-band (portal, another team) | Shared connections owned elsewhere. Reference by name only. |
| **Imperative (`connection create`)** | Nowhere local -- created directly on Foundry         | One-off dev connections; quick experiments. See `manage`.  |

They coexist. A toolbox tool referencing `project_connection_id: my-conn` doesn't care which path created `my-conn`; it just needs the name to resolve.

Caveat: imperative connections survive `azd down` and are NOT re-created by `azd provision`. Nuke the env and you re-issue them. Declarative wins for reproducibility.

## How connections wire to tools

A connection on its own does nothing. A tool activates it. Three patterns:

**Toolbox tool with `project_connection_id`** (modern, recommended -- used for `mcp`, `openapi`, `a2a_preview`, and for `azure_ai_search` / `bing_grounding` inside a toolbox):

```yaml
connections:
  - name: github-mcp-conn
    category: RemoteTool
    target: https://api.githubcopilot.com/mcp
    authType: CustomKeys
    credentials:
      keys:
        Authorization: ${PARAM_GITHUB_MCP_CONN_KEYS_AUTHORIZATION}
toolboxes:
  - name: agent-tools
    tools:
      - type: mcp
        server_label: github
        project_connection_id: github-mcp-conn   # wires it in
```

**`resources[]` with `connectionName`** (legacy direct-binding for built-in tools that need a connection -- `bing_grounding` and `azure_ai_search` at the agent's top level):

```yaml
connections:
  - name: my-search-conn
    category: CognitiveSearch
    target: https://my-search.search.windows.net/
    authType: ApiKey
    credentials:
      key: ${PARAM_MY_SEARCH_CONN_KEY}
    metadata:
      indexName: docs-corpus
resources:
  - resource: azure_ai_search
    connectionName: my-search-conn   # wires it in
```

**No connection** (built-in tools that don't need one): `web_search`, `code_interpreter`, `file_search`, `function`. Bare `type:` entries in a toolbox.

```yaml
toolboxes:
  - name: misc-tools
    tools:
      - type: web_search
      - type: code_interpreter
```

## Credentials never live in `azure.yaml`

Every string leaf in a connection's `credentials:` map is externalized at init time to a `PARAM_<CONN>_<KEY>` env var; `azure.yaml` only holds `${PARAM_...}` references. When adding a connection manually, do the same:

1. Write `azure.yaml` with `${PARAM_<NAME>}` placeholders.
2. `azd env set PARAM_<NAME> <value>` for each secret.

The full rule (nested-map handling, the `credentials.type` -> `authType` promotion, expected shape per auth type) is in `auth-types`.

## Where to go next

* "What category do I use for X?" -- `categories`
* "How do I structure credentials for this auth type?" -- `auth-types`
* "Step-by-step for GitHub MCP / Azure AI Search / Bing / OpenAPI / A2A" -- `add`
* "I just want the CLI for create / update / delete" -- `manage`
* "How do connection blocks fit in azure.yaml overall?" -- `azd ai doc agent configure`
* "How do I bundle the tools that use this connection?" -- `azd ai doc toolbox`
