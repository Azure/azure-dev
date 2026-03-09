# Plan: Simplify init.go Agent Flow with Skills + UserInput Handler

## Problem Statement

The current `initAppWithAgent()` runs 6 sequential steps with hardcoded prompts, inter-step feedback loops, and manual orchestration. This can be replaced with a single prompt that delegates to the `azure-prepare` and `azure-validate` skills from the Azure plugin. The agent needs to be able to ask the user questions during execution via azd's existing UX prompts.

## Current State (6-step flow in init.go)

```
Step 1: Discovery & Analysis     → agent.SendMessageWithRetry(prompt)
        ↓ collectAndApplyFeedback("Any changes?")
Step 2: Architecture Planning    → agent.SendMessageWithRetry(prompt)
        ↓ collectAndApplyFeedback("Any changes?")
Step 3: Dockerfile Generation    → agent.SendMessageWithRetry(prompt)
        ↓ collectAndApplyFeedback("Any changes?")
Step 4: Infrastructure (IaC)     → agent.SendMessageWithRetry(prompt)
        ↓ collectAndApplyFeedback("Any changes?")
Step 5: azure.yaml Generation   → agent.SendMessageWithRetry(prompt)
        ↓ collectAndApplyFeedback("Any changes?")
Step 6: Project Validation       → agent.SendMessageWithRetry(prompt)
        ↓ postCompletionSummary()
```

~150 lines of step definitions, feedback loops, and summary aggregation.

## Target State (single prompt)

```
agent.SendMessageWithRetry(
    "Prepare this application for deployment to Azure.
     Use the azure-prepare skill to analyze the project, generate infrastructure,
     Dockerfiles, and azure.yaml. Then use the azure-validate skill to verify
     everything is ready for deployment.
     Ask the user for input when you need clarification about architecture
     choices, service selection, or configuration options."
)
```

The skills handle all the orchestration internally. The agent can ask the user questions via the SDK's `ask_user` tool.

## Key Components

### 1. Wire `OnUserInputRequest` handler

The SDK has built-in support for the agent to ask the user questions. When `OnUserInputRequest` is set on `SessionConfig`, the agent gets an `ask_user` tool. When invoked, our handler renders the question using azd's UX components.

```go
// In CopilotAgentFactory — wire to SessionConfig
sessionConfig.OnUserInputRequest = func(
    req copilot.UserInputRequest,
    inv copilot.UserInputInvocation,
) (copilot.UserInputResponse, error) {
    if len(req.Choices) > 0 {
        // Multiple choice — use azd Select prompt
        selector := ux.NewSelect(&ux.SelectOptions{
            Message: req.Question,
            Choices: toSelectChoices(req.Choices),
        })
        idx, err := selector.Ask(ctx)
        return copilot.UserInputResponse{Answer: req.Choices[*idx]}, err
    }

    // Freeform — use azd Prompt
    prompt := ux.NewPrompt(&ux.PromptOptions{
        Message: req.Question,
    })
    answer, err := prompt.Ask(ctx)
    return copilot.UserInputResponse{Answer: answer, WasFreeform: true}, err
}
```

**SDK types:**
- `UserInputRequest{Question string, Choices []string, AllowFreeform *bool}`
- `UserInputResponse{Answer string, WasFreeform bool}`

### 2. Simplify `initAppWithAgent()`

Replace the 6-step loop with a single prompt. Remove:
- `initStep` struct and step definitions
- `collectAndApplyFeedback()` between steps
- `postCompletionSummary()` aggregation
- Step-by-step summary display

The agent's response IS the summary.

### 3. AgentDisplay handles all UX

The `AgentDisplay` already renders tool execution, reasoning, errors, and completion. The new `OnUserInputRequest` handler adds interactive questioning. No other UX changes needed.

## Files Changed

| File | Change |
|------|--------|
| `internal/agent/copilot_agent_factory.go` | **Modify** — Wire `OnUserInputRequest` handler using azd UX prompts |
| `cmd/init.go` | **Modify** — Replace 6-step loop with single prompt; remove step definitions, feedback loops, summary aggregation |

## What's Removed from init.go

- `initStep` struct (~5 lines)
- 6 step definitions (~30 lines)
- Step loop with feedback collection (~50 lines)
- `collectAndApplyFeedback()` call sites (~15 lines)
- `postCompletionSummary()` function (~30 lines)
- Step summary display logic (~10 lines)
- **Total: ~140 lines removed**

## What's Preserved

- Alpha warning display
- Consent check before agent mode starts
- `azdAgent.SendMessageWithRetry()` — retry-on-error UX
- File watcher (PrintChangedFiles)
- Error handling and context cancellation
- The prompt text (simplified to single prompt referencing skills)

## Design Decisions

### Why `OnUserInputRequest` instead of a custom tool?
The SDK already has a built-in `ask_user` tool that's enabled when `OnUserInputRequest` is set. The agent knows how to use it natively — no custom tool definition needed. Our handler just renders the question using azd's Select/Prompt/Confirm components instead of the CLI's TUI (which doesn't exist in headless mode).

### Why not keep the feedback loop?
The skills (`azure-prepare`, `azure-validate`) have their own internal orchestration. They decide when to ask the user questions (via `ask_user`) and when to proceed. The inter-step feedback loop was needed because the old langchaingo agent had no way to ask questions mid-execution. With `OnUserInputRequest`, the agent can ask whenever it needs to.

### Prompt design
The single prompt references the skills by name. The Copilot CLI auto-discovers installed plugin skills, so the agent can invoke `azure-prepare` and `azure-validate` directly. The prompt just needs to state the goal — the skills handle the "how".
