---
short: Recipes for adding toolboxes via azd ai toolbox (MCP, AI Search, A2A, Bing Custom Search).
order: 20
---
# Toolbox add: recipes

Each recipe walks through:

1. The **connection** the toolbox needs (created with `azd ai agent connection create`, or declaratively in `azure.yaml` + `azd provision`).
2. The optional **declarative shape** under `azure.yaml services.<name>.config.toolboxes[]` (what init scaffolds from a `kind: toolbox` resource in the seed manifest -- a record of intent; today the CLI step below is what actually materializes it on Foundry).
3. The **`azd ai toolbox` CLI** step that creates or updates the toolbox on Foundry.
4. The **agent env var** the running agent reads.

Prerequisite once:

```bash
azd extension install azure.ai.toolboxes
```

For the lifecycle, see `overview`. For per-category field reference, see `tools`. For connection setup, see `azd ai doc connection`.

## Post-init: adding a tool to an existing agent

When you're modifying an existing project to add a tool, do these three checks before applying any recipe below:

**1. Connection exists on the project.** `azd ai agent connection list --output json` should show the connection name you'll pass to the toolbox CLI. If not, create it first. Two paths:

* **Imperative (recommended for post-init, especially `--from-code` projects with no `infra/` folder):** `azd ai agent connection create <name> --kind <category> --target <url> --auth-type <type> ...` -- see `azd ai doc connection manage` for the full flag matrix.
* **Declarative:** add the connection to `azure.yaml services.<name>.config.connections[]` and run `azd provision`. This requires an `infra/` folder; it fails on code-first projects. See `azd ai doc connection add`.

**2. Toolbox exists or not?** `azd ai toolbox list --output json`.

* If the toolbox doesn't exist -> use `azd ai toolbox create <name> --from-file <path>`.
* If it does -> use `azd ai toolbox connection add <name> <connection>` (single tool) or `--from-file <path>` (multiple).

Each call publishes a new version that becomes the default.

**3. Agent env var.** After running the CLI, get the endpoint with `azd ai toolbox show <name>` and wire it into the agent:

```bash
ENDPOINT=$(azd ai toolbox show my-toolbox --output json | jq -r .endpoint)
azd env set TOOLBOX_MY_TOOLBOX_MCP_ENDPOINT "$ENDPOINT"
```

(Name rule: uppercase the toolbox name and collapse non-alphanumeric to `_`, then append `_MCP_ENDPOINT`. `my-toolbox` -> `TOOLBOX_MY_TOOLBOX_MCP_ENDPOINT`.)

If the agent's `agent.yaml` doesn't already reference the env var under `environment_variables[]`, add it:

```yaml
environment_variables:
  - name: TOOLBOX_MY_TOOLBOX_MCP_ENDPOINT
    value: ${TOOLBOX_MY_TOOLBOX_MCP_ENDPOINT}
```

Then `azd deploy` so the deployed agent picks up the new env var. `azd deploy` itself does NOT create the toolbox on Foundry -- you must have run `azd ai toolbox create` / `connection add` first.

## GitHub MCP via Personal Access Token

User intent: "Add GitHub MCP."

**Connection (imperative):**

```bash
azd ai agent connection create github-mcp-conn \
  --kind remote-tool \
  --target https://api.githubcopilot.com/mcp \
  --auth-type custom-keys \
  --custom-key Authorization="Bearer ghp_xxx..."
```

(For the declarative form in `azure.yaml`, see `azd ai doc connection add` -> GitHub MCP recipe.)

**Declarative shape (optional, for the record):**

```yaml
# azure.yaml services.<name>.config.toolboxes[]
toolboxes:
  - name: agent-tools
    description: "Toolbox with GitHub MCP."
    tools:
      - type: mcp
        project_connection_id: github-mcp-conn
```

**CLI -- create the toolbox:**

```bash
cat > tools.json <<EOF
{
  "description": "Toolbox with GitHub MCP.",
  "connections": [{ "name": "github-mcp-conn" }]
}
EOF

azd ai toolbox create agent-tools --from-file tools.json
```

Or add to an existing toolbox:

```bash
azd ai toolbox connection add agent-tools github-mcp-conn
```

**Wire the env var:**

```bash
ENDPOINT=$(azd ai toolbox show agent-tools --output json | jq -r .endpoint)
azd env set TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT "$ENDPOINT"
```

## Azure AI Search RAG

User intent: "Ground my agent's answers in an Azure AI Search index."

**Connection:**

```bash
azd ai agent connection create my-search-conn \
  --kind cognitive-search \
  --target https://my-search.search.windows.net/ \
  --auth-type api-key \
  --key "<search-admin-key>"
```

**Declarative shape:**

```yaml
toolboxes:
  - name: agent-tools
    tools:
      - type: azure_ai_search
        index_name: contoso-outdoors
        project_connection_id: my-search-conn
```

**CLI -- attach the connection with the required `--index`:**

```bash
azd ai toolbox connection add agent-tools my-search-conn --index contoso-outdoors
```

