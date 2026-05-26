---
short: Recipes for adding common connections (MCP, Azure AI Search, Bing, OpenAPI, A2A).
order: 20
---
# Connection add: recipes

Pick the recipe matching the user's intent. Each shows the manifest fragment (init-time input), the resulting `azure.yaml` block (what you edit post-init), and any env vars you need to set.

For the mental model, read `overview` first. For category and auth reference, see `categories` and `auth-types`.

## Apply pattern

After applying any recipe:

```bash
azd provision     # Bicep creates / updates the connection on Foundry
azd deploy        # publishes / updates the toolbox referencing it
azd ai agent invoke "..."     # smoke test
```

## GitHub MCP with a Personal Access Token (CustomKeys)

"Add GitHub MCP using my PAT for auth."

Manifest:

```yaml
parameters:
  github_pat:
    secret: true
    description: GitHub PAT (ghp_... or github_pat_...)

resources:
  - kind: connection
    name: github-mcp-conn
    target: https://api.githubcopilot.com/mcp
    category: RemoteTool
    credentials:
      type: CustomKeys
      keys:
        Authorization: "Bearer {{ github_pat }}"
  - kind: toolbox
    name: agent-tools
    tools:
      - type: mcp
        server_label: github
        server_url: https://api.githubcopilot.com/mcp
        project_connection_id: github-mcp-conn
```

azure.yaml:

```yaml
services:
  my-agent:
    config:
      connections:
        - name: github-mcp-conn
          category: RemoteTool
          target: https://api.githubcopilot.com/mcp
          authType: CustomKeys      # promoted from credentials.type
          credentials:
            keys:
              Authorization: ${PARAM_GITHUB_MCP_CONN_KEYS_AUTHORIZATION}
      toolboxes:
        - name: agent-tools
          tools:
            - type: mcp
              server_label: github
              server_url: https://api.githubcopilot.com/mcp
              project_connection_id: github-mcp-conn
```

Env vars:

```bash
azd env set PARAM_GITHUB_MCP_CONN_KEYS_AUTHORIZATION "Bearer ghp_xxx..."
```

## GitHub MCP via Foundry-managed OAuth2

"Add GitHub MCP without handing me PATs -- let Microsoft manage the OAuth app."

Manifest:

```yaml
resources:
  - kind: connection
    name: github-oauth-conn
    category: RemoteTool
    authType: OAuth2
    target: https://api.githubcopilot.com/mcp
    connectorName: foundrygithubmcp     # Microsoft-managed OAuth app
    credentials:
      type: OAuth2
  - kind: toolbox
    name: agent-tools
    tools:
      - type: mcp
        server_label: github
        project_connection_id: github-oauth-conn
```

azure.yaml:

```yaml
services:
  my-agent:
    config:
      connections:
        - name: github-oauth-conn
          category: RemoteTool
          target: https://api.githubcopilot.com/mcp
          authType: OAuth2
          connectorName: foundrygithubmcp
      toolboxes:
        - name: agent-tools
          tools:
            - type: mcp
              server_label: github
              project_connection_id: github-oauth-conn
```

No env vars -- the Foundry platform handles client credentials. End users consent on first call.

## MCP with the end-user's Entra token (UserEntraToken)

"The agent should act as the user (1P OBO flow)."

Manifest:

```yaml
resources:
  - kind: connection
    name: workiq-mail-conn
    category: RemoteTool
    authType: UserEntraToken
    audience: ea9ffc3e-8a23-4a7d-836d-234d7c7565c1    # required: MCP server's app ID
    target: https://agent365.svc.cloud.microsoft/agents/servers/mcp_MailTools
  - kind: toolbox
    name: agent-tools
    tools:
      - type: mcp
        server_label: workiq-mail
        project_connection_id: workiq-mail-conn
```

azure.yaml: same shape as the manifest -- copy across.

No env vars. Token comes from the calling user at runtime. The Foundry project needs the right Entra app registrations and role assignments for OBO to succeed.

## Azure AI Search RAG

"Ground my agent's answers in an Azure AI Search index."

Manifest:

```yaml
resources:
  - kind: connection
    name: my-search-conn
    category: CognitiveSearch
    target: https://my-search.search.windows.net/
    authType: ApiKey
    credentials:
      key: "{{ search_api_key }}"
    metadata:
      indexName: contoso-outdoors
  - kind: tool
    id: azure_ai_search
    name: search
```

azure.yaml:

