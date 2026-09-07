# Agent protocol primitives

## Status

- **PRD:** [GitHub issue #9676](https://github.com/Azure/azure-dev/issues/9676)
- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Feature status:** Preview

## Goal

Expose orthogonal CLI primitives that closely map to the hosted-agent service APIs. The CLI creates work with `invoke`; protocol-specific command groups retrieve, follow, and cancel that work. The CLI does not impose concurrency or steering policy on top of the service.

## Service behavior

### Responses

- `POST /responses` creates a Response from supplied input.
- The `background` request property determines whether work continues after the client disconnects.
- Supplying the same `conversation.id` preserves history. If work is active and the agent supports steering, another POST can steer it. Otherwise the agent and service determine how concurrent Responses are handled.
- Foreground and background Responses can steer either execution mode.
- `GET /responses/{id}` returns a snapshot.
- `GET /responses/{id}?stream=true` replays buffered events from the beginning and follows new events.
- `POST /responses/{id}/cancel` cancels a Response.

### Invocations

- `POST /invocations` creates an Invocation.
- `GET /invocations/{id}` returns an Invocation.
- `POST /invocations/{id}/cancel` cancels an Invocation.

## Public CLI contract

### Responses

```bash
# POST /responses with background=false
azd ai agent invoke "message"

# POST /responses with background=true; remain attached
azd ai agent invoke "message" --background

# POST /responses with background=true; detach after the Response ID is saved
azd ai agent invoke "message" --background --no-wait

# GET /responses/{id}; omit the ID to use the current Response
azd ai agent responses show [--response-id <id>]

# GET /responses/{id}?stream=true; replay from the beginning and follow
azd ai agent responses follow [--response-id <id>]

# POST /responses/{id}/cancel
azd ai agent responses cancel [--response-id <id>]
```

### Invocations

```bash
azd ai agent invoke "message" --protocol invocations
azd ai agent invocations show [--invocation-id <id>]
azd ai agent invocations cancel [--invocation-id <id>]
```

Invocations primitives are delivered in a second stacked PR. Existing synchronous, streaming, and long-running Invocation POST behavior is unchanged.

## Design principles

1. **Creation is independent from lifecycle operations.** `invoke` only creates work.
2. **Protocol resources have protocol commands.** Responses and Invocations do not share lifecycle flags.
3. **No client-side concurrency policy.** azd never blocks a new Response because another Response appears active.
4. **Steering is normal creation.** Posting input with the same conversation is the service steering primitive; azd has no `--steer` flag.
5. **Execution mode and steering are independent.** Foreground and background only describe client-disconnect behavior.
6. **Current IDs are conveniences, not lifecycle state.** The latest identified resource is saved for omission of an explicit ID.
7. **Explicit targeting is side-effect free.** Show, follow, and cancel with an explicit ID never change current selection.
8. **Replay starts from the beginning.** azd does not maintain a playback cursor.

## Current resource selection

azd stores one current Response ID and one current Invocation ID per existing agent context key.

A successfully identified foreground or background create replaces the corresponding current ID. A create that fails before azd receives an ID leaves the previous current ID unchanged.

Lifecycle commands resolve their target as follows:

1. Use `--response-id` or `--invocation-id` when supplied.
2. Otherwise load the current ID for the selected agent.
3. Fail with actionable guidance when neither is available.

Explicit IDs work with `--agent-endpoint` and do not require project-backed local state. Explicit show, follow, and cancel operations do not update current selection.

Response state is intentionally minimal:

```yaml
extensions:
  ai-agents:
    responses:
      "<agent-key>":
        responseId: resp_123
```

No Response status, session, conversation, or event sequence is persisted. Existing session and conversation stores remain responsible for subsequent create context.

## Responses create

All Responses creates use `stream=true` so azd can render output and identify the Response. Background creates additionally send:

```json
{
  "store": true,
  "background": true
}
```

`--no-wait` requires `--background`. It reads complete SSE events through the first event that identifies the Response, saves and prints the ID, then closes the connection and returns success.

A foreground or background create never retrieves or evaluates the status of the previous current Response. Reusing the existing conversation preserves history and lets the service apply steering or concurrency behavior.

If an attached background create disconnects after its Response ID is known, azd reports the ID and directs the user to `responses follow`. It does not retry the creating POST or reconnect automatically.

`--output raw` remains unsupported for background create because `--no-wait` and create-to-follow recovery require event parsing. Existing foreground raw output remains unchanged and does not promise current-ID extraction when the wire response cannot be inspected without changing raw output.

## Responses show

`responses show` performs exactly one snapshot GET and prints the service resource:

- JSON by default.
- Table output with `--output table`, following `sessions show` conventions.

It does not use cached status and does not modify current selection.

## Responses follow

The first follow request is:

```http
GET /responses/{id}?stream=true
```

It intentionally omits `starting_after`, including when the Response is already terminal. Buffered events replay from the beginning and the command follows new events until terminal completion.

The command makes one streaming GET and does not track event sequence numbers or reconnect automatically. If the connection ends before a terminal event, it reports the Response ID and directs the user to rerun `responses follow`, which replays from the beginning.

Follow retains SSE framing, bounded event decoding, Response identity validation, and terminal status handling. It does not silently become show or retry the request.

A completed Response replays and exits successfully. Failed, incomplete, and cancelled terminal outcomes preserve their existing command error behavior after rendering available output.

## Responses cancel

Cancel always targets the explicit or current Response ID and calls the cancel endpoint. It does not rely on locally cached status and does not alter current selection.

Cancellation is idempotent from the CLI perspective. If cancel is rejected because the Response is already terminal, azd may perform one snapshot GET to confirm the terminal service state, report it, and return success.

Cancel never stops or deletes the hosted-agent session.

## Output and identity

Friendly create output prints the Response ID as soon as it has been successfully saved when local state is available. If local state is unavailable, explicit lifecycle commands remain available using the printed ID.

`responses show` follows resource-show output conventions. `responses follow` uses the existing friendly SSE renderer. `responses cancel` prints the resulting or confirmed status.

## Validation

- `--no-wait` requires `--background`.
- `--background` is remote Responses-only.
- Background create rejects an explicitly supplied total `--timeout`.
- Background create rejects raw output.
- Responses lifecycle commands are remote-only.
- A lifecycle `--agent-endpoint` must identify a Responses endpoint.
- An omitted resource ID requires project-backed current state.
- An explicit resource ID does not require current state.

## Removed implementation policy

This design deliberately removes the earlier resumable-work orchestration introduced with the first implementation slice:

- No `invoke --resume`, `invoke --continue`, `invoke --steer`, or invoke-level `--cancel`.
- No active-Response guard or snapshot preflight before create.
- No event cursor, replay offset, or periodic progress persistence.
- No persisted Response status, session, or conversation metadata.
- No terminal-state network short circuit.
- No lifecycle context inheritance from a saved Response record.
- No follow snapshot fallback.

## Testing

Responses coverage must include:

- Foreground and background request bodies.
- `--no-wait` identity persistence before detach.
- A second create while another Response is active, with no preflight GET.
- Current ID replacement only after identity is received.
- Explicit and implicit show, follow, and cancel selection.
- Explicit lifecycle operations with `--agent-endpoint` and no local state.
- Explicit operations not changing current selection.
- Show JSON and table output.
- Follow without `starting_after` or automatic retries.
- A new follow replaying from the beginning.
- Terminal replay success.
- Idempotent terminal cancellation.
- Existing foreground, local, Invocations, A2A, and raw-output regressions.

## Delivery

1. **Responses primitives:** simplify the merged attached-background implementation, add Responses show/follow/cancel, and retain only in-process follow resilience.
2. **Invocations primitives:** add Invocation current-ID persistence, show, and cancel without changing existing invoke execution behavior.
