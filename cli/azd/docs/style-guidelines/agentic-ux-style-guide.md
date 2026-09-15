# Azure Developer CLI (`azd`) Agentic UX Style Guide

## Overview

This guide defines UX patterns for the Azure Developer CLI's **agentic GitHub Copilot experience**.

> **Keep agentic and core styling separate.** Core success, failure, and warning statuses keep their standard colors and prefixes. Agent identity, tool activity, and thinking states use the magenta styling and symbols defined here. Agent prompts continue to use the core blue `?` marker for prompts.

## Scope and Entry Points

The agentic experience starts when a user selects **"Set up with GitHub Copilot (Preview)"** during `azd init` and continues through the internal Copilot session runtime.

## Colors and Symbols

Magenta identifies the agent or active AI work. Agent identity currently uses `color.MagentaString`; the shared `WithHintFormat` helper is also magenta but remains valid for hints in any flow.

| Event or element | Required rendering | Implementation |
| --- | --- | --- |
| Spinner | Animated glyph in magenta via `WithHintFormat` | `pkg/ux/spinner.go` |
| Tool running | `Running <tool>`; tool name magenta, summary gray | `SessionEventTypeToolExecutionStart` |
| Tool succeeded | Green `✔︎`, contextual verb, optional gray detail; `powershell` and generic tool names are magenta | `printToolState()` and `toolVerb()` |
| Tool failed | Red `✖`, magenta tool name, optional red error on a `└` line | `printToolState()` |
| Subagent started | Magenta `◆ <name>` with an optional gray description | `SessionEventTypeSubagentStarted` |
| Subagent succeeded | Green `✔︎`, magenta name, `completed` | `SessionEventTypeSubagentCompleted` |
| Subagent failed | Entire `✖ <name> failed: <error>` line in red | `SessionEventTypeSubagentFailed` |
| Skill invoked | Cyan `◇ Using skill: <name>` with optional gray `from <plugin>@<version>` | `SessionEventTypeSkillInvoked` |
| Nested detail | Gray `├` / `└` lines for commands and MCP arguments | `AgentDisplay` |
| Init preparation | `Preparing application for Azure deployment...` in magenta | `cmd/init.go` |

Use direct magenta only for agent identity and active AI work, not arbitrary text. Keep core progress prefixes out of agent events: use `✔︎ Ran powershell`, not `(✓) Ran powershell`.

## Rendering and Interaction

- Render assistant messages as terminal markdown with `output.WithMarkdown(...)`.
- Indent nested subagent tool calls two spaces.
- Use `Thinking...` instead of the shared spinner's default `Loading...`. Show up to the last five streamed reasoning lines above it in dim gray (`color.HiBlackString`).
- `AgentDisplay.Start` draws a `uxlib.Canvas` in this order: reasoning → blank line → spinner → gray detail tree → gray `Press Ctrl+C to cancel` with bold `Ctrl+C`. Completed events persist above the canvas while the transient region is redrawn.
- Reuse the core prompt component in [`pkg/ux/prompt.go`](../../pkg/ux/prompt.go). Prompts keep the blue `?` marker; do not use a magenta prompt marker.

## Output Examples

**Thinking / reasoning + spinner** (spinner glyph is the default `|` `/` `-` `\` animation, rendered magenta):

```
  ...streamed reasoning text (dim gray)...

  / Thinking...
  Press Ctrl+C to cancel
```

**Tool run and completion:** The spinner shows a concise summary. A completed PowerShell tool may show the actual command on a gray tree line.

```
Running powershell Run tests...
✔︎ Ran powershell Run tests
  └ go test ./...
```

**Subagent lifecycle:**

```
◆ GitHub Copilot
  └ Generates Azure app scaffolding
✔︎ GitHub Copilot completed
```
