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

## Test coverage symmetry

When tests exist for one side of a symmetric code path, flag the missing counterpart:

- **Prompt paths**: If there is a test for subscription prompt (success, error, cancellation), add equivalent tests for the location prompt and any other structurally identical prompts. Missing location-side tests allow regressions to go undetected.
- **Serialisation round-trips**: For `MarshalYAML`/`UnmarshalYAML` or JSON encode/decode paths, add a load-back assertion (e.g. save the file then reload and verify the field) in addition to just testing the write direction.
- **Compound constraint interactions**: When multiple constraints can interact (e.g., capacity step-alignment × max-capacity), add a test that exercises both simultaneously — not just each in isolation. The cross-case is often the one that surfaces bugs.

_Source: [#8883](https://github.com/Azure/azure-dev/pull/8883), [#8874](https://github.com/Azure/azure-dev/pull/8874), [#8876](https://github.com/Azure/azure-dev/pull/8876)_
