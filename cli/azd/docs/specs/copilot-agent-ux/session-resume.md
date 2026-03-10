# Plan: Session Resume Support

## SDK Capabilities

The SDK has full session resume support:

- **`client.ResumeSession(ctx, sessionID, config)`** — resumes a session with full conversation history
- **`client.ListSessions(ctx, filter)`** — lists sessions filterable by `cwd`, `gitRoot`, `repository`, `branch`
- **`SessionMetadata`** — has `SessionID`, `StartTime`, `ModifiedTime`, `Summary`, `Context`
- **`ResumeSessionConfig`** — accepts all the same options as `CreateSession` (MCP servers, skills, hooks, permissions, etc.)

Sessions persist in `~/.copilot/session-state/{uuid}/` and survive process crashes.

## Proposed Flow

### On `azd init` with agent mode:

```
1. Check for previous sessions in the current directory
   → client.ListSessions(ctx, &SessionListFilter{Cwd: cwd})
   
2. If previous sessions exist:
   → Show list with timestamps and summaries
   → Prompt: "Resume previous session or start fresh?"
     - "Resume: {summary} ({time ago})"
     - "Start a new session"
   
3. If resume chosen:
   → client.ResumeSession(ctx, selectedID, resumeConfig)
   → Agent has full conversation history from previous run
   → Continue with Q&A loop
   
4. If new session chosen (or no previous sessions):
   → client.CreateSession(ctx, sessionConfig) (current behavior)
   → Run init prompt
```

### Session ID Storage

**Option A (Recommended): Don't store — use ListSessions**
The SDK already stores sessions in `~/.copilot/session-state/`. `ListSessions` with `Cwd` filter finds sessions for the current directory. No need for azd to store the session ID separately.

**Option B: Store in `.azure/copilot-session.json`**
Save the session ID to a project-local file. Simpler lookup but requires file management.

**Recommendation:** Option A — the SDK handles everything. We just call `ListSessions` filtered by cwd.

## Files Changed

| File | Change |
|------|--------|
| `internal/agent/copilot_agent_factory.go` | Add `Resume()` method alongside `Create()` |
| `cmd/init.go` | Check for previous sessions, prompt to resume or start fresh |

## Key Consideration

`ResumeSessionConfig` accepts the same hooks, permissions, MCP servers, and skills as `CreateSession`. So a resumed session gets the same tool access as a new one.
