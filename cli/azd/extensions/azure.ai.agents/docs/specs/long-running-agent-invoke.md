# Long-running agent invocation primitives

## Status

- **PRD:** [GitHub issue #9676](https://github.com/Azure/azure-dev/issues/9676)
- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Feature status:** Preview

## Goal

Expose orthogonal primitives for creating and managing agent work without adding a new CLI resource noun for every protocol. `invoke` creates work; the single `invocations` command group shows, follows, and cancels it. Each protocol implementation maps these operations to its service APIs. The CLI does not impose concurrency or steering policy.

“Invocation” in the command group is the noun form of `invoke`. It is not limited to the wire protocol named `invocations`.

## Public CLI contract

The following is the target contract after both PRs. PR #9900 implements the shared group and Responses operations; Invocations-protocol show/cancel and persistence arrive in PR #9901.

```bash
# Create and wait, using the selected agent's protocol
azd ai agent invoke "message"

# Responses protocol: continue service-side execution after disconnection;
# remain attached until completion or disconnection
azd ai agent invoke "message" --long-running

# Return after receiving the service-assigned ID
azd ai agent invoke "message" --long-running --no-wait

# Manage current work using the selected agent's protocol
azd ai agent invocations show
azd ai agent invocations follow
azd ai agent invocations cancel

# Explicit protocol and service-assigned ID
azd ai agent invocations show --protocol responses --id <response-id>
azd ai agent invocations follow --protocol responses --id <response-id>
azd ai agent invocations cancel --protocol responses --id <response-id>

azd ai agent invoke "message" --protocol invocations
azd ai agent invocations show --protocol invocations --id <invocation-id>
azd ai agent invocations cancel --protocol invocations --id <invocation-id>
```

The Invocations-protocol lifecycle implementation is delivered in the second PR. Its existing synchronous, SSE, raw, and `202 Accepted` polling behavior on create is unchanged.

### Protocol selection

- `--protocol` selects the protocol explicitly, using the same values as `invoke` (`responses`, `invocations`, and `a2a`). Recognition of a protocol does not imply lifecycle support.
- With `--agent-endpoint`, derive the protocol and target agent from the URL. Reject conflicting `--protocol`, `--agent-name`, or `--version` options.
- Otherwise infer the protocol from the selected agent, using the existing invoke resolution rules. Multi-protocol agents require explicit selection; do not guess from a resource ID.
- `--agent-name` selects an agent in a multi-agent project, following the existing `sessions` command convention.
- Resolve the protocol before looking up the current ID. Unsupported operations fail before lifecycle requests or session/conversation creation.

### Operation support

| Operation | Responses protocol | Invocations protocol |
| --- | --- | --- |
| `invoke` | Responses create | Existing sync/SSE/LRO handling |
| `invoke --long-running` | Supported | Unsupported |
| `invocations show` | Snapshot GET | One-shot GET (PR #9901) |
| `invocations follow` | One streaming GET, replay from the beginning | Unsupported |
| `invocations cancel` | Cancel POST | Cancel POST, if the agent implements it (PR #9901) |

This table describes CLI support, not a guarantee that every deployed agent implements an endpoint. The supplied Invocations reference agent currently returns `cancel_invocation not implemented`; the CLI surfaces that service failure.

A2A, Activity, WebSocket, and voice lifecycle support is not part of this change. Future protocols can implement suitable operations in the shared group without introducing new nouns. Do not invent show/follow/cancel semantics for protocols lacking the corresponding service contract.

## Execution and waiting are independent

`--long-running` requests that service-side work continue if the client disconnects. It does not imply a minimum duration, checkpointing, crash recovery, or automatic reconnection. With Responses it sends `store=true` and `background=true`; the service property remains named `background`.

`--no-wait` controls when the CLI returns. It requires `--long-running`, reads through the first complete event containing the Response ID, saves the ID when local state is available, and detaches without rendering subsequent events. A save failure is reported with the ID rather than silently claiming the current selection was saved.

Without `--no-wait`, the command remains attached until completion or disconnection. It never retries the creating POST. After disconnection, users can run `invocations follow` to replay available output from the beginning.

`--long-running` is remote Responses-only for now. It rejects explicit total `--timeout` and `--output raw`. Normal foreground raw output is unchanged; current-ID extraction is not guaranteed in that raw path.

## Service behavior and steering

### Responses

- `POST /responses` creates a Response from supplied input.
- `background` determines whether service-side work continues after disconnection.
- The same `conversation.id` preserves history. With active work and agent steering support, another POST can steer it. Repeated steering inputs are valid; later input supersedes earlier input.
- Steering works across both foreground and background execution modes.
- Without steering support, the service and agent determine how concurrent Responses are handled.
- `GET /responses/{id}` returns a snapshot.
- `GET /responses/{id}?stream=true` replays buffered events and follows new events. The tested service supports this only for Responses created with `background=true`, including completed ones.
- `POST /responses/{id}/cancel` requests cancellation without stopping the hosted session.

### Invocations

- `POST /invocations` creates an Invocation.
- `GET /invocations/{id}` retrieves it.
- `POST /invocations/{id}/cancel` requests cancellation, subject to agent support.

azd never checks the previous Response status before creating new work, and never rejects new input merely because a Response is active. Existing session/conversation reuse remains unchanged. There is no separate steering command or flag.

## Current selection and persistence

`--id` is the service-assigned ID for the selected protocol. If omitted, use that protocol's current ID for the agent. Explicit show/follow/cancel never changes the current selection.

The latest identified foreground or long-running Responses create saves its Response ID. Successful remote Invocations creates save IDs from the response header or accepted response body. Failed creates without an ID leave the previous selection unchanged. Normal attached execution may continue with a warning if saving the ID fails.

There is one current ID per agent context and protocol, using existing context keys. Concurrent creates may overwrite current selection; use explicit IDs when managing concurrent work.

UserConfig remains protocol-specific internally, despite the shared command group:

```json
{
  "extensions": {
    "ai-agents": {
      "responses": {
        "<agent-key>": {"responseId": "resp_123"}
      },
      "invocations": {
        "<agent-key>": {"invocationId": "inv_123"}
      }
    }
  }
}
```

The Invocation map is introduced by the second PR. There is no compatibility migration. No lifecycle status, event cursor, session, or conversation is stored in these records. Existing session/conversation maps are independent create-time context.

Explicit `--id` with `--agent-endpoint` works without project-backed state. Lifecycle commands must not create sessions or conversations. The current caller must provide the correct authentication, identity, and allowed custom headers.

## Lifecycle behavior

### Show

One GET retrieves the current service resource. JSON is the default; `--output table` gives a summary consistent with `sessions show`. The command does not use cached status, poll, or change current selection.

### Follow

For Responses, perform exactly one `GET /responses/{id}?stream=true`, never including `starting_after`. Replay starts from the beginning on every command invocation. Do not track sequence numbers, suppress events based on sequence, retry HTTP errors, or reconnect automatically.

If the stream disconnects before a terminal event, report the ID and direct the user to run `invocations follow` again. Do not silently replace follow with a snapshot GET.

Retain SSE framing, bounded decoding, identity validation, output rendering, and terminal-event handling. Completed Responses replay and exit successfully. Failed, incomplete, and cancelled outcomes retain their existing error behavior.

### Cancel

Call the protocol's cancel endpoint for the explicit or current ID, without changing current selection or stopping the session. If cancellation is rejected, one GET may confirm that work is already terminal; then report that state and succeed. Otherwise preserve the cancellation failure. Do not treat a missing cancel implementation as successful cancellation of active work.

## Code organization

Within `internal/cmd/`:

- `invoke.go`: common invoke command, flags, context, and protocol dispatch.
- `invocations.go`: shared lifecycle command group, flags, protocol/ID selection, and dispatch.
- `invoke_response.go`: Responses create and lifecycle implementations, SSE, output, and ID storage.
- `invoke_invocation.go`: Invocations-protocol create and lifecycle implementations, polling, output, and ID storage.
- `agent_endpoint.go`: shared endpoint parsing and protocol invoke URL construction.

No `responses` command group or separate `responses.go` remains. No generic capability framework is required; explicit protocol dispatch is sufficient.

## Validation and delivery

1. **PR #9900:** shared `invocations show|follow|cancel` commands with Responses support, `--long-running`, and removal of the superseded background/cursor implementation from #9703.
2. **PR #9901:** add Invocations-protocol show/cancel and current-ID storage to that same command group, preserving existing create execution.

Test protocol selection (explicit, endpoint-derived, inferred, and ambiguous), unsupported operations, long-running/no-wait rules, explicit/current ID selection, no current-state mutation by lifecycle operations, HTTP methods/headers/paths, full replay without retries, terminal-cancel fallback, JSON/table output, and existing invoke regressions. Validate the full extension and use focused race tests for new coverage.
