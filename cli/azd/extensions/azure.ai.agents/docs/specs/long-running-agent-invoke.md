<!-- cspell:ignore checkpointed conv exterrors httptest nonterminal omitempty sess unreplayable -->

# Long-Running Responses Support in `azd ai agent invoke`

## Status

- **PRD:** [GitHub issue #9676](https://github.com/Azure/azure-dev/issues/9676)
- **Component:** `cli/azd/extensions/azure.ai.agents`
- **Protocol:** Remote hosted-agent Responses lifecycle, plus one-shot retrieval for remote Invocations
- **Feature status:** Preview

This document is the implementation specification for the product behavior in issue #9676. The issue is the source of truth for the CLI user experience. This specification defines the MVP architecture, protocol handling, local state, validation, and test strategy.

## Goals

The MVP adds these capabilities to `azd ai agent invoke`:

1. Start a stored background Response and remain attached to its SSE stream.
2. Start a stored background Response, capture its identity, and return without waiting for completion.
3. Reconnect to the saved current background Response.
4. Resume strictly after the automatically saved event sequence cursor.
5. Submit revised input that steers the active current Response, or starts the next turn when it is terminal.
6. Cancel the saved current background Response without stopping its hosted-agent session.
7. Preserve existing foreground Responses, local invoke, Invocations create/poll, and A2A behavior.
8. For remote Invocations, save the latest invocation context and let message-free `--continue` perform one best-effort GET without applying Responses lifecycle semantics.

## Non-goals

The MVP does not add:

- Background creation, polling continuation, stream reconnect, replay, steering, or cancellation semantics for Invocations or A2A. Remote Invocations supports only the additive one-shot retrieval defined below.
- Local AgentServer background/reconnect support.
- Task or State Store commands.
- Conversation branching from an older terminal Response.
- Explicit response selection or cross-machine continuation.
- Graceful Ctrl-C interception or detach-specific cleanup.
- Raw HTTP output for background lifecycle operations.
- Rich rendering for every Responses event type.
- A new core azd interrupt or extension-runner API.

## MVP deviations from the target PRD

Issue #9676 describes the intended end-state UX. The first implementation deliberately defers two parts of that target:

1. Ctrl-C will disconnect the current azd process and leave accepted background work running, but the MVP does not guarantee a final cursor flush or detach banner. Response identity and cursor state are saved before interruption whenever possible.
2. Raw output for the new lifecycle operations is deferred and rejected rather than silently losing reconnect state.

These deviations are called out in [MVP limitations](#mvp-limitations) and [Future optimizations](#future-optimizations). They do not change the final product direction in the PRD.

## Delivery plan

The implementation is delivered as four independently useful, stacked pull requests:

1. **Attached background Responses:** add `--background`, typed SSE processing, and saved Response identity/cursor state. The command remains attached until terminal completion or interruption. Active-turn enforcement is deferred until users have follow/cancel recovery in PR 2.
2. **Responses detach, reconnect, and cancel:** add `--no-wait`, message-free `--continue`, cursor replay, transport recovery, snapshot fallback, and `--cancel`.
3. **Responses steering:** add message-bearing `--continue`, replacement turns, terminal next-turn behavior, and service-refreshed active-turn enforcement.
4. **Invocations retrieval:** save the latest remote invocation context and add message-free `--continue` as exactly one best-effort GET with no polling, status interpretation, replay, steering, or cancellation.

PR 3 and PR 4 depend on PR 2 and can be reviewed in parallel. Each PR includes focused unit tests and live validation against the corresponding hosted reference agent.

## Public CLI contract

The PRD defines the full user-facing behavior. The new forms are summarized here to anchor the implementation:

```bash
azd ai agent invoke "long task" --background
azd ai agent invoke "long task" --background --no-wait

azd ai agent invoke --continue
azd ai agent invoke "revised requirements" --continue
azd ai agent invoke --cancel
```

Message-free operations in multi-agent projects use an explicit selector:

```bash
azd ai agent invoke --continue --agent-name my-agent
azd ai agent invoke --cancel --agent-name my-agent
```

The existing named-message form remains valid:

```bash
azd ai agent invoke my-agent "revised requirements" --continue
```

Because the existing grammar treats one positional argument as a message, `azd ai agent invoke my-agent --continue` sends the text `my-agent`; it does not select that agent. Help and validation examples must direct message-free multi-agent operations to `--agent-name`.

## Existing implementation

The current command is primarily implemented in:

```text
cli/azd/extensions/azure.ai.agents/internal/cmd/invoke.go
```

Remote Responses currently:

1. Resolves the remote agent, session, and conversation.
2. Sends one `POST` with `stream=true`.
3. Reads one SSE connection.
4. Prints `response.output_text.delta` events.
5. Returns when `response.completed` is received.

It does not retain the Response ID or event cursor, retrieve an existing Response, reconnect a stream, steer a turn, or cancel a Response.

Current local state is stored in string maps:

```yaml
extensions:
  ai-agents:
    sessions:
      "<agent-key>": "sess_123"
    conversations:
      "<agent-key>": "conv_456"
```

The existing agent key contains the normalized project endpoint, agent name, effective version, and local/remote mode. It does not include header-based user identity. The MVP intentionally reuses this key and inherits that limitation.

## Command operation model

### Operation enum

Parse the command into one operation before protocol routing or network access:

```go
type invokeOperation int

const (
    invokeForegroundCreate invokeOperation = iota
    invokeBackgroundCreateAndFollow
    invokeBackgroundCreateNoWait
    invokeContinueExisting
    invokeSteerOrCreateNext
    invokeCancelExisting
)
```

The operation is selected as follows:

| Input | Operation |
| --- | --- |
| Message without new lifecycle flags | `invokeForegroundCreate` |
| Message with `--background` | `invokeBackgroundCreateAndFollow` |
| Message with `--background --no-wait` | `invokeBackgroundCreateNoWait` |
| No message with `--continue` | `invokeContinueExisting` |
| Message with `--continue` | `invokeSteerOrCreateNext` |
| No message with `--cancel` | `invokeCancelExisting` |

### New flags

Add fields to `invokeFlags`:

```go
background    bool
noWait       bool
continueRun  bool
cancel        bool
agentName    string
```

`continueRun` is the internal name for the public `--continue` flag to avoid confusing the field with control flow.

### Positional parsing

Preserve the current `[name] [message]` positional grammar for message operations. Do not reinterpret a single positional argument differently only because `--continue` or `--cancel` is present.

For message-free operations:

- Agent auto-detection remains the default.
- `--agent-name` explicitly selects an agent in a multi-agent project.
- `--agent-endpoint` remains available outside an azd project.

### Validation

Perform structural validation before project resolution, followed by protocol validation after protocol resolution.

| Combination | Result |
| --- | --- |
| `--background` without a message or `--input-file` | Reject |
| `--background` with a message or `--input-file` | Accept |
| `--no-wait` without `--background` | Reject |
| `--continue` with `--cancel` | Reject |
| `--cancel` with a message or `--input-file` | Reject |
| Message-free `--continue` with `--input-file` | Reject |
| `--background` with `--continue` or `--cancel` | Reject |
| `--no-wait` with `--continue` or `--cancel` | Reject |
| `--continue` or `--cancel` with `--session-id` or `--new-session` | Reject; use the saved response context |
| `--continue` or `--cancel` with `--conversation-id` or `--new-conversation` | Reject; use the saved response context |
| `--agent-name` with a positional agent name | Reject |
| New lifecycle operation with `--local` | Reject |
| Message-free `--continue` with remote Invocations | Accept; perform one best-effort GET of the saved invocation |
| Message-bearing `--continue` with Invocations | Reject; steering is not defined |
| `--background`, `--no-wait`, or `--cancel` with Invocations | Reject |
| New lifecycle operation with A2A | Reject after protocol resolution |
| New lifecycle operation with `--output raw` | Reject before network access |
| Message-free operation in an ambiguous project without `--agent-name` and without prompts | Reject with agent-selection guidance |

## Invocations one-shot retrieval contract

For a remote Invocations agent, message-free `azd ai agent invoke --continue` loads the latest saved invocation for the selected agent context and sends exactly one authenticated request:

```text
GET {projectEndpoint}/agents/{agent}/endpoint/protocols/invocations/{invocationId}?api-version={apiVersion}[&agent_session_id={sessionId}]
```

The invocation ID is escaped as one path segment and query parameters are built with `net/url`. The record stores the invocation ID, effective session ID, and API version. IDs are captured from successful remote Invocations responses without inferring whether the work is background-capable.

The GET uses a fresh bearer token, the saved session, and the `--user-identity` and allowed client headers supplied on the current command. Users must repeat the same identity used for the original invocation; raw identities are not persisted.

This operation deliberately:

- performs one request with no CLI-level retry or polling;
- does not interpret status, result, error, cursor, or terminal fields from a successful body;
- does not update or clear the saved invocation based on the GET body;
- does not reconnect an SSE response;
- rejects messages, session/conversation overrides, local mode, `--agent-endpoint`, and raw output;
- returns `404`, throttling, service, timeout, and transport failures immediately with best-effort guidance.

Running `--continue` again manually performs another independent GET. This is retrieval, not a guarantee of background execution, reconnect, or replay.

## Responses HTTP contract

All requests use the resolved project endpoint, agent name, API version, bearer token, user-identity header, and allowed client headers.

### Endpoint forms

```text
POST   {responsesBase}?api-version={apiVersion}
GET    {responsesBase}/{responseId}?api-version={apiVersion}
GET    {responsesBase}/{responseId}?api-version={apiVersion}&stream=true[&starting_after=N]
POST   {responsesBase}/{responseId}/cancel?api-version={apiVersion}
```

Where:

```text
{responsesBase} = {projectEndpoint}/agents/{agent}/endpoint/protocols/openai/responses
```

Response IDs must be escaped as path segments. Query parameters must be built with `net/url`, not string concatenation.

### Background create and follow

Request body:

```json
{
  "input": "long task",
  "stream": true,
  "store": true,
  "background": true,
  "agent_session_id": "sess_123",
  "conversation": {"id": "conv_456"}
}
```

The exact hosted-session property must be verified before implementation. The existing CLI sends `session_id`; current hosted-agent long-running references use `agent_session_id`. See [Session compatibility investigation](#session-compatibility-investigation). The request examples in this specification use `agent_session_id` provisionally and are not the compatibility decision.

The command parses and renders SSE until a terminal event or a user interruption. As soon as an identity-bearing lifecycle event arrives, it prints and saves the Response ID before rendering later output.

### Background create with `--no-wait`

The MVP chooses a streaming create for `--no-wait` so attached and detached background creation share the same identity/cursor parser and azd obtains an initial replay cursor before returning. The service compatibility investigation must confirm replay behavior before implementation; the choice is not based on an assumption that every stored non-streaming create is inherently unreplayable.

The command:

1. Sends the same stored background streaming request.
2. Reads complete SSE events until one contains the Response ID.
3. Saves the Response record and the event's sequence cursor.
4. Prints the Response identity, actual snapshot status, and next-step command. The cursor remains internal.
5. Closes the response body immediately after applying that identity event; later buffered events are not rendered.
6. Returns success.

Do not retry the creating POST. If the stream ends or fails before a Response ID is received, return an actionable error explaining that work might have been accepted but cannot be reconnected to implicitly.

### Follow an existing Response

Resolve the saved current background Response. Fail with guidance to start one when no record exists. Send:

```http
GET {responsesBase}/{responseId}?api-version={apiVersion}&stream=true&starting_after={cursor}
Accept: text/event-stream
```

Use the saved cursor for the current Response when available; otherwise omit `starting_after` and replay retained events from the beginning. The server uses strict-after semantics: when azd supplies its saved cursor, the first accepted event must have a greater sequence number.

If the Response is terminal and replay has drained, print the terminal status/result and return. If replay is unavailable, retrieve the Response without `stream=true`; render its authoritative snapshot and status.

### Cancel a Response

Send:

```http
POST {responsesBase}/{responseId}/cancel?api-version=v1
```

Cancellation applies to the saved current Response, not the hosted-agent session. Treat cancellation as idempotent. If the Response is already terminal, print its current status and return success.

Do not delete the saved session, conversation, or Response record. Update the locally saved terminal status when available.

## SSE processing

### Decoder

Replace the current line-oriented rendering parser with an SSE decoder that dispatches one event at a blank-line boundary.

The decoder must support:

- `event:` with or without one optional leading value space.
- One or more `data:` fields joined with newline separators.
- Comment lines.
- Data-only events whose JSON `type` provides the discriminator.
- A configurable but bounded maximum event size larger than the current one-line limit.
- Context cancellation while blocked on input.

Decode the minimum common envelope first:

```go
type responsesEventEnvelope struct {
    Type           string          `json:"type"`
    SequenceNumber *int64          `json:"sequence_number"`
    Response       json.RawMessage `json:"response"`
}
```

Use a pointer for `SequenceNumber` because zero is valid.

### Lifecycle states

Recognize these Response states:

```text
queued
in_progress
completed
failed
incomplete
cancelled
```

Recognize at least these lifecycle events:

```text
response.created
response.queued
response.in_progress
response.completed
response.failed
response.incomplete
error
```

Cancellation may be observed as a Response status from a snapshot or fallback GET rather than a distinct SSE event.

### Cursor commitment

A cursor is committed only after the event is:

1. Completely read.
2. Successfully decoded.
3. Successfully applied to local response state and rendering.

Ignore replayed events with a sequence number less than or equal to the last committed in-memory cursor. Reject or reconnect on a response ID mismatch.

Persist cursor progress:

- Immediately when the Response ID is first learned.
- On lifecycle transitions.
- Periodically while events stream, no less often than once every three seconds or every 64 committed events, whichever occurs first.
- On terminal completion when the process remains alive.

Periodic persistence limits replay after abrupt Ctrl-C or process termination. It does not guarantee that the displayed output and saved cursor are identical at the instant the process exits.

### Recovery snapshot reset

The first `response.in_progress` begins normal execution. A later `response.in_progress` after recovery is an authoritative snapshot reset.

On reset, print:

```text
--- RESPONSE RECOVERED: OUTPUT RESET TO LAST CHECKPOINT ---
```

Then:

1. Replace the in-memory response/output model with the event snapshot.
2. Print the authoritative checkpointed text from that snapshot.
3. Apply later deltas to the replacement model.

Use this append-only behavior for TTY and non-TTY output. Text that appeared before the reset may appear again after the marker. Terminal redraw is not part of the MVP.

### Friendly rendering

Preserve current text-delta rendering for the common case. Internally retain enough output item and content-part indexes to apply authoritative snapshots correctly.

The MVP may leave non-text tool, reasoning, image, and annotation events unrendered in friendly output, but the decoder must still advance the cursor after safely accepting them.

## Reconnect policy

After a Response ID is known, an unexpected EOF or retryable transport failure on an active stream triggers a follow GET from the last committed cursor. Never retry the creating POST.

Retry:

- Connection resets and temporary network errors.
- HTTP 408, 429, and 5xx responses.
- Respect integer or HTTP-date `Retry-After` values.

Do not retry ordinary 400, 401, 403, or response-not-found failures without classification. Reacquire the bearer token before a reconnect request so an hours-long operation does not reuse an expired token.

Use exponential reconnect delays starting at one second and capped at 30 seconds. After five consecutive failed reconnect attempts:

1. Retrieve the current Response snapshot once when possible.
2. Persist the latest known local state.
3. Print the Response ID and `azd ai agent invoke --continue` guidance. Keep the cursor internal.
4. Return a classified error without cancelling server-side work.

A successful event resets the consecutive reconnect failure count.

## Steering and multi-turn behavior

### Single-current-response guard

Before starting any ordinary foreground or background turn, inspect the saved background record:

1. If no record exists or the saved status is terminal, proceed.
2. If the saved status is `queued` or `in_progress`, retrieve current service status.
3. If the service reports terminal, update the record and proceed.
4. If the service still reports active, reject the new turn and direct the user to `invoke <message> --continue` to steer or `invoke --cancel` to stop it.
5. A new background turn replaces a terminal record when its identity arrives. A successfully accepted ordinary foreground turn clears the terminal background record because the conversation advances outside the background lifecycle; retain the record if the foreground request fails before acceptance.

This guard prevents accidental concurrent turns in the same conversation. The service may temporarily expose an active superseded Response and a queued replacement during steering, but only one handler executes at a time.

While the saved background Response is active:

| Command | Result |
| --- | --- |
| `invoke "message"` | Reject |
| `invoke "message" --background` | Reject |
| `invoke --continue` | Follow current output |
| `invoke "message" --continue` | Steer the current turn |
| `invoke --cancel` | Cancel the current turn |

After it becomes terminal:

| Command | Result |
| --- | --- |
| `invoke "message"` | Start a normal foreground next turn |
| `invoke "message" --background` | Start a new background next turn |
| `invoke "message" --continue` | Start a new background next turn |
| `invoke --continue` | Replay or retrieve the terminal result |
| `invoke --cancel` | Report terminal status without failing |

### Message-bearing continuation

Message-bearing `--continue` always creates a new stored background Response in the saved conversation and hosted session:

```json
{
  "input": "revised requirements",
  "stream": true,
  "store": true,
  "background": true,
  "agent_session_id": "sess_123",
  "conversation": {"id": "conv_456"}
}
```

azd uses one consistent conversation-based history mechanism for ordinary, background, and steering turns. It does not send `previous_response_id`. If the saved conversation is busy and the agent enables `steerable_conversations`, the service queues the new turn and cooperatively winds down the active handler. If the conversation is idle, the same request starts the normal next turn. The request shape therefore does not depend on a potentially stale local Response status and has no active-to-terminal predecessor race.

Use the session and conversation bound to the saved current record. Save the new Response over that record after its identity event arrives and remain attached until completion, interruption, or reconnect exhaustion. A superseded Response may become `cancelled`, `failed`, or another documented terminal status, possibly with no partial output; the new Response determines the command result. Classify conversation-lock and steering-queue failures as actionable service errors.

### Session and conversation support

The background create captures its effective session and conversation. Follow, steer, and cancel inherit them; users do not repeat IDs.

| Operation | No override | Session/conversation override |
| --- | --- | --- |
| Foreground create, no active Response | Allow | Allow using existing context-selection behavior |
| Background create, no active Response | Allow | Allow and save the selected context with the new Response |
| Any create, active Response | Reject with follow/steer/cancel guidance | Reject with the same guidance |
| Follow, steer, or cancel | Use saved context | Reject and tell the user to remove the override |

Before any create, refresh a saved nonterminal Response. If still active, offer: follow with `invoke --continue`, steer with `invoke "<message>" --continue`, or stop with `invoke --cancel`. If terminal, preserve the old record until the new request succeeds: a foreground create then clears it, while a background create replaces it when the new Response identity is saved.

Example:

```bash
azd ai agent invoke "long task" --background --no-wait \
  --session-id sess_123 --conversation-id conv_456
azd ai agent invoke --continue # reuses sess_123 and conv_456
```

Clearing local state does not cancel server-side work.

## Local response state

### Existing context key

Reuse the existing `agentKey` without adding user-identity, session, or conversation dimensions. User-identity isolation and effective-version key normalization are inherited concerns and remain future optimizations.

### Proposed UserConfig schema

```yaml
extensions:
  ai-agents:
    sessions:
      "<agent-key>": "sess_123"
    conversations:
      "<agent-key>": "conv_456"
    backgroundResponses:
      "<agent-key>":
        responseId: "resp_123"
        cursor: 42
        status: "in_progress"
        sessionId: "sess_123"
        conversationId: "conv_456"
```

Suggested Go shape:

```go
type savedBackgroundResponse struct {
    ResponseID     string `json:"responseId"`
    LastSequenceNumber *int64 `json:"cursor,omitempty"`
    Status         string `json:"status,omitempty"`
    SessionID      string `json:"sessionId,omitempty"`
    ConversationID string `json:"conversationId,omitempty"`
}
```

Cursor uses a pointer because zero is valid.

### Update rules

- Save or replace the record when a background Response ID is first learned.
- Update its cursor and status while following.
- Replace it with a steering replacement or next background turn.
- Update it to terminal after completion or cancel.
- Clear it after a successful ordinary foreground turn or after successfully changing away from its terminal session or conversation.
- Never move the persisted cursor backward intentionally.

Use the existing UserConfig read-modify-write mechanism and accept its current last-writer-wins behavior for MVP.

### Resolution rules

For `--continue` or `--cancel`:

1. Resolve the existing agent context key.
2. Load its one background record.
3. Fail with actionable guidance if missing.
4. Follow, steer, or cancel the Response ID in that record.

Follow and cancel must not create a new hosted-agent session or conversation while resolving existing work. They must bypass normal-invoke helpers that auto-create version-backed sessions or conversations when saved state is absent.

## Session compatibility investigation

Before implementation finalizes request construction, verify which body property is accepted by every supported path:

```text
session_id
agent_session_id
```

Test at least:

- Current deployed hosted-agent Responses endpoint.
- The long-running Responses reference agent.
- Current local AgentServer Responses package.

The public hosted long-running examples use `agent_session_id`. Existing local and remote azd code uses `session_id`. Do not send both unless the protocol schema explicitly permits it.

The selected property must route steering and follow-up turns to the same hosted session. Update existing remote Responses requests only if compatibility is verified; local behavior may require a separate field.

## HTTP clients and timeout behavior

Background create/follow operations ignore the existing total `--timeout` and use no `http.Client.Timeout`. A stream can remain attached indefinitely.

Use a 30-second bound for response-header acquisition, status GET, cancel POST, and individual reconnect setup. Token acquisition continues to use the credential implementation's context and timeout behavior.

Use transport-level response-header timeouts rather than a total request timeout. Reconnect sleeps must observe command context cancellation. Reject an explicitly supplied `--timeout` with a background lifecycle operation because those operations do not honor an overall request timeout.

Existing timeout behavior remains unchanged for foreground Responses, local invoke, Invocations, and A2A.

## Ctrl-C behavior

The MVP adds no signal handler and requires no azd core change.

The extension persists the Response ID immediately and checkpoints the cursor while streaming. Once the server has accepted a stored background Response, an abrupt client disconnect does not cancel server-side work.

Known edge cases:

- Ctrl-C before the identity event can leave accepted work without a locally known Response ID.
- The last displayed event can be newer than the last periodically persisted cursor, causing bounded duplicate replay.
- Detach-specific output and guaranteed final cursor persistence are not available.

These are accepted MVP limitations. Explicit cancel remains the only CLI operation that calls the Response cancel endpoint.

## Output behavior

### Friendly output

Print identifiers as soon as available:

```text
Response:     resp_123
Session:      sess_123
Conversation: conv_456
```

On `--no-wait`, print:

```text
Status: queued

Next:
  azd ai agent invoke --continue
```

Print the status actually present in the identity-bearing event; do not assume `queued`. Save its sequence cursor internally without printing it.

On a failed reconnect sequence, print the Response ID for diagnostics and future recovery tooling.

### Raw output

Reject `--output raw` for every new background lifecycle operation. Keep existing foreground raw behavior unchanged.

This avoids silently disabling response identity extraction, cursor persistence, replay suppression, snapshot reset handling, and automatic reconnect.

### Structured output

The MVP does not add `--output json` to invoke. This is a future optimization for automation.

## Errors

Add stable extension error codes for at least:

- Invalid background flag combinations.
- Background lifecycle used with an unsupported protocol or local mode.
- Missing implicit background Response.
- Response context mismatch.
- Response identity not received.
- Reconnect exhausted.
- Replay unavailable with no retrievable snapshot.
- Steering rejected because the saved conversation is locked, unavailable, or its steering queue is full.

Context errors must include the valid next action:

- A lifecycle override tells the user to remove `--session-id`, `--new-session`, `--conversation-id`, or `--new-conversation` because the saved context is reused.
- An active-context switch offers follow (`invoke --continue`), steer (`invoke "<message>" --continue`), and cancel (`invoke --cancel`).

Lower-level HTTP and decoding helpers return wrapped plain errors. Classify at background operation boundaries using `internal/exterrors`. Use service errors for Azure HTTP failures and validation/dependency errors for local state and option failures.

Do not include response bodies containing user content, message text, response IDs, session IDs, conversation IDs, or raw user identities in telemetry fields.

## Code organization

Keep implementation within the extension and the existing `cmd` package to avoid exporting command-specific types.

Recommended files:

```text
internal/cmd/invoke.go
    Existing Cobra command, common parsing, protocol routing

internal/cmd/invoke_background.go
    Operation orchestration, create/follow/steer/cancel, reconnect policy

internal/cmd/invoke_responses_stream.go
    SSE framing, event decoding, response state application, friendly rendering

internal/cmd/invoke_response_store.go
    UserConfig schema, existing agent keys, current response/cursor persistence

internal/cmd/agent_endpoint.go
    Add retrieve/follow/cancel Responses URL builders
```

Small testability seams:

```go
type httpDoer interface {
    Do(*http.Request) (*http.Response, error)
}

type responseStateStore interface {
    Get(context.Context, string) (*savedBackgroundResponse, error)
    Save(context.Context, string, savedBackgroundResponse) error
    Delete(context.Context, string) error
}
```

Inject a sleeper/clock for reconnect and persistence-throttle tests. Pass `io.Writer` into new rendering code rather than adding new global stdout writes.

A mechanical split of unrelated existing invoke code is optional and must not block the MVP.

## Testing strategy

### Command and validation tests

Add table-driven coverage for every validation row, including named-agent message-free forms, endpoint mode, protocol auto-detection, raw rejection, and existing foreground regression behavior.

### SSE decoder tests

Cover:

- Standard blank-line dispatch.
- Multiple `data:` fields.
- Data-only event type.
- Response ID from lifecycle snapshots.
- Sequence zero.
- Cursor commit only after successful application.
- Duplicate and decreasing replay events.
- Partial event interruption.
- Created, queued, in-progress, completed, failed, incomplete, cancelled status snapshots, and error events.
- Recovery reset marker and authoritative snapshot replacement.
- Unrendered non-text events still advancing the cursor.

### Background HTTP tests

Use `httptest.Server` with scripted connections:

1. Verify attached background request fields and headers.
2. Verify `--no-wait` prints the actual identity-event status, persists its cursor without printing it, and exits before rendering later buffered events.
3. Drop a stream after sequence 3.
4. Assert reconnect uses `starting_after=3`.
5. Replay sequences 2, 3, and 4; assert only 4 is applied.
6. Finish with a terminal event.

Also cover replay fallback, retryable and non-retryable status codes, `Retry-After`, token reacquisition, reconnect exhaustion, and context cancellation during backoff. Verify that background clients have no total timeout while existing foreground invokes retain their current timeout. Verify that follow and cancel never call session or conversation creation APIs.

### Persistence tests

Build on the existing in-process UserConfig gRPC test server. Cover:

- One record per existing agent key.
- Missing current response guidance.
- Cursor zero and cursor monotonicity.
- Record replacement on create, steer, and next turn.
- Terminal status update on completion and cancel.
- Terminal record clearing after a successfully accepted ordinary foreground turn, but not after a failed request.
- All session/conversation overrides rejected for follow, steer, and cancel.
- Explicit context captured on background create and reused without repeated IDs.
- Active context-change rejection with follow/steer/cancel guidance.
- Terminal record clearing only after a successful session/conversation change.
- Existing last-writer-wins behavior without adding identity-aware keys.

### Steering tests

Cover:

- An active current Response blocks ordinary and background new turns.
- Active and terminal message-bearing `--continue` use the same saved `conversation.id` and compatible `agent_session_id` request shape.
- Message-bearing `--continue` never sends `previous_response_id`.
- A busy steerable conversation interrupts or winds down the active turn and completes the replacement.
- An idle conversation starts the normal next background turn with the same request shape.
- Two consecutive steering cycles remain in one conversation without a fork or forwarding error.
- Active session/conversation changes are rejected; terminal changes clear the record after success.

### Cancellation tests

Cover current-record selection, idempotent terminal cancellation, user-identity header propagation, no session stop/delete call, local status update, service errors, and missing current state.

### Live validation

Use the resilient Responses reference agent to verify:

- Background execution survives client disconnect.
- `--no-wait` can later reconnect to the same stream.
- Reconnect resumes strictly after the cursor.
- Steering cancels/supersedes the active turn and completes the replacement.
- Recovery emits the reset marker and completes with authoritative output.
- Abrupt client interruption after identity/cursor persistence leaves work running and permits a later reconnect; no graceful detach banner is required by the MVP.
- Explicit cancel does not stop or delete the session.

## MVP limitations

1. **No graceful Ctrl-C detach:** azd cannot guarantee detach-specific output or a final cursor flush.
2. **One local current Response:** there is no explicit response selection, cross-machine continuation, or management of a record after local state is lost.
3. **No intentional parallel turns:** active work blocks ordinary/background new turns and session/conversation changes; steering is the sequential replacement path.
4. **Existing user-identity state behavior:** the background record reuses the current unscoped agent key, so identities can overwrite each other's local selection even though the service enforces isolation.
5. **Last-writer-wins UserConfig:** concurrent azd processes can overwrite the current record or cursor; stale cursors can cause replay.
6. **Append-only recovery reset:** checkpointed text can repeat after the prominent reset marker.
7. **Text-oriented friendly rendering:** non-text events are tracked for cursors but are not necessarily displayed.
8. **No raw background lifecycle output:** `--output raw` is rejected for new operations.
9. **No structured invoke output:** scripts must use friendly output until JSON output is added.
10. **Responses lifecycle only:** background, cursor, reconnect, steering, and cancellation semantics apply only to Responses. Remote Invocations supports one best-effort GET using locally saved context; A2A remains unchanged.
11. **No total background timeout:** attached background commands can run indefinitely until completion, interruption, or reconnect failure.

## Future optimizations

### Graceful Ctrl-C detach

Coordinate azd core and extension signal handling so the first Ctrl-C persists the final cursor, prints detach guidance, and returns a distinct successful detach result while later interrupts retain force-exit behavior.

### Explicit response targeting

Add recovery and management of Responses outside the one locally saved current context:

```bash
azd ai agent invoke --continue --response-id resp_123
azd ai agent invoke --cancel --response-id resp_123
```

Define session, conversation, version, and user-identity validation when local metadata is missing.

### Multiple saved background Responses

Retain per-response session/conversation history so users can discover, reconnect to, and switch among older contexts. Turns in one conversation remain sequential.

### Explicit cursor override

After explicit response targeting and graceful detach make an exact cursor available to users, add:

```bash
azd ai agent invoke \
  --continue \
  --response-id resp_123 \
  --starting-after 42
```

The flag overrides local cursor state for debugging, scripting, and cross-machine continuation. Until then, azd manages the current Response cursor internally.

### Raw background streams

Tee SSE bytes through the parser while preserving a documented raw multi-connection format. Define framing for POST plus reconnect GET responses and keep friendly diagnostics off raw stdout.

### Structured output

Add `--output json` with stable fields for response ID, cursor, status, session, conversation, and reconnect guidance. Support automation without scraping friendly output.

### User-identity-aware local state

Scope sessions, conversations, and background Responses by a stable identity fingerprint so different `--user-identity` values do not overwrite local selection.

### Strong multi-process persistence

Replace whole-object UserConfig read-modify-write with file locking, compare-and-swap, or monotonic cursor merges. Define behavior for simultaneous processes and detect attempts to start competing turns.

### Cross-machine state

Build on explicit response targeting to export/import response context or retrieve enough server metadata to reconstruct it from a Response ID.

### TTY redraw

Replace previously rendered output after a recovery snapshot in interactive terminals while retaining the reset-marker behavior for logs and non-TTY output.

### Rich Responses rendering

Render tool calls, reasoning, refusals, annotations, images, and multiple output/content items while preserving cursor and snapshot correctness.

### Response management commands

If lifecycle operations outgrow `invoke`, consider dedicated list/show/follow/cancel commands without changing the MVP aliases.

### Rich Invocations-specific long-running support

The MVP blind GET requires no capability discovery because it makes no claim about lifecycle semantics. Polling, registration retry, streaming follow, replay, cancellation, steering, and terminal-state handling remain deferred until an application capability-discovery contract exists. Do not assume the Responses model applies to arbitrary Invocations agents.

## Implementation sequence

### PR 1 — Attached background Responses

1. Add `--background` validation and preserve existing protocol behavior.
2. Add the typed SSE decoder and saved Response state.
3. Add attached stored background creation and identity/cursor persistence.
4. Validate attached execution against the hosted Responses reference agent.

### PR 2 — Responses detach, reconnect, and cancel

1. Add `--no-wait` after identity capture.
2. Add message-free `--continue`, strict-after cursor replay, and automatic reconnect.
3. Add snapshot fallback, recovery reset rendering, and token refresh.
4. Add `--cancel` without stopping the hosted session.
5. Validate detach, reconnect, recovery, and cancel against the hosted Responses reference agent.

### PR 3 — Responses steering

1. Add message-bearing `--continue` for active replacement and terminal next-turn creation.
2. Refresh active status before blocking competing turns.
3. Validate repeated conversation-based steering and superseded terminal outcomes.
4. Validate steering against the hosted Responses reference agent.

### PR 4 — Invocations one-shot retrieval

1. Save the latest successful remote invocation ID, effective session, and API version.
2. Route message-free Invocations `--continue` to exactly one opaque GET.
3. Reject unsupported background, steering, replay, and cancellation combinations.
4. Validate one-shot retrieval against the hosted Invocations reference agent.

## References

- [PRD: Azure/azure-dev#9676](https://github.com/Azure/azure-dev/issues/9676)
- [Resilience for long-running Microsoft Foundry hosted agents](https://learn.microsoft.com/azure/foundry/agents/concepts/long-running-agent-resilience)
- [Stream long-running agent output with reconnect](https://learn.microsoft.com/azure/foundry/agents/how-to/stream-with-reconnect)
- [Steer an in-flight agent turn](https://learn.microsoft.com/azure/foundry/agents/how-to/steer-hosted-agent)
- [Long-running agent API reference](https://learn.microsoft.com/azure/foundry/agents/concepts/long-running-agent-reference)
