# Hook Executor Config Integration — Development Plan

## Overview

Integrate the `Config map[string]any` property bag (PR #7690) into each hook executor, enabling users to configure executor-specific settings in `azure.yaml`. Properties mirror existing `ServiceConfig.Config` patterns where applicable.

**Parent Issue:** #7653 (Add generic config property bag to HookConfig)
**Parent Epic:** #7435 (Multi-Language Hook Support)
**Prerequisite:** PR #7690 (core Config plumbing — in draft)

## Scope Assessment

3 issues, all parallelizable. Flat plan (no epics needed).

## Sequencing

All three issues can be implemented in parallel — they each modify a single executor with no cross-dependencies. All depend on PR #7690 (core Config bag) being merged first.

```
PR #7690 (core Config bag) ──┬── Issue 1 (JS/TS packageManager)
                              ├── Issue 2 (Python virtualEnvName)
                              └── Issue 3 (.NET configuration + framework)
```

## Cross-Cutting Concerns

- **Shared validation pattern**: Each executor reads from `execCtx.Config` and validates its own keys. Consider extracting a shared `configString(config map[string]any, key string) (string, error)` helper if patterns converge.
- **JSON schema**: After all executors are integrated, update `azure.yaml.json` with `oneOf` discriminated on `kind` to validate per-executor config shapes. (Separate issue.)
- **Documentation**: After all executors are integrated, update `docs/language-hooks.md` with per-executor config options. (Separate issue.)
