applyTo:
  - "**/*.go"
---
# Modern Go (1.26+) — PR Review Guidelines

This project uses **Go 1.26** (`cli/azd/go.mod`). Do not flag modern Go 1.26
features as errors.

## `new(expr)` creates typed pointers from values

`new(false)`, `new(true)`, `new(0)`, `new("s")` are **valid Go 1.26**. They
create a pointer to the given value. This replaces helper functions like
`to.Ptr(val)`. Do NOT suggest `boolPtr()` or `&localVar` replacements.

## Other modern patterns to accept (not flag)

- `errors.AsType[*T](err)` — generic error unwrapping (replaces `var e *T; errors.As(err, &e)`)
- `for i := range n` — range over integers
- `t.Context()` — test context (replaces `context.Background()` in tests)
- `t.Chdir(dir)` — test directory change (replaces `os.Chdir` + deferred restore)
- `wg.Go(func() { ... })` — WaitGroup shorthand (replaces `wg.Add(1); go func() { defer wg.Done(); ... }()`)
- `min()`, `max()`, `clear()` — built-in functions

## Review the full file, not just the diff

Before flagging missing imports or undefined references, verify the symbol isn't
already defined in unchanged portions of the file. The diff context may not show
all existing imports or declarations.

## CLI behavior and domain filtering

- When reviewing command input resolution, explicit CLI args and flags should win over defaults. Do not prompt the user toward a different default when they provided a valid new value; reserve prompts for ambiguous choices and preserve deterministic `--no-prompt` behavior for CI/scripts.
- When filtering AI models or quota data by location, keep location-specific usage data associated with only the models available in that location. Empty or unknown usage data from an unrelated location must not make a model eligible elsewhere; add regression coverage for cross-location quota cases.

## Telemetry span tests and feature matrix accuracy

- When adding or testing telemetry fields/events, write **span-recorder assertions** that verify
  the attribute was actually emitted on the span — do not rely solely on RPC status or response
  payload assertions. Tests that only check a response field pass even if the span attribute is
  removed entirely. Use an in-memory OTel span recorder and assert the specific attribute key and
  value on the correct span.
- `feature-telemetry-matrix.md` must accurately distinguish two coverage columns: mark
  **Command-Specific Attrs** ✅ only when the command sets an attribute on its span; mark
  **Feature Events** ✅ only when a dedicated named OTel event is emitted — not when an attribute
  is merely set on the command span. A command that only sets `extension.source.category` on its
  span has Command-Specific Attrs ✅ but Feature Events ❌.
- Telemetry fields added in a PR must be documented in `docs/reference/telemetry-data.md` (under
  the correct section — command-scoped runtime attributes do not belong under the azure.yaml
  project-context section) and in `docs/specs/metrics-audit/telemetry-schema.md` with its allowed
  enum set.

  _Source: [#9174](https://github.com/Azure/azure-dev/pull/9174), [#9452](https://github.com/Azure/azure-dev/pull/9452)_
