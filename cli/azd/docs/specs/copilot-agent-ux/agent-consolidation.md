# Plan: Consolidate Agent into Self-Contained Copilot Agent

## Goal

Simplify the agent into a single, self-contained `CopilotAgent` that encapsulates initialization, session management, display, and usage — created via `CopilotAgentFactory` for easy IoC injection.

## Target API

```go
// 1. Inject factory (via IoC)
type myAction struct {
    agentFactory *agent.CopilotAgentFactory
}

// 2. Create agent (with optional overrides)
copilotAgent, err := i.agentFactory.Create(ctx,
    agent.WithModel("claude-opus-4.6"),        // override model
    agent.WithReasoningEffort("high"),          // override reasoning
)

// 3. Initialize (prompts for model/reasoning if not configured, installs plugins)
initResult, err := copilotAgent.Initialize(ctx)  // or agent.WithForcePrompt()
// initResult has: Model, ReasoningEffort, IsFirstRun

// 4. Select a session (shows UX picker, returns selected or nil for new)
selectedSession, err := copilotAgent.SelectSession(ctx)

// 5. Send message (uses selected session if set)
result, err := copilotAgent.SendMessage(ctx, "Prepare this app for Azure")
// or with explicit session resume
result, err := copilotAgent.SendMessage(ctx, "Continue", agent.WithSessionID("abc-123"))
// or with retry
result, err := copilotAgent.SendMessageWithRetry(ctx, "Prepare this app for Azure")

// result has: Content, SessionID, Usage{InputTokens, OutputTokens, Cost, ...}

// 6. Stop
copilotAgent.Stop()
```

## Changes

### New/Modified Files

| File | Change |
|------|--------|
| `internal/agent/copilot_agent.go` | **Major rewrite** — self-contained agent with Initialize, ListSessions, SendMessage returning `AgentResult` |
| `internal/agent/types.go` | **New** — `AgentResult`, `InitResult`, `UsageMetrics`, `SendOptions` structs |
| `cmd/init.go` | **Simplify** — use new agent API, remove `configureAgentModel()` |
| `cmd/container.go` | **Simplify** — remove old AgentFactory registration |

### Files to Delete (Dead Code)

| File | Reason |
|------|--------|
| `internal/agent/agent.go` | Old `agentBase`, `Agent` interface, langchaingo options |
| `internal/agent/agent_factory.go` | Old `AgentFactory` with langchaingo delegation |
| `internal/agent/conversational_agent.go` | Old `ConversationalAzdAiAgent` with langchaingo executor |
| `internal/agent/prompts/conversational.txt` | Old ReAct prompt template |
| `internal/agent/tools/common/utils.go` | `ToLangChainTools()` — no longer needed |
| `pkg/llm/azure_openai.go` | Old provider — SDK handles models |
| `pkg/llm/ollama.go` | Old provider — no offline mode |
| `pkg/llm/github_copilot.go` | Old build-gated provider — SDK handles auth |
| `pkg/llm/model.go` | `modelWithCallOptions` wrapper — unnecessary |
| `pkg/llm/model_factory.go` | Old `ModelFactory` — unnecessary |
| `pkg/llm/copilot_provider.go` | Marker provider — agent handles directly |
| `internal/agent/logging/thought_logger.go` | Old langchaingo callback handler |
| `internal/agent/logging/file_logger.go` | Old langchaingo callback handler |
| `internal/agent/logging/chained_handler.go` | Old langchaingo callback handler |
| `internal/agent/tools/common/types.go` | Old `AnnotatedTool` interface with langchaingo embedding |

### New Types (`internal/agent/types.go`)

```go
// AgentResult is returned by SendMessage with response content and metrics.
type AgentResult struct {
    Content   string       // Final assistant message
    SessionID string       // Session ID for resume
    Usage     UsageMetrics // Token/cost metrics
}

// UsageMetrics tracks resource consumption for a session.
type UsageMetrics struct {
    Model           string
    InputTokens     float64
    OutputTokens    float64
    TotalTokens     float64
    Cost            float64
    PremiumRequests float64
    DurationMS      float64
}

// InitResult is returned by Initialize with configuration state.
type InitResult struct {
    Model           string
    ReasoningEffort string
    IsFirstRun      bool // true if user was prompted
}

// SendOptions configures a SendMessage call.
type SendOptions struct {
    SessionID string // Resume this session (empty = new session)
    Mode      string // "interactive" (default), "autopilot", "plan"
}
```

### Simplified `CopilotAgentFactory`

Factory stays as the IoC-friendly constructor. Injects all dependencies, returns agent.

