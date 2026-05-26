---
short: How agent code consumes the toolbox MCP endpoint at runtime.
order: 40
---
# Toolbox consume: agent-side runtime wiring

How the running agent reaches its toolbox: the env var convention (which you set yourself today), the header every call must include, the MCP client gotchas, and per-runtime patterns.

For the toolbox-side definition (creating one with `azd ai toolbox`), see `add`. For lifecycle, see `overview`.

## The env var convention

The agent reads the toolbox URL from an env var. The convention is `TOOLBOX_<NAME>_MCP_ENDPOINT`, where `<NAME>` is the toolbox name uppercased with non-alphanumeric characters collapsed to `_`.

| Toolbox name      | Convention env var                    |
| ----------------- | ------------------------------------- |
| `agent-tools`     | `TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT`    |
| `my-toolbox`      | `TOOLBOX_MY_TOOLBOX_MCP_ENDPOINT`     |
| `agent.tools.v2`  | `TOOLBOX_AGENT_TOOLS_V2_MCP_ENDPOINT` |
| `Web-Search:V2`   | `TOOLBOX_WEB_SEARCH_V2_MCP_ENDPOINT`  |

`azd ai toolbox` does NOT auto-populate this env var today. You do it yourself after running `azd ai toolbox show`:

```bash
ENDPOINT=$(azd ai toolbox show agent-tools --output json | jq -r .endpoint)
azd env set TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT "$ENDPOINT"
```

Also add a reference in the agent's on-disk `agent.yaml` so the deployed container reads it:

```yaml
# <service>/agent.yaml under environment_variables
environment_variables:
  - name: TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT
    value: ${TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT}
```

Then `azd deploy` so the deployed agent picks up the new env var.

## Endpoint URL shapes

| Pattern                                                                   | When                                                       |
| ------------------------------------------------------------------------- | ---------------------------------------------------------- |
| `{project}/toolboxes/{name}/versions/{version}/mcp?api-version=v1`        | Version-pinned. What `azd ai toolbox show` returns.        |
| `{project}/toolboxes/{name}/mcp?api-version=v1`                           | Default version (consumer). Always serves `default_version`.|

`azd ai toolbox show` prints the version-pinned URL of the version you're viewing. If you want the agent to auto-pick up new default versions without redeploying, set the env var to the consumer URL instead (drop the `/versions/<ver>` segment).

## Required header

```http
Foundry-Features: Toolboxes=V1Preview
```

Every MCP request to the toolbox endpoint must include this header. Without it the call fails.

## Token scope

When acquiring a bearer token for the toolbox endpoint:

```
https://ai.azure.com/.default
```

## MCP client gotchas

Foundry's toolbox MCP endpoint has a couple of quirks. With a generic MCP client, set these or the connection won't work:

* **Always stream.** Non-streaming mode is NOT supported. Use the streamable HTTP transport.
* **Don't call `prompts/list`.** Foundry's server doesn't implement it; the call returns `500`. Many MCP clients call it automatically at startup -- pass `load_prompts=False` (or the equivalent option) to disable.
* **Generic clients: don't call `send_ping()`.** Same reason. Microsoft Agent Framework's `MCPStreamableHTTPTool._ensure_connected()` already catches the failure and sets `_ping_available = False` on its own, so Agent Framework users don't need to do anything. Generic MCP clients that hard-fail on ping need an override.
* **MCP tool names are prefixed with `server_label`.** A tool `get_info` on `server_label: myserver` is exposed as `myserver.get_info`. GitHub Copilot SDK rejects dots in tool names -- the bridge must map `myserver.get_info` <-> `myserver_get_info`.
* **Approval gating is the client's job, not the proxy's.** Many MCP servers (GitHub MCP, others) declare `require_approval: always` on every tool. The Foundry toolbox proxy does NOT enforce this -- it forwards `tools/call` unconditionally -- but Agent Framework's `MCPStreamableHTTPTool` defaults to "require approval", which silently blocks tool calls (empty response, no error). Either pass `approval_mode="never_require"` to allow auto-invocation, or wire up an approval handler. See **Handling `require_approval`** below.

