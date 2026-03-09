# Consolidated Copilot Agent UX Renderer

## Problem Statement

The current CopilotAgent UX is a thin port of the langchaingo thought-channel pattern. It only handles 3 of 50+ SDK event types, uses an intermediate `Thought` struct channel that loses information, and can't render streaming tokens, tool completion results, errors, or turn boundaries.

We need a **direct event-driven UX renderer** that subscribes to `session.On()` and renders all relevant event types using azd's existing UX components (Canvas, Spinner, Console, output formatters).

## Approach

Replace the `Thought` channel indirection with a single `AgentDisplay` component that:
1. Subscribes directly to SDK `SessionEvent` stream via `session.On()`
2. Manages a Canvas with Spinner + dynamic VisualElement layers
3. Handles all event types with appropriate UX rendering
4. Exposes `Start()`/`Stop()` lifecycle matching the `SendMessage` call boundary

## Architecture

```
session.On(agentDisplay.HandleEvent)
    │
    ├── assistant.turn_start     → Show spinner "Processing..."
    ├── assistant.intent         → Update spinner with intent text
    ├── assistant.reasoning      → Show thinking text (gray)
    ├── assistant.message_delta  → Stream tokens to response area
    ├── assistant.message        → Finalize response text
    ├── tool.execution_start     → Update spinner "Running {tool}..."
    ├── tool.execution_progress  → Update spinner with progress
    ├── tool.execution_complete  → Print "✔ Ran {tool}" completion line
    ├── session.error            → Print error in red
    ├── session.idle             → Signal turn complete
    ├── skill.invoked            → Show skill badge
    ├── assistant.turn_end       → Clear spinner
    └── (all others)             → Log to file only
```

## New Components

### 1. `AgentDisplay` — replaces thought channel + renderThoughts

```go
// internal/agent/display.go

type AgentDisplay struct {
    console      input.Console
    canvas       ux.Canvas
    spinner      *ux.Spinner

    // State
    mu               sync.Mutex
    latestThought    string
    currentTool      string
    currentToolInput string
    toolStartTime    time.Time
    streaming        strings.Builder  // accumulates message_delta content
    finalContent     string           // set on assistant.message

    // Lifecycle
    idleCh    chan struct{}
    cancelCtx context.CancelFunc
}
```

**Methods:**
- `NewAgentDisplay(console) *AgentDisplay` — constructor
- `Start(ctx) (cleanup func(), err error)` — creates canvas, starts render goroutine
- `HandleEvent(event copilot.SessionEvent)` — main event dispatcher (called by SDK)
- `WaitForIdle(ctx) (string, error)` — blocks until session.idle, returns final message
- `Stop()` — cleanup

### 2. Event Handling (inside `HandleEvent`)

| Event | UX Action |
|-------|-----------|
| `assistant.turn_start` | Start spinner "Processing..." |
| `assistant.intent` | Update spinner "◆ {intent}" |
| `assistant.reasoning` / `reasoning_delta` | Show gray thinking text below spinner |
| `assistant.message_delta` | Append to streaming buffer, show in visual element |
| `assistant.message` | Store final content, clear streaming buffer |
| `tool.execution_start` | Print previous tool completion, update spinner "Running {tool} with {input}..." with elapsed timer |
| `tool.execution_progress` | Update spinner with progress message |
| `tool.execution_complete` | Print "✔ Ran {tool}" with result summary |
| `session.error` | Print error in red via console.Message |
| `session.warning` | Print warning in yellow |
| `session.idle` | Signal idleCh, clear canvas |
| `assistant.turn_end` | Print final tool completion |
| `skill.invoked` | Print "◇ Using {skill}" |
| `subagent.started` | Print "◆ Delegating to {agent}" |

### 3. Simplified `CopilotAgent.SendMessage`

```go
func (a *CopilotAgent) SendMessage(ctx context.Context, args ...string) (string, error) {
    display := NewAgentDisplay(a.console)
    cleanup, err := display.Start(ctx)
    if err != nil {
        return "", err
    }
    defer cleanup()

    prompt := strings.Join(args, "\n")

    // Subscribe display to session events
    unsubscribe := a.session.On(display.HandleEvent)
    defer unsubscribe()

    // Send prompt (non-blocking)
    _, err = a.session.Send(ctx, copilot.MessageOptions{Prompt: prompt})
    if err != nil {
        return "", err
    }

    // Wait for idle — display handles all UX rendering
    return display.WaitForIdle(ctx)
}
```

