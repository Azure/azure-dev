---
applyTo: "**/*.go"
---
# Modern Go (1.26+) PR review guidelines

This project uses **Go 1.26** (`cli/azd/go.mod`). Do not flag modern Go 1.26
features as errors. The authoritative [modern Go conventions](../../cli/azd/AGENTS.md#modern-go)
include `new(expr)`, `errors.AsType`, integer ranges, `t.Context`, `t.Chdir`,
`wg.Go`, and the `min`, `max`, and `clear` built-ins. In particular,
`new(false)`, `new(true)`, `new(0)`, and `new("s")` are valid; do not suggest
pointer helpers or address-of-local replacements for them.

## Review the full file, not just the diff

Before flagging missing imports or undefined references, verify the symbol isn't
already defined in unchanged portions of the file. The diff context may not show
all existing imports or declarations.

## CLI behavior and domain filtering

- When reviewing command input resolution, explicit CLI args and flags should win over defaults. Do not prompt the user toward a different default when they provided a valid new value; reserve prompts for ambiguous choices and preserve deterministic `--no-prompt` behavior for CI/scripts.
- When reviewing flag validation: if a flag was explicitly set (verifiable via `cmd.Flags().Changed("<name>")`) but the code path would silently ignore it, that is a bug. Reject the combination with a clear error — "success with dropped flags" breaks automation scripts.
- When filtering AI models or quota data by location, keep location-specific usage data associated with only the models available in that location. Empty or unknown usage data from an unrelated location must not make a model eligible elsewhere; add regression coverage for cross-location quota cases.