```go
type CopilotAgentFactory struct {
    clientManager        *llm.CopilotClientManager
    sessionConfigBuilder *llm.SessionConfigBuilder
    consentManager       consent.ConsentManager
    console              input.Console
    configManager        config.UserConfigManager
}

// AgentOption configures agent creation.
type AgentOption func(*CopilotAgent)

func WithModel(model string) AgentOption              // override configured model
func WithReasoningEffort(effort string) AgentOption    // override configured reasoning
func WithMode(mode string) AgentOption                 // "interactive", "autopilot", "plan"
func WithDebug(debug bool) AgentOption

// Create builds a new CopilotAgent with all dependencies wired.
func (f *CopilotAgentFactory) Create(ctx context.Context, opts ...AgentOption) (*CopilotAgent, error)
```

### Simplified `CopilotAgent`

Agent owns its full lifecycle — initialize, session selection, display, usage.

```go
type CopilotAgent struct {
    // Dependencies (from factory)
    clientManager        *llm.CopilotClientManager
    sessionConfigBuilder *llm.SessionConfigBuilder
    consentManager       consent.ConsentManager
    console              input.Console
    configManager        config.UserConfigManager
    
    // Overrides (from AgentOption)
    modelOverride           string
    reasoningEffortOverride string
    modeOverride            string  // "interactive", "autopilot", "plan"
    
    // Runtime state
    session     *copilot.Session
    sessionID   string
    display     *AgentDisplay
    fileLogger  *logging.SessionFileLogger
}

// Initialize handles first-run config (model/reasoning prompts), plugin install,
// and client startup. Returns current config. Use WithForcePrompt() to always prompt.
func (a *CopilotAgent) Initialize(ctx, ...InitOption) (*InitResult, error)

// SelectSession shows a UX picker with previous sessions for the cwd.
// Returns the selected session metadata, or nil if user chose "new session".
func (a *CopilotAgent) SelectSession(ctx) (*SessionMetadata, error)

// ListSessions returns previous sessions for the given working directory.
func (a *CopilotAgent) ListSessions(ctx, cwd) ([]SessionMetadata, error)

// SendMessage sends a prompt and returns the result with content, session ID, and usage.
// Creates a new session or resumes one if WithSessionID() is provided or SelectSession() was called.
func (a *CopilotAgent) SendMessage(ctx, prompt, ...SendOption) (*AgentResult, error)

// SendMessageWithRetry wraps SendMessage with interactive retry-on-error UX.
func (a *CopilotAgent) SendMessageWithRetry(ctx, prompt, ...SendOption) (*AgentResult, error)

func (a *CopilotAgent) Stop() error
```

### Simplified `init.go`

```go
type initAction struct {
    agentFactory *agent.CopilotAgentFactory  // injected via IoC
    console      input.Console
    // ... other fields
}

func (i *initAction) initAppWithAgent(ctx context.Context) error {
    // Show alpha warning
    i.console.MessageUxItem(ctx, &ux.MessageTitle{...})

    // Create agent
    copilotAgent, err := i.agentFactory.Create(ctx,
        agent.WithMode("interactive"),
        agent.WithDebug(i.flags.global.EnableDebugLogging),
    )
    if err != nil {
        return err
    }
    defer copilotAgent.Stop()

    // Initialize — prompts on first run, shows config on subsequent
    initResult, err := copilotAgent.Initialize(ctx)
    if err != nil {
        return err
    }

    // Show current config
    i.console.Message(ctx, output.WithGrayFormat("  Agent: model=%s, reasoning=%s",
        initResult.Model, initResult.ReasoningEffort))

    // Session picker — resume previous or start fresh
    selected, err := copilotAgent.SelectSession(ctx)
    if err != nil {
        return err
    }

    // Build send options
    opts := []agent.SendOption{}
    if selected != nil {
        opts = append(opts, agent.WithSessionID(selected.SessionID))
    }

    // Send init prompt
    result, err := copilotAgent.SendMessageWithRetry(ctx, initPrompt, opts...)
    if err != nil {
        return err
    }

    // Show summary
    i.console.Message(ctx, "")
    i.console.Message(ctx, color.HiMagentaString("◆ Azure Init Summary:"))
    i.console.Message(ctx, output.WithMarkdown(result.Content))

    // Show usage
    if usage := result.Usage.Format(); usage != "" {
        i.console.Message(ctx, "")
        i.console.Message(ctx, usage)
    }

    return nil
}
```

The init flow is now ~40 lines instead of ~150. All agent internals (display, plugins, MCP, consent, permissions) are encapsulated.

## Execution Order

1. Create `types.go` with result structs
2. Rewrite `copilot_agent.go` with self-contained API
3. Merge factory logic into agent (Initialize handles plugins/client/session)
4. Update `init.go` to use new API
5. Update `cmd/container.go` to register new agent
6. Update `cmd/middleware/error.go` to use new agent
7. Delete all dead code files
8. Remove langchaingo from `go.mod`
