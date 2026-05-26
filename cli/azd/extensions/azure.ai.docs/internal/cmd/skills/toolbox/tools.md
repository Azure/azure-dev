---
short: Connection categories and tool types the toolbox CLI accepts.
order: 30
---
# Toolbox tools reference

The `azd ai toolbox` CLI is **connection-centric**: every tool in a toolbox is derived from a project connection's category. You pass connection names + a couple of per-category flags; the toolbox derives the tool kind.

For recipes, see `add`. For connection setup, see `azd ai doc connection`.

## Connection categories accepted

| Connection `category`         | Produces toolbox tool of `type` | Required extra field                          | Set via                                  |
| ----------------------------- | ------------------------------- | --------------------------------------------- | ---------------------------------------- |
| `RemoteTool`                  | `mcp`                           | (none)                                        | -                                        |
| `CognitiveSearch`             | `azure_ai_search`               | Search index name                             | `--index <name>` on `connection add`, or `index:` in `--from-file` |
| `RemoteA2A`                   | `a2a_preview`                   | (none)                                        | -                                        |
| `GroundingWithCustomSearch`   | `bing_custom_search`            | Bing custom-search instance name              | `--instance-name <name>` on `connection add`, or `instance_name:` in `--from-file` |

The CLI rejects connections in any other category. To use a category outside this list, fall back to the SDK or REST API.

## File shape (`--from-file`)

```yaml
description: research toolbox       # only on `create`; `connection add` keeps the existing description
connections:
  - name: my-mcp                    # RemoteTool
  - name: my-search                 # CognitiveSearch
    index: products
  - name: my-bing                   # GroundingWithCustomSearch
    instance_name: docs-config
  - name: my-a2a                    # RemoteA2A
```

JSON form has the same field names. Unknown fields are rejected. At least one connection is required.

Project connections must already exist on the Foundry project; the toolbox CLI does NOT create connections. Use `azd ai agent connection list --output json` to enumerate what's available.

## What the tool entry looks like on Foundry

The CLI assembles each connection into a JSON tool entry sent to `POST /toolboxes/<name>/versions?api-version=v1`. You don't author these directly; they're useful to know for debugging (`azd ai toolbox show --output json` returns them).

```jsonc
// RemoteTool -> mcp
{ "type": "mcp", "server_label": "my-mcp", "project_connection_id": "<ARM-id-of-my-mcp>" }

// CognitiveSearch -> azure_ai_search
{
  "type": "azure_ai_search",
  "name": "my-search",
  "azure_ai_search": {
    "indexes": [{ "index_name": "products", "project_connection_id": "<ARM-id-of-my-search>" }]
  }
}

// RemoteA2A -> a2a_preview
{ "type": "a2a_preview", "project_connection_id": "<ARM-id-of-my-a2a>" }

// GroundingWithCustomSearch -> bing_custom_search
{
  "type": "bing_custom_search",
  "custom_search_configuration": { "instance_name": "docs-config" },
  "project_connection_id": "<ARM-id-of-my-bing>"
}
```

`server_url`, `server_label`, `require_approval`, and similar tool-level fields come from the connection's record, not from CLI flags. If you need to override them, set them on the connection itself (target URL, metadata) before attaching it.

## Tools the toolbox CLI does NOT manage today

These tool types are valid in the Foundry toolbox API but the `azd ai toolbox` CLI doesn't expose them (no connection backs them):

| Tool type                  | What it is                                                            |
| -------------------------- | --------------------------------------------------------------------- |
| `web_search`               | Plain Bing web search (no custom instance). Built-in.                 |
| `code_interpreter`         | Sandboxed Python execution. Built-in.                                 |
| `file_search`              | Vector-search over a pre-created vector store. Built-in.              |
| `function`                 | Local function tool with a JSON-schema parameters object. Built-in.   |
| `toolbox_search_preview`   | Intent-based routing directive. Built-in.                             |

To include any of these in a toolbox, create the version through the Python / .NET / JavaScript SDK or POST directly to `{project}/toolboxes/<name>/versions?api-version=v1` with the `Foundry-Features: Toolboxes=V1Preview` header. The `azure.yaml services.<name>.config.toolboxes[].tools[]` block can still list them as the declarative shape -- but the azd CLI won't push them.

## Universal optional fields

| Field         | What it does                                                                                                                                |
| ------------- | ------------------------------------------------------------------------------------------------------------------------------------------- |
| `name`        | Unique tool name inside the toolbox. The CLI defaults to the connection's short name; override only if you need to disambiguate manually.    |
| `description` | Set at toolbox-create time via the `description:` field in the `--from-file` payload. The MODEL reads this to pick between tools at runtime. |

A toolbox supports at most ONE tool of a given type without a `name`. The CLI sets `name` to the connection's short name for you, so collisions inside a single connection-add are rare. If you attach two `CognitiveSearch` connections with the same connection name (unlikely) or two tools that produce the same `type` + default name, set unique `name` values via the SDK -- the CLI doesn't expose `name` as a flag today.

## Validation and lifecycle commands

| Command                                                                  | Use for                                                       |
| ------------------------------------------------------------------------ | ------------------------------------------------------------- |
| `azd ai toolbox list --output json`                                      | Which toolboxes exist on the project.                         |
| `azd ai toolbox show <name> [--version <ver>]`                           | Full tool list + the MCP endpoint URL for the chosen version. |
| `azd ai toolbox connection list <toolbox> --output json`                 | Connection-by-connection view of what's attached.             |
| `azd ai toolbox version list <toolbox> --output json`                    | Every published version.                                      |
| `azd ai toolbox update <name> --default-version <ver>`                   | Pin the default version (otherwise every mutation auto-promotes). |
| `azd ai toolbox delete <name> [--version <ver>] [--force]`               | Remove a whole toolbox or a single version.                   |

## Reference

* Toolbox tool catalog (Foundry): https://learn.microsoft.com/en-us/azure/foundry/agents/concepts/tool-catalog
* Toolbox how-to: https://learn.microsoft.com/en-us/azure/foundry/agents/how-to/tools/toolbox
