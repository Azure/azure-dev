# Azure Developer CLI (`azd`) Agentic UX Style Guide

## Overview

This guide covers the **agentic (AI / GitHub Copilot) UX patterns** for the Azure Developer CLI. These are a **deliberately distinct visual system** from the core azd flows documented in [azd-style-guide.md](./azd-style-guide.md).

> **Scope**: This guide is for the AI-driven / agentic experience only. For core azd terminal UX (progress reports, spinners, colors, prompts, responsive list/table layouts) see [azd-style-guide.md](./azd-style-guide.md). For extension-specific UX see [extensions-style-guide.md](../extensions/extensions-style-guide.md).

> **Do not blend the two systems.** Core azd status colors (green `(✓)`, red `(x)`, yellow `(!)`) still mean success/failure/warning inside agentic output, but the agent's own identity, tool activity, and thinking states use the magenta + glyph vocabulary described here. Never use magenta for core-flow status, and never restyle core progress reports with the agentic glyphs. Note that agentic **prompts deliberately reuse the core prompt conventions** (the blue `?` marker); it is only the agent's own status/output that uses the agentic vocabulary.

## Agentic (AI / GitHub Copilot) UX Patterns

azd has an **agentic experience** — the AI-driven flow entered when a user picks **"Set up with GitHub Copilot (Preview)"** during `azd init`, and used by the `azd agent` / copilot session runtime. This experience has a **deliberately distinct visual language** from core azd flows. Its signature is **magenta**, which marks AI/agent identity and active AI work.

> **Do not blend the two systems.** Core azd status colors (green `(✓)`, red `(x)`, yellow `(!)`) still mean success/failure/warning inside agentic output, but the agent's own identity, tool activity, and thinking states use the magenta + glyph vocabulary described here. Never use magenta for core-flow status, and never restyle core progress reports with the agentic glyphs.

- Entry point (init branch): [`cmd/init.go`](../../cmd/init.go) — `promptInitType()` adds the Copilot option and `initAppWithAgent()` runs the flow.
- Agent display/event renderer: [`internal/agent/display.go`](../../internal/agent/display.go) (`AgentDisplay`).
- Branding constant: `DisplayTitle = "GitHub Copilot"` in `internal/agent/copilot/config_keys.go`.

### Branding & Naming