## Two consumption patterns

### Server-side (Foundry runs the tools)

Your agent uses the Foundry SDK's Responses or Invocations API, includes the toolbox endpoint in its tool list, and Foundry executes tool calls server-side. The agent code never opens an MCP client itself.

Use when: writing a hosted agent against the Foundry Responses API, or you want the platform to handle auth, retries, and observability.

### Client-side (the agent code calls MCP directly)

Your agent reads `TOOLBOX_<NAME>_MCP_ENDPOINT`, opens an MCP session, lists the tools, and includes them in its own tool-calling loop (LangGraph, LangChain, Agent Framework, GitHub Copilot SDK, custom code).

Use when: bringing your own runtime, or you want fine-grained control over tool invocation, approval policies, or post-processing.

## Minimal client-side example (Python)

```python
import os
import asyncio
from azure.identity import DefaultAzureCredential
from mcp.client.streamable_http import streamablehttp_client
from mcp import ClientSession

async def main():
    url = os.environ["TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT"]
    token = DefaultAzureCredential().get_token("https://ai.azure.com/.default").token
    headers = {
        "Authorization": f"Bearer {token}",
        "Foundry-Features": "Toolboxes=V1Preview",
    }
    async with streamablehttp_client(url, headers=headers) as (read, write, _):
        async with ClientSession(read, write) as session:
            await session.initialize()
            tools = (await session.list_tools()).tools
            print(f"Tools found: {len(tools)}")
            for t in tools:
                print(f"  - {t.name}: {(t.description or '')[:80]}")
            # result = await session.call_tool("<tool_name>", arguments={...})

asyncio.run(main())
```

Install: `pip install mcp azure-identity`.

## Required RBAC

The calling identity needs the **Foundry User** role on the Foundry project. Three identities matter:

