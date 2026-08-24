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

## Self-defending tests

A test that passes even when the behavior it is supposed to protect is removed is a **false negative**. When reviewing or writing tests:

- Assert the specific observable behavior the test is named for — not just a returned flag or a higher-level side effect that would survive if the core logic were deleted.
- For mock-based tests, assert that the expected call/response was actually consumed; do not rely on last-match precedence or ordering assumptions in the mock framework without pinning that behavior explicitly.
- When a test exercises a multi-step flow (e.g. install → verify → return), assert each critical intermediate step, not only the final result.

_Source: [#8805](https://github.com/Azure/azure-dev/pull/8805), [#9091](https://github.com/Azure/azure-dev/pull/9091)_

## Keep code comments in sync with code

When you change a function, variable, or data structure, update every comment that describes it — including doc comments on other functions/types that reference it, inline comments in callers, and rows in documentation tables that describe its behavior or values.

Specific patterns to watch:
- A doc comment separated from its function by an unrelated declaration now documents the wrong thing.
- An inline comment that names a specific value (e.g. `"mixed"`) becomes stale when the value is replaced.
- A documentation table row that describes when/whether a field is emitted must be updated when the emit condition changes.

_Source: [#9091](https://github.com/Azure/azure-dev/pull/9091)_