Or as part of an initial `toolbox create`:

```yaml
# tools.yaml
description: "AI Search RAG toolbox."
connections:
  - name: my-search-conn
    index: contoso-outdoors
```

```bash
azd ai toolbox create agent-tools --from-file tools.yaml
```

For multiple indexes against the same search service: add multiple entries with different `index` values. (The CLI surfaces each as a distinct toolbox tool.)

## Bing Custom Search

User intent: "Add a scoped Bing Custom Search instance."

**Connection (must pre-exist; create via Bicep or the portal -- the agent CLI doesn't accept `grounding-with-custom-search` today):**

```yaml
# azure.yaml services.<name>.config.connections[]
- name: bing-custom-conn
  category: GroundingWithCustomSearch
  authType: ApiKey
  target: ""
  credentials:
    key: ${PARAM_BING_CUSTOM_CONN_KEY}
  metadata:
    ResourceId: /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.Bing/accounts/<bing-account>
    type: bing_custom_search
```

```bash
azd env set PARAM_BING_CUSTOM_CONN_KEY "<bing-api-key>"
azd provision
```

> `azd provision` requires an `infra/` folder with Bicep / Terraform. Code-first projects scaffolded by `azd ai agent init --from-code` don't have one, and provision will fail with a Bicep error. For those projects, create the connection via the portal or Bicep deployed out-of-band; the agent CLI doesn't yet support creating `GroundingWithCustomSearch` connections.

**CLI -- attach with the required `--instance-name`:**

```bash
azd ai toolbox connection add agent-tools bing-custom-conn --instance-name docs-config
```

For plain web search (no custom Bing instance), the toolbox CLI can't help today -- `web_search` is a built-in tool that the toolbox CLI doesn't expose, and `azd ai toolbox create --from-file` rejects a file with zero connections, so a web_search-only toolbox is not creatable via this CLI. Workarounds: (a) bundle `web_search` alongside a real connection-backed tool through the SDK / REST API, or (b) call `WebSearchTool()` directly in your agent code outside of any toolbox.

## A2A peer agent

User intent: "Delegate to another deployed agent."

**Connection:**

```bash
azd ai agent connection create peer-agent-conn \
  --kind remote-a2a \
  --target https://other-agent.foundry-account.westus2.azure.com/ \
  --auth-type none
```

For an authenticated peer, use `--auth-type project-managed-identity --audience https://ai.azure.com/.default` instead.

**CLI:**

```bash
azd ai toolbox connection add agent-tools peer-agent-conn
```

## Multi-connection toolbox via `--from-file`

Bundle several connections in one new version:

```yaml
# tools.yaml
description: "GitHub MCP + AI Search + A2A peer."
connections:
  - name: github-mcp-conn
  - name: my-search-conn
    index: contoso-outdoors
  - name: peer-agent-conn
```

```bash
# Initial create
azd ai toolbox create agent-tools --from-file tools.yaml

# Or append all three to an existing toolbox in one new version
azd ai toolbox connection add agent-tools --from-file tools.yaml
```

`connection add --from-file` publishes ONE new version regardless of how many connections the file lists.

## Built-in tools that the toolbox CLI doesn't manage

The `azd ai toolbox` CLI only handles **connection-backed tools** (RemoteTool / CognitiveSearch / RemoteA2A / GroundingWithCustomSearch). These tools have no connection and are NOT addable via this CLI today:

* `web_search` (plain Bing grounding)
* `code_interpreter`
* `file_search`
* `function`
* `toolbox_search_preview`

Because `azd ai toolbox create --from-file` requires `connections[]` to be non-empty, a toolbox composed only of built-in tools cannot be created via the CLI -- you'd hit a "no connections" validation error. To include any built-in tool in a toolbox today, you must either bundle it alongside a real connection-backed tool through the Python / .NET / JavaScript SDK or POST directly to the REST API. The `azure.yaml services.<name>.config.toolboxes[].tools[]` block can still record built-ins as the declarative shape, but the azd CLI won't push them to Foundry.

## Remove a connection from a toolbox

```bash
azd ai toolbox connection remove agent-tools github-mcp-conn
```

Publishes a new default version without the tool. Refuses to leave the toolbox with zero tools -- delete the toolbox instead.

## Delete a toolbox or version

```bash
# Delete a single version
azd ai toolbox delete agent-tools --version v3

# Delete the entire toolbox (cascades)
azd ai toolbox delete agent-tools --force
```

`--force` skips the confirmation prompt; required for `--no-prompt` runs.

## Promote a version manually

The first version is auto-promoted. After that, `connection add` / `remove` auto-promote each new version. To pin a specific version:

```bash
azd ai toolbox update agent-tools --default-version v2
```

## Validate

```bash
azd ai toolbox list --output json
azd ai toolbox show agent-tools --output json
azd ai toolbox connection list agent-tools --output json
```

End-to-end smoke test:

```bash
azd deploy
azd ai agent invoke "list the tools you have access to"
```