* **Developer** -- the human / pipeline that runs `azd ai toolbox` commands.
* **Agent identity** -- the hosted agent's managed identity that calls tools at runtime.
* **End user** -- only when `UserEntraToken` or OAuth connections are involved (the user's identity is proxied through).

## Tool call argument shapes

A small per-tool-type reference. Argument names are easy to get wrong:

| Tool type        | `tools/call` arguments                                                 |
| ---------------- | ---------------------------------------------------------------------- |
| Azure AI Search  | `{"query": "search text"}`                                             |
| A2A              | `{"message": {"parts": [{"type": "text", "text": "Hello"}]}}`          |
| MCP              | Whatever the underlying MCP tool's `inputSchema.properties` defines.   |
| Bing Custom Search | `{"search_query": "..."}` (same as `web_search`).                    |

Inspect each tool's `inputSchema` (returned by `tools/list`) to confirm the exact parameter names.

## Handling `require_approval` (MCP)

For MCP tools, each `tools/list` entry includes `_meta.tool_configuration.require_approval`. Values:

* `"always"` -- the agent runtime must prompt the user for confirmation before EVERY invocation. Many servers (GitHub MCP and others) default to this for every tool.
* `"never"` -- the agent can invoke freely.

The toolbox MCP proxy does NOT enforce this -- it always executes `tools/call`. Gating is your agent runtime's responsibility. Build an approval map at startup from `tools/list` and check it before each call.

**Agent Framework users:** `MCPStreamableHTTPTool` defaults to requiring approval, and when no handler is wired up the runtime silently drops the call (empty response, no error). Pass `approval_mode="never_require"` to auto-allow.

**Do NOT pass `headers=` with a static bearer token.** Tokens acquired once at startup expire after ~1 hour. Long-running agent processes will start returning `401` mid-session. Use an `httpx.AsyncClient` with a `request` event hook so a fresh token is acquired before every MCP call:

```python
import os
import httpx
from azure.identity import DefaultAzureCredential
from agent_framework.tools.mcp import MCPStreamableHTTPTool

_credential = DefaultAzureCredential()

def _inject_auth(request: httpx.Request) -> None:
    # Called before every MCP request -- always fresh, never expired.
    token = _credential.get_token("https://ai.azure.com/.default").token
    request.headers["Authorization"] = f"Bearer {token}"
    request.headers["Foundry-Features"] = "Toolboxes=V1Preview"

tool = MCPStreamableHTTPTool(
    name="github",            # sets the server_label prefix on tool names
    url=os.environ["TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT"],
    httpx_client=httpx.AsyncClient(event_hooks={"request": [_inject_auth]}),
    load_prompts=False,           # Foundry does not implement prompts/list
    approval_mode="never_require",  # GitHub MCP marks every tool require_approval:always
)
```

Install: `pip install httpx azure-identity`.

For human-in-the-loop approval, wire a custom handler instead of `"never_require"`. The `name=` parameter becomes the MCP server label prefix (e.g. `name="github"` -> tools appear as `github.list_repos` etc.).

## Verifying the connection

```bash
TOKEN=$(az account get-access-token --resource https://ai.azure.com --query accessToken -o tsv)
curl -sS "$TOOLBOX_AGENT_TOOLS_MCP_ENDPOINT" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Foundry-Features: Toolboxes=V1Preview" \
  -H "Accept: application/json, text/event-stream" \
  -H "Content-Type: application/json" \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

A `200` with a JSON-RPC body listing the tools means the wire is intact. Each tool has `name`, `description`, `inputSchema`, and (for MCP) a `_meta.tool_configuration.require_approval` field.

## Troubleshooting

| Symptom                                                | Likely cause                                                                                |
| ------------------------------------------------------ | ------------------------------------------------------------------------------------------- |
| `TOOLBOX_<NAME>_MCP_ENDPOINT` not set                  | Never ran `azd ai toolbox show` + `azd env set`. Run them, then `azd deploy`.               |
| Env var not visible to deployed agent                  | `<service>/agent.yaml` is missing the `environment_variables[]` entry. Add it + `azd deploy`. |
| `400` with `Toolboxes` in the message                  | Missing `Foundry-Features: Toolboxes=V1Preview` header.                                     |
| `401` on MCP calls                                     | Expired token or wrong scope. Use `https://ai.azure.com/.default`. For Agent Framework, use an httpx event hook for per-request token refresh instead of a static token. |
| `403 Forbidden`                                        | Caller missing `Foundry User` role; or for `UserEntraToken`, the user lacks rights on the downstream service. |
| `404` on the version-pinned URL                        | Version was deleted. Re-run `azd ai toolbox show` to refresh, or switch to the consumer URL. |
| `500` on `prompts/list`                                | Foundry's MCP server doesn't implement it. Pass `load_prompts=False` to your MCP client.    |
| `500` on `send_ping()` (generic MCP client)            | Same -- disable the ping. Agent Framework already handles this; only an issue for clients that hard-fail on ping. |
| `500` with non-streaming `tools/call`                  | Non-streaming not supported. Use `stream=True` / streamable HTTP transport.                 |
| Empty response, no error, agent never calls the tool   | Likely a `require_approval: always` tool with no approval handler wired up. Pass `approval_mode="never_require"` to Agent Framework's `MCPStreamableHTTPTool`, or wire an approval handler. |
| `500` on `tools/list`                                  | Transient. Retry after a few seconds.                                                       |
| `CONSENT_REQUIRED` (`-32007`)                          | OAuth connection needs user consent. Open the URL from `error.message`; retry afterwards.   |
| `tools/list` returns zero tools                        | The connection backing the tool has invalid credentials, or the toolbox version is still provisioning. Verify with `azd ai agent connection list --output json` and `azd ai toolbox show`. |
| Tool names don't match what the model called           | MCP tool names are prefixed with `server_label.`. Use `{server_label}.{tool_name}`.        |
| Custom env vars overwritten                            | The platform reserves the `FOUNDRY_` prefix. Don't use it for your own values.              |