```yaml
services:
  my-agent:
    config:
      connections:
        - name: my-search-conn
          category: CognitiveSearch
          target: https://my-search.search.windows.net/
          authType: ApiKey
          credentials:
            key: ${PARAM_MY_SEARCH_CONN_KEY}
          metadata:
            indexName: contoso-outdoors
      resources:
        - resource: azure_ai_search
          connectionName: my-search-conn
```

Env vars:

```bash
azd env set PARAM_MY_SEARCH_CONN_KEY "<search-admin-key>"
```

Alternative: `authType: AAD` (no key, no env var). Grant the agent's managed identity the `Search Index Data Reader` role on the search service. See `auth-types`.

## Bing grounding

"Add Bing grounding so the agent can cite real web sources."

Manifest:

```yaml
resources:
  - kind: connection
    name: bing-grounding-conn
    category: GroundingWithBingSearch
    target: https://api.bing.microsoft.com/
    authType: ApiKey
    credentials:
      key: "{{ bing_api_key }}"
  - kind: tool
    id: bing_grounding
    name: bing
```

azure.yaml:

```yaml
services:
  my-agent:
    config:
      connections:
        - name: bing-grounding-conn
          category: GroundingWithBingSearch
          target: https://api.bing.microsoft.com/
          authType: ApiKey
          credentials:
            key: ${PARAM_BING_GROUNDING_CONN_KEY}
      resources:
        - resource: bing_grounding
          connectionName: bing-grounding-conn
```

```bash
azd env set PARAM_BING_GROUNDING_CONN_KEY "<bing-search-resource-key>"
```

For plain "search the web" without Bing-grounding semantics, drop the connection and use the built-in `web_search` tool inside a toolbox -- it needs no connection.

## OpenAPI tool (ApiKey)

"Wire up my internal REST API as a tool."

Manifest:

```yaml
resources:
  - kind: connection
    name: contoso-api-conn
    category: ApiKey
    target: https://api.contoso.com
    authType: ApiKey
    credentials:
      key: "{{ contoso_api_key }}"
  - kind: toolbox
    name: agent-tools
    tools:
      - type: openapi
        project_connection_id: contoso-api-conn
        # OpenAPI spec lives in your agent source and gets uploaded at deploy time.
```

azure.yaml: same shape as the manifest. Externalize `key` to `${PARAM_CONTOSO_API_CONN_KEY}` and `azd env set` it.

## A2A (Agent-to-Agent) bridge

"Let my agent delegate to another deployed agent."

```yaml
resources:
  - kind: connection
    name: peer-agent-conn
    category: RemoteTool
    target: https://other-agent.foundry-account.westus2.azure.com/
    authType: ProjectManagedIdentity
    audience: https://ai.azure.com/.default
  - kind: toolbox
    name: agent-tools
    tools:
      - type: a2a_preview
        project_connection_id: peer-agent-conn
```

No env vars -- the project's managed identity calls the peer agent.

## Multiple connections in one toolbox

Toolboxes can mix any number of tools, built-in + custom, against different connections:

```yaml
services:
  my-agent:
    config:
      connections:
        - name: github-conn
          category: RemoteTool
          target: https://api.githubcopilot.com/mcp
          authType: CustomKeys
          credentials:
            keys:
              Authorization: ${PARAM_GITHUB_CONN_KEYS_AUTHORIZATION}
        - name: my-search-conn
          category: CognitiveSearch
          target: https://my-search.search.windows.net/
          authType: ApiKey
          credentials:
            key: ${PARAM_MY_SEARCH_CONN_KEY}
          metadata:
            indexName: contoso-outdoors
      toolboxes:
        - name: agent-tools
          description: GitHub MCP + AI Search + web search + code execution.
          tools:
            - type: mcp
              server_label: github
              project_connection_id: github-conn
            - type: azure_ai_search
              project_connection_id: my-search-conn
            - type: web_search
            - type: code_interpreter
```

Caveat: a toolbox can hold **at most one** built-in tool of each type without a `name`. To include two `web_search` instances (etc.), give each a unique `name`.

## Remove a connection

1. Remove the entry from `azure.yaml` `connections[]` (or `toolConnections[]`).
2. Remove any tool referencing it via `project_connection_id`; remove any `resources[]` entry with matching `connectionName`.
3. `azd env unset PARAM_<...>` for the credential env vars (optional but tidy).
4. `azd provision` -- Bicep removes the connection from Foundry.
5. `azd deploy` -- updates the toolbox.

If the connection was created imperatively, use `azd ai agent connection delete <name>` -- `azd provision` won't touch it.
