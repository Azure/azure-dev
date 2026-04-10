# Local Preflight Validation

Local preflight validation is a client-side check that runs automatically before every `azd provision` deployment. It analyzes the compiled ARM template and a Bicep deployment snapshot to detect common issues — such as missing permissions.

## When It Runs

The preflight pipeline executes inside `BicepProvider.Deploy()`, after the Bicep module has been compiled and parameters resolved, but **before** the template is sent to Azure for server-side validation or deployment.

```
azd provision
  │
  ├── Compile Bicep module  →  ARM template + parameters
  ├── ► Local preflight validation  ← runs here
  │     ├── Parse ARM template (schema, contentVersion, resources)
  │     ├── Generate Bicep snapshot (resolved resource graph)
  │     ├── Analyze resources (derive properties)
  │     └── Run registered check functions
  ├── Server-side preflight (Azure ValidatePreflight API)
  └── Deploy
```

The user can disable preflight entirely by setting `provision.preflight` to `"off"` in their azd user configuration:

```bash
azd config set provision.preflight off
```

## Bicep Snapshots

Local preflight depends on the `bicep snapshot` command (available in modern Bicep CLI versions). The snapshot produces a **fully resolved deployment graph**: all template expressions are evaluated, conditions are applied, copy loops are expanded, and nested deployments are flattened into a single flat list of predicted resources.

### Why Snapshots Instead of Manual Parsing

An ARM template as compiled by `bicep build` still contains unresolved expressions like `"[parameters('location')]"`, conditional resources, and nested deployment modules. Manually parsing these would require reimplementing the ARM expression evaluator. The Bicep snapshot command does this natively and returns the final, concrete set of resources that would be deployed.

Advantages of snapshots over manual template parsing:

| Aspect | Manual Parsing | Bicep Snapshot |
|---|---|---|
| Expression resolution | Not possible (e.g. `[concat(...)]`) | Fully resolved |
| Nested deployments | Must recursively extract | Flattened automatically |
| Conditional resources | Cannot evaluate `condition` expressions | Excluded when false |
| Copy loops | Cannot expand `copy` blocks | Expanded to individual resources |
| Resource IDs | Symbolic names only | Resolved resource IDs |

### How Snapshots Are Generated

1. **Determine the parameters file.** If the module path is a `.bicepparam` file, it is used directly. Otherwise, a temporary `.bicepparam` file is generated from the resolved ARM parameters using `generateBicepParam()`. This file is placed next to the source `.bicep` module so that relative `using` paths resolve correctly.

2. **Build snapshot options.** The deployment target (`infra.Deployment`) provides scope information:
   - **Subscription-scoped deployments** → `--subscription-id` and `--location` flags.
   - **Resource-group-scoped deployments** → `--subscription-id` and `--resource-group` flags.

3. **Invoke `bicep snapshot`.** The Bicep CLI generates a `<basename>.snapshot.json` file. azd reads it into memory and deletes the temporary file.

4. **Parse the snapshot.** The JSON output contains a `predictedResources` array — each entry is a fully resolved resource with type, apiVersion, name, location, properties, and so on.

```
┌──────────────────────────┐
│  main.bicep              │
│  + parameters            │
└──────────┬───────────────┘
           │
    generateBicepParam()
           │
           ▼
┌──────────────────────────┐
│  preflight-*.bicepparam  │  (temporary)
└──────────┬───────────────┘
           │
    bicep snapshot --subscription-id ... --resource-group ...
           │
           ▼
┌──────────────────────────┐
│  preflight-*.snapshot.json│
│  {                       │
│    "predictedResources": │
│    [                     │
│      { type, name, ... } │
│      { type, name, ... } │
│    ]                     │
│  }                       │
└──────────────────────────┘
```

## Check Pipeline

The preflight system uses a pluggable pipeline of check functions. Each check receives a `validationContext` containing:

- **`Console`** — for user interaction (prompts, messages).
- **`Props`** — derived properties from the resource analysis (e.g. `HasRoleAssignments`).
- **`ResourcesSnapshot`** — the raw JSON from `bicep snapshot`.
- **`SnapshotResources`** — the parsed `[]armTemplateResource` list from the snapshot.

Checks are registered via `AddCheck()` before calling `validate()`. They run in registration order and each returns either:

- `nil` — nothing to report (check passed).
- `*PreflightCheckResult` with a `Severity` (`PreflightCheckWarning` or `PreflightCheckError`) and a `Message`.

### Adding a New Check

To add a new preflight check:

```go
localPreflight.AddCheck(func(ctx context.Context, valCtx *validationContext) (*PreflightCheckResult, error) {
    // Inspect valCtx.SnapshotResources, valCtx.Props, etc.
    for _, res := range valCtx.SnapshotResources {
        if strings.EqualFold(res.Type, "Microsoft.SomeProvider/problematicResource") {
            return &PreflightCheckResult{
                Severity: PreflightCheckWarning,
                Message:  "This resource type requires additional configuration.",
            }, nil
        }
    }
    return nil, nil // nothing to report
})
```