No more thought channel, no more separate event logger for UX. The file logger remains separate for audit logging.

## Files Changed

| File | Change |
|------|--------|
| `internal/agent/display.go` | **New** — AgentDisplay component |
| `internal/agent/copilot_agent.go` | **Modify** — Remove renderThoughts, thoughtChan; use AgentDisplay |
| `internal/agent/copilot_agent_factory.go` | **Modify** — Remove thought channel setup; keep file logger |
| `internal/agent/logging/session_event_handler.go` | **Modify** — Remove SessionEventLogger (replaced by AgentDisplay); keep SessionFileLogger and CompositeEventHandler |

## What's Preserved

- **`SessionFileLogger`** — continues logging all events to daily file for audit
- **`Canvas` + `Spinner` + `VisualElement`** — same azd UX component stack
- **Tool completion format** — "✔ Ran {tool} with {input}" (green check + magenta tool name)
- **`Console.Message`** — for errors, warnings, markdown output
- **`output.*Format()`** — WithErrorFormat, WithWarningFormat, WithHighLightFormat, WithMarkdown
- **File watcher** (`PrintChangedFiles`) — called after each SendMessage

## What's Removed

- `Thought` struct and thought channel (from CopilotAgent path only — langchaingo agent keeps its own)
- `SessionEventLogger` (UX thought channel part — file logger stays)
- `renderThoughts()` goroutine in CopilotAgent
- `WithCopilotThoughtChannel` option

## Key Design Decisions

### 1. Direct event subscription instead of channel indirection
The `Thought` channel flattened rich SDK events into `{Thought, Action, ActionInput}` strings, losing context like tool results, error types, streaming deltas, and skill/subagent info. Direct `session.On()` subscription gives us the full `SessionEvent` data.

### 2. AgentDisplay owns canvas lifecycle per SendMessage call
Each `SendMessage` creates a fresh `AgentDisplay` → canvas → spinner, and tears them down on idle. This ensures clean state between agent turns and prevents canvas artifacts from previous turns.

### 3. Streaming support via `message_delta` events
With `SessionConfig.Streaming: true` (already set), the SDK emits `assistant.message_delta` events with incremental tokens. `AgentDisplay` accumulates these in a `strings.Builder` and renders progressively — significantly improving perceived responsiveness.

### 4. File logger remains separate
`SessionFileLogger` handles ALL events for audit/debugging. `AgentDisplay` only handles UX-relevant events. They're both registered via `session.On()` — no coupling between them.

### 5. Event handler is synchronous
SDK calls `session.On()` handlers synchronously in registration order. Canvas updates within `HandleEvent` are serialized naturally — no race conditions, no mutex needed for canvas operations (only for shared state like `latestThought`).

## UX Component Composition

```
Canvas
├── Spinner
│   └── Dynamic text: "Processing..." / "Running {tool} with {input}... (5s)"
└── VisualElement (thinking display)
    └── Gray text: latest reasoning/thought content

+ Console.Message (printed outside canvas):
  ├── "✔ Ran {tool} with {input}" — tool completions
  ├── "◇ Using {skill}" — skill invocations
  ├── "◆ Delegating to {agent}" — subagent handoffs
  ├── Red error messages
  └── Yellow warning messages
```

## Existing UX Components Used

| Component | From | Purpose |
|-----------|------|---------|
| `ux.NewSpinner()` | `pkg/ux` | Animated loading indicator with dynamic text |
| `ux.NewCanvas()` | `pkg/ux` | Container composing spinner + visual elements |
| `ux.NewVisualElement()` | `pkg/ux` | Custom render function for thinking display |
| `console.Message()` | `pkg/input` | Static messages (completions, errors) |
| `output.WithErrorFormat()` | `pkg/output` | Red error formatting |
| `output.WithWarningFormat()` | `pkg/output` | Yellow warning formatting |
| `output.WithMarkdown()` | `pkg/output` | Glamour-rendered markdown |
| `color.GreenString()` | `fatih/color` | Green "✔" check marks |
| `color.MagentaString()` | `fatih/color` | Magenta tool/agent names |
| `color.HiBlackString()` | `fatih/color` | Gray thinking/input text |
