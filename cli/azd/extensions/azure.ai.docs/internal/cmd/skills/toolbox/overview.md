---
short: What a toolbox is and how to create and manage one with the azd ai toolbox CLI.
order: 10
---
# Toolbox overview

A **toolbox** is a curated bundle of connection-backed tools that Foundry exposes as a single MCP-compatible endpoint. The agent connects to one URL and dynamically discovers every tool inside. Toolboxes are the recommended way to group multiple connection-backed tools -- one endpoint, central credential handling, no per-agent tool wiring.

Today, toolboxes are managed through the `azd ai toolbox` CLI (from the `azure.ai.toolboxes` extension). `azd deploy` does NOT auto-create toolboxes. You install the extension once, then drive the lifecycle explicitly.

For step-by-step recipes, see `add`. For the supported connection categories and their tool shapes, see `tools`. For agent-side runtime wiring, see `consume`.

## Install the extension

```bash
azd extension install azure.ai.toolboxes
```

Then `azd ai toolbox --help` to see the verbs.

## The CLI surface

| Command                                                                          | What it does                                                  |
| -------------------------------------------------------------------------------- | ------------------------------------------------------------- |
| `azd ai toolbox create <name> --from-file <path>`                                | Create a toolbox + publish its initial version. File must list at least one existing project connection. |
| `azd ai toolbox connection add <toolbox> <connection> [--index ...] [--instance-name ...]` | Attach a single connection; publishes a new default version. |
| `azd ai toolbox connection add <toolbox> --from-file <path>`                     | Attach many connections in one call; publishes ONE new version. |
| `azd ai toolbox connection remove <toolbox> <connection> [--force]`              | Detach a connection; publishes a new default version. Refuses to leave zero tools. |
| `azd ai toolbox connection list <toolbox>`                                       | List the connection-backed tools attached to a toolbox.       |
| `azd ai toolbox show <name> [--version <ver>]`                                   | Show the toolbox + its MCP endpoint URL. Defaults to the default version. |
| `azd ai toolbox list`                                                            | List toolboxes on the project.                                |
| `azd ai toolbox version list <toolbox>`                                          | List published versions for a toolbox.                        |
| `azd ai toolbox update <name> --default-version <ver>`                           | Re-point the default version (the only field `update` supports today). |
| `azd ai toolbox delete <name> [--version <ver>] [--force]`                       | Delete a whole toolbox, or a single version.                  |

Every mutation publishes a new immutable version. The first version of a new toolbox is automatically the default; subsequent versions require an explicit `update --default-version` to promote.

## Connections must already exist

The toolbox CLI **does not create connections**. It attaches connections that already exist on the Foundry project. Two ways to get a connection on the project before calling `azd ai toolbox`:

* **Imperative**: `azd ai agent connection create <name> --kind <category> --target ... --auth-type ... --key ...`. See `azd ai doc connection manage`.
* **Declarative**: add a `kind: connection` resource to the seed manifest (or to `azure.yaml services.<name>.config.connections[]` post-init) and run `azd provision`. See `azd ai doc connection add`.

Once a connection exists, list them with `azd ai agent connection list --output json` -- the `name` field is what you pass to `azd ai toolbox`.

## The two file shapes

Both `toolbox create --from-file` and `toolbox connection add --from-file` take the same connections list shape:

```yaml
description: research toolbox    # only accepted by `create`, not `connection add`
connections:
  - name: my-mcp                 # RemoteTool
  - name: my-search              # CognitiveSearch -- needs `index`
    index: products
  - name: my-bing                # GroundingWithCustomSearch -- needs `instance_name`
    instance_name: docs-config
  - name: my-a2a                 # RemoteA2A
```

The toolbox derives the tool kind from the connection's category. You don't write `type: mcp` or `type: azure_ai_search` yourself.

## Lifecycle at a glance

| Stage                | What happens                                                                                  |
| -------------------- | --------------------------------------------------------------------------------------------- |
| Create connections   | Imperative (`azd ai agent connection create`) or declarative (`azd provision` after editing `azure.yaml`). |
| `toolbox create`     | Publishes the initial version. Toolbox is created if it didn't exist. First version is the default. |
| `toolbox connection add` / `remove` | Each call publishes a new version and promotes it to default.                                  |
| Agent reads endpoint | Run `azd ai toolbox show <name>`; copy the `Endpoint` field; `azd env set TOOLBOX_<NAME>_MCP_ENDPOINT "<url>"`. The deployed agent reads the env var. |
| Subsequent updates   | Re-run `toolbox connection add` / `remove` -- the new version becomes the default automatically, so the same env var URL keeps serving the latest. (Or pin a specific version via `--version` on `show` and don't auto-promote.) |

## agent.yaml `kind: toolbox` -- the declarative shape

`agent.yaml` (the seed manifest passed to `azd ai agent init -m`) accepts a `kind: toolbox` resource. Init lands it in `azure.yaml services.<name>.config.toolboxes[]` as a structured record of which tools belong to the toolbox.

That block is the **declarative form** -- a record of intent. **Today, you also need to run the `azd ai toolbox` CLI** to create the toolbox on Foundry; the deploy pipeline does not yet read this block. See `add` for end-to-end recipes that include both the declarative shape and the CLI steps.

## Developer vs consumer endpoint

Foundry exposes two endpoint patterns:

| Endpoint                                                                  | When                                                       |
| ------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `{project}/toolboxes/{name}/versions/{version}/mcp?api-version=v1`        | Version-pinned (developer / version-specific).             |
| `{project}/toolboxes/{name}/mcp?api-version=v1`                           | Default version (consumer). Always serves `default_version`.|

`azd ai toolbox show` prints the version-pinned URL of the version you're viewing. For auto-pickup of new default versions in the running agent, manually compose the consumer URL (drop the `/versions/<ver>` segment) and set THAT as the env var.

## Required header

Every request to a toolbox MCP endpoint must include:

```http
Foundry-Features: Toolboxes=V1Preview
```

Your agent code MUST send it on every MCP call -- see `consume`.

## Where to go next

* "How do I add a toolbox with X / Y / Z tools?" -> `add`
* "What connection categories does the CLI accept and what fields does each need?" -> `tools`
* "How does my agent code call the toolbox at runtime?" -> `consume`
* "How do I create the connections the toolbox depends on?" -> `azd ai doc connection add`
