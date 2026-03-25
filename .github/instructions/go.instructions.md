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
