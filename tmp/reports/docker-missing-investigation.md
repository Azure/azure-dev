# Docker Missing: Investigation & Implementation Plan

**Generated:** 2026-03-22  
**Issue:** #5715, #3533  
**Telemetry:** 2,936 failures, 784 users / 28 days

---

## Problem

When Docker is not installed, `azd deploy`/`up` fails with `tool.Docker.missing` but **doesn't tell users they can use `remoteBuild: true`** — a built-in alternative that builds containers on Azure instead of locally.

This affects Container Apps, AKS, and some Function App deployments where Docker is listed as required but isn't actually needed when remote builds are enabled.

---

## Architecture

```
azd deploy
  → projectManager.EnsureServiceTargetTools()
    → containerHelper.RequiredExternalTools()
      → if remoteBuild: true → [] (no Docker needed!)
      → if remoteBuild: false/nil → [docker] (Docker required)
    → tools.EnsureInstalled(docker)
      → docker.CheckInstalled() → MissingToolErrors
```

### Key Insight

`ContainerHelper.RequiredExternalTools()` (`pkg/project/container_helper.go:250-260`) already skips Docker when `remoteBuild: true`. The user just doesn't know this option exists.

---

## What Supports remoteBuild

| Service Target | remoteBuild Support | Default | Docker Required Without It |
|---|---|---|---|
| **Container Apps** | ✅ Yes | Not set | Yes |
| **AKS** | ✅ Yes | Not set | Yes |
| **Function App** (Flex) | ✅ Yes | JS/TS/Python: true | Varies |
| **App Service** | ❌ No | N/A | Yes (if containerized) |
| **Static Web App** | ❌ No | N/A | No |

---

## Proposed Fix

### Where to change: `internal/cmd/deploy.go` (after `EnsureServiceTargetTools`)

When `MissingToolErrors` is caught with Docker in the tool list:

1. Check which services actually need Docker (considering remoteBuild support)
2. If all services support remoteBuild → suggest it as primary alternative
3. If some do, some don't → suggest it for eligible services + install Docker for the rest

### Error flow (proposed):

```go
if toolErr, ok := errors.AsType[*tools.MissingToolErrors](err); ok {
    if slices.Contains(toolErr.ToolNames, "Docker") {
        // Check if services can use remoteBuild instead
        return nil, &internal.ErrorWithSuggestion{
            Err: toolErr,
            Suggestion: "Your services can build on Azure instead of locally. " +
                "Add 'docker: { remoteBuild: true }' to each service in azure.yaml, " +
                "or install Docker: https://aka.ms/azure-dev/docker-install",
        }
    }
}
```

### Files to modify

| File | Change |
|---|---|
| `cli/azd/internal/cmd/deploy.go` | Catch Docker missing, wrap with remoteBuild suggestion |
| `cli/azd/pkg/project/project_manager.go` | Add helper to detect remoteBuild-capable services |
| `cli/azd/pkg/tools/docker/docker.go` | Optionally enhance base error message |
| `cli/azd/internal/cmd/deploy_test.go` | Test for suggestion when Docker missing |

### Same pattern needed in:
- `internal/cmd/provision.go` (calls `EnsureAllTools`)
- `internal/cmd/up.go` (compound command)

---

## Expected Impact

- **2,936 failures/28d** get actionable guidance instead of a dead-end error
- **784 users** learn about remoteBuild as an alternative
- Users without Docker can still deploy to Container Apps and AKS
- Aligns with existing `ErrorWithSuggestion` pattern used throughout the codebase

---

## Related Issues

- **#5715** — "When docker is missing, suggest to use remoteBuild: true" (open)
- **#3533** — "Operations should verify presence of dependency tools needed for service" (open, good first issue)
- **#7239** — UnknownError investigation (newly created P0)