### Built-in Checks

| Check | What It Does | Severity |
|---|---|---|
| Role assignment permissions | Detects `Microsoft.Authorization/roleAssignments` in the snapshot and verifies the current principal has `roleAssignments/write` permission on the subscription. | Warning |

### Investigated Checks (Not Implemented)

The following checks were investigated but not shipped due to technical
limitations. Each link leads to a detailed writeup of the investigation,
including the approaches tried and why they were not viable.

| Check | Goal | Reason Not Implemented |
|---|---|---|
| [Storage account policy check](storage-account-policy-check.md) | Warn when Azure Policy denies storage accounts with local authentication enabled (`allowSharedKeyAccess: true`). | Client-side policy parsing produces false positives (cannot evaluate ARM expressions in policy conditions); server-side `checkPolicyRestrictions` API does not evaluate management-group-inherited policies. |

## UX Presentation

Results are displayed using the `PreflightReport` UX component (`pkg/output/ux/preflight_report.go`), which implements the standard `UxItem` interface. The report groups and orders findings: all warnings appear first, followed by all errors. Each entry is prefixed with the standard azd status icons.

## Scenarios

### Scenario 1: No Issues Found

All registered checks pass. No output is printed from the preflight step. The deployment proceeds directly to server-side validation and then Azure deployment.

```
Validating deployment (✓) Done:

Creating/Updating resources ...
```

### Scenario 2: Warnings Only

One or more checks return warnings but no errors. The warnings are displayed and the user is prompted to continue. The default selection is **Yes** — pressing Enter continues the deployment.

```
Validating deployment

(!) Warning: the current principal (abc-123) does not have permission
to create role assignments (Microsoft.Authorization/roleAssignments/write)
on subscription sub-456. The deployment includes role assignments and
will fail without this permission.

? Preflight validation found warnings that may cause the deployment
  to fail. Do you want to continue? (Y/n)
```

If the user confirms (or accepts the default), deployment proceeds normally. If the user declines, the operation is aborted with a zero exit code (an intentional abort, not a failure).

### Scenario 3: Errors Only

One or more checks return errors. The errors are displayed and the deployment is **immediately aborted** — the user is not prompted. The CLI exits with a zero exit code.

```
Validating deployment

(x) Failed: critical configuration error detected in template

preflight validation detected errors, deployment aborted
```

Note: the exit code is **zero** because the preflight validation **successfully** detected problems and intentionally aborted the deployment. This is not an unexpected internal failure — the CLI completed its task (validating and reporting errors) without encountering any execution errors itself.

### Scenario 4: Warnings and Errors

When the report contains both warnings and errors, warnings are listed first and errors second. Because errors are present the deployment is aborted immediately — the warning prompt is skipped.

```
Validating deployment

(!) Warning: the current principal does not have permission to create
role assignments on this subscription.

(x) Failed: required parameter 'storageAccountName' is missing from
the deployment.

preflight validation detected errors, deployment aborted
```

### Scenario 5: Check Function Returns an Error

If a check function itself fails (returns a Go `error` rather than a `*PreflightCheckResult`), this is treated as an infrastructure failure. The CLI reports it as a hard error and exits with a non-zero code. This is distinct from a check returning a result with `PreflightCheckError` severity — that case means "we successfully detected a problem in the template", while an error return means "something went wrong while trying to run the check".

```
ERROR: local preflight validation failed: preflight check failed: <underlying error>
```

## Exit Code Behavior

The exit code distinguishes between **successful operation** (the CLI did what it was supposed to do) and **internal failure** (the CLI could not complete its task).

Preflight validation detecting errors and aborting the deployment is a **successful outcome** — the CLI performed the validation and correctly prevented a bad deployment. Only failures in the validation machinery itself produce a non-zero exit code.

| Outcome | Exit Code | Rationale |
|---|---|---|
| No issues | 0 | Deployment proceeds and succeeds. |
| Warnings only, user continues | 0 | User acknowledged warnings; deployment proceeds. |
| Warnings only, user declines | 0 | User chose to abort; intentional, not a failure. |
| Errors detected | 0 | Validation successfully detected problems and aborted the deployment. |
| Check function error | 1 | Internal failure running a check (the `validate` function returned a non-nil error). |

## File Layout

```
pkg/
├── infra/provisioning/bicep/
│   ├── local_preflight.go          # Core pipeline, ARM types, parseTemplate, analyzeResources
│   ├── local_preflight_test.go     # Unit tests for parsing, analysis, check pipeline
│   ├── role_assignment_check_test.go  # Tests for the role assignment check
│   ├── generate_bicep_param_test.go   # Tests for .bicepparam generation
│   └── bicep_provider.go          # validatePreflight() integration, checkRoleAssignmentPermissions
├── output/ux/
│   ├── preflight_report.go        # PreflightReport UxItem
│   └── preflight_report_test.go   # Tests for PreflightReport
└── tools/bicep/
    └── bicep.go                   # Snapshot() method, SnapshotOptions builder
```