- Use **"GitHub Copilot"** for the user-facing name (via the `DisplayTitle` constant — don't hardcode the string). The `azd init` menu item is **"Set up with GitHub Copilot (Preview)"**.
- The experience is in **preview**: the entry flow prints a preview notice (with a link to `https://aka.ms/azd-feature-stages`) and tells users how to change permissions later via `azd copilot consent`. Keep this notice when touching the flow.

### Magenta: The Agentic Signature Color

There is **no `WithMagentaFormat` helper** in `pkg/output/colors.go`. The only magenta-mapped helper is `WithHintFormat` (`color.MagentaString`), which is a **shared** hint color used in both core and agentic flows (spinner glyph, prompt hints, next-step messages) — its magenta is *not* exclusive to agentic UX. The agentic **agent-identity** styling is different: it applies magenta **directly** through `color.MagentaString` from `fatih/color` to agent/tool/subagent names and in-progress AI messages. When flagging violations, distinguish shared `WithHintFormat` hints (allowed anywhere) from direct `color.MagentaString` agent-identity styling (agentic only).

Magenta agent-identity styling semantically means **"this is the AI agent / AI is actively working"**. It is applied to:

| Element                      | Rendering                                              | Location |
| ---------------------------- | ----------------------------------------------------- | -------- |
| Spinner glyph                | Animated glyph in magenta via `WithHintFormat`         | `pkg/ux/spinner.go` |
| Running tool name            | `Running <toolName>` — tool name magenta               | `internal/agent/display.go` (`SessionEventTypeToolExecutionStarted`) |
| Tool completion (verb)       | `✔︎ Ran <tool>` — `<tool>` magenta for `powershell`/generic tools | `toolVerb()` in `display.go` |
| Failed tool name             | `✖ <toolName>` — name magenta (glyph red)              | `printToolState()` in `display.go` |
| Subagent banner              | `◆ <subagent name>` in magenta                         | `SessionEventTypeSubagentStarted` |
| Subagent completed name      | `✔︎ <subagent> completed` — name magenta (check green) | `SessionEventTypeSubagentCompleted` |
| Init "preparing" message     | `Preparing application for Azure deployment...` magenta | `cmd/init.go` |

Reserve **direct `color.MagentaString`** styling for these agent-identity / active-AI-work cases. Do not magenta-color arbitrary text. (Shared `WithHintFormat` hints are a separate, allowed use of magenta.)

### Agentic Glyph & Layout Vocabulary

The agent renderer uses a distinct glyph set (not the core `(✓)/(x)/(!)` prefixes):

| Glyph | Meaning                        | Color                                   |
| ----- | ------------------------------ | --------------------------------------- |
| `◆`   | Subagent started               | Magenta                                 |
| `◇`   | Skill invoked (`◇ Using skill: <name>`) | Cyan (`color.CyanString`); optional gray `from <plugin>@<version>` |
| `✔︎`  | Tool / subagent succeeded      | Green check; label follows (verb or name) |
| `✖`   | **Tool** failed                | Red glyph + magenta tool name; optional error detail in red on a `└` line |
| `✖`   | **Subagent** failed            | Entire single line rendered red (`✖ <name> failed: <error>`) |
| `├` / `└` | Sub-detail / nested tree lines (shell command, MCP args, error) | Gray (`color.HiBlackString`) |

- **Nested (subagent) tool calls are indented** two spaces to show hierarchy.
- **Assistant messages** are rendered as terminal markdown via `output.WithMarkdown(...)`, not plain text.
- **Reasoning / "thinking"**: `AgentDisplay` initializes the spinner with the text `Thinking...` (overriding the shared spinner's default `Loading...`); streamed reasoning shows above the spinner in **dim gray** (`color.HiBlackString`), truncated to the last five lines.
- **Cancel affordance**: the live canvas shows `Press Ctrl+C to cancel` in gray with a bold `Ctrl+C`.

### The Agent Display Canvas

Live agent output is drawn on a `uxlib.Canvas` (`AgentDisplay.Start`) composed, top to bottom, of: recent reasoning (dim gray) → blank separator → spinner (magenta glyph + status text) → sub-detail tree (`├`/`└`, gray) → `Press Ctrl+C to cancel`. Completed events (`◆`/`◇`/`✔︎`/`✖`) are printed above the canvas and persist; the transient spinner/reasoning region is cleared and redrawn.

### Prompts in Agentic Flows

Agentic flows **reuse the core prompt component** ([`pkg/ux/prompt.go`](../../pkg/ux/prompt.go)), so user prompts still use the **blue `?`** marker described in [User Inputs](./azd-style-guide.md#user-inputs). **Agent prompts do not use magenta markers.** Magenta is reserved for agent identity and in-progress AI work, not for soliciting input. When adding prompts inside an agentic flow, follow the core User Inputs rules — do not restyle the `?` marker.

### Agentic Output Examples

**Thinking / reasoning + spinner** (spinner glyph is the default `|` `/` `-` `\` animation, rendered magenta):

```
  ...streamed reasoning text (dim gray)...

  / Thinking...
  Press Ctrl+C to cancel
```

**Tool run and completion:**

```
Running powershell go build...
✔︎ Ran powershell
```

**Subagent lifecycle:**

```
◆ GitHub Copilot
  └ Generates Azure app scaffolding
✔︎ GitHub Copilot completed
```

> Colors: `◆` and the subagent/tool names → magenta (`color.MagentaString`). `✔︎` → green. `✖` and error detail → red. Reasoning, sub-detail (`├`/`└`), and hints → gray. `◇ Using skill:` → cyan. Assistant prose → `WithMarkdown`.

### Agentic UX Guidelines

- **Keep the two systems separate.** Don't apply core progress-report prefixes to agent events, and don't apply agentic glyphs/magenta to core commands.
- **Direct magenta = agent identity / active AI work.** Use `color.MagentaString` for agent/tool/subagent names and in-progress AI messages — not for arbitrary text. (The shared `WithHintFormat` hint color is separate and allowed in any flow.)
- **Reuse the shared prompt and spinner components** — don't fork a magenta prompt marker.
- **Preserve the preview notice and consent guidance** in the init entry flow.
- **Render assistant messages as markdown** (`output.WithMarkdown`) so formatting is legible in the terminal.
