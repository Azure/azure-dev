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
├── infra/provisioning/
│   └── validation_dispatcher.go   # ValidationCheckDispatcher interface (DI decoupling)
├── output/ux/
│   ├── preflight_report.go        # PreflightReport UxItem
│   └── preflight_report_test.go   # Tests for PreflightReport
└── tools/bicep/
    └── bicep.go                   # Snapshot() method, SnapshotOptions builder
```

## Extension-Provided Checks

Extensions can contribute validation checks to the local preflight pipeline using
the `validation-provider` capability. This allows extensions to inspect the Bicep
deployment data (ARM template, snapshot, parameters, location) and return additional
warnings or errors that are merged into the preflight report.

### How It Works

1. The extension declares `validation-provider` in its `extension.yaml` capabilities.
2. During startup, the extension registers one or more checks with a `check_type`
   (e.g., `"local-preflight"`) and a stable `rule_id`.
3. When `BicepProvider.validatePreflight()` runs, after the built-in checks complete,
   it dispatches to all extension-registered checks matching `check_type: "local-preflight"`.
4. Each extension check receives a context map with:
   - `resources_snapshot` — Bicep snapshot JSON (`predictedResources`)
   - `predicted_resources` — Parsed resource array from the snapshot
   - `arm_template` — Compiled ARM template JSON
   - `arm_parameters` — Resolved ARM parameters JSON
   - `env_location` — Azure location string
5. The extension returns `ValidationCheckResult` items (severity, message, suggestion, links)
   which are appended to the preflight report.

### Extension Code Example

```go
// In your extension's listen command:
host := azdext.NewExtensionHost(azdClient).
    WithValidationCheck(azdext.ValidationCheckRegistration{
        CheckType: "local-preflight",
        RuleID:    "my_naming_rule",
        Factory: func() azdext.ValidationCheckProvider {
            return &MyNamingCheck{}
        },
    })

// The check implementation:
type MyNamingCheck struct{}

func (c *MyNamingCheck) Validate(
    ctx context.Context,
    valCtx *azdext.ValidationContext,
    req *azdext.ValidationCheckRequest,
) (*azdext.ValidationCheckResponse, error) {
    resources, err := valCtx.ParsePredictedResources()
    if err != nil || len(resources) == 0 {
        return &azdext.ValidationCheckResponse{}, nil
    }

    // Inspect resources, return results...
    return &azdext.ValidationCheckResponse{
        Results: results,
    }, nil
}
```

### Failure Handling

If an extension check returns an error (the `Validate` method fails), the error is
logged as a warning but does **not** block the deployment. Only the extension's
results are omitted. Built-in check failures still follow the standard error behavior
described above.

### Future Check Types

The `check_type` field is designed for extensibility. Two check types are
currently supported — `"local-preflight"` (Bicep-only, ARM-rich context) and
`"provision"` (provider-agnostic, lean context; see below). Future check types
(e.g., `"project-config"`, `"auth"`) can be added without changing the protocol.
Each check type defines its own context keys.

## Provider-Agnostic Provision Checks (`"provision"`)

The `"local-preflight"` dispatch above only runs for **Bicep**-provisioned
deployments, because its context is built from the compiled ARM template and
Bicep snapshot. Extensions that ship their own provisioning provider (e.g.
`microsoft.foundry`, the `demo` provider) — as well as the core Terraform
provider — never route through `BicepProvider`, so a `"local-preflight"` check
registered by such an extension would be dead code.

To let a check run **before provisioning regardless of the provider**, register
it under the `"provision"` check type instead. This dispatch happens in the
provider-agnostic `provisioning.Manager.Deploy()` (and `Manager.Preview()`),
immediately before the selected provider runs:

```
azd provision
  │
  ├── ► Provider-agnostic "provision" validation  ← runs here, all providers
  ├── provider.Deploy()
  │     └── (Bicep only) "local-preflight" validation
  └── ...
```

### Lean, Provider-Agnostic Context

Because Terraform and extension providers do not produce an ARM template, the
`"provision"` context is intentionally lean and carries **no** template,
parameters, or resource snapshot. Each check receives:

- `env_name` — the azd environment name
- `subscription_id` — the target Azure subscription id
- `env_location` — the Azure location string
- `resource_group` — the target resource group name (empty for subscription-scoped)
- `target_scope` — `"subscription"` or `"resourceGroup"`

Typed accessors (`EnvName()`, `SubscriptionID()`, `EnvLocation()`,
`ResourceGroup()`, `TargetScope()`) are available on `azdext.ValidationContext`.

### Shared Behavior

The `"provision"` dispatch reuses the same machinery as `"local-preflight"`:
parallel dispatch, the uniform preflight report, severity/abort semantics
(WARNING prompts to continue; ERROR aborts with exit code 0), an equivalent
60s dispatch timeout (`provisionValidationTimeout` in `provision_validation.go`,
mirroring the Bicep path's `extensionValidationTimeout`), and the
`provision.preflight` config gate (`off` disables both dispatch sites).
Registration still requires the `validation-provider` capability.

### Extension Code Example

```go
host := azdext.NewExtensionHost(azdClient).
    WithValidationCheck(azdext.ValidationCheckRegistration{
        CheckType: azdext.ValidationCheckTypeProvision, // "provision"
        RuleID:    "resource_group_location",
        Factory: func() azdext.ValidationCheckProvider {
            return &MyLocationCheck{}
        },
    })
```

### Registering Both Check Types

An extension can register **both** check types at once — they are independent and
both fire when applicable. The `microsoft.azd.demo` extension does exactly this as
a reference:

- `demo_warning` under `"local-preflight"` — inspects the Bicep snapshot; runs only
  for Bicep and is skipped gracefully when no snapshot is available.
- `demo_provision_warning` under `"provision"` — reads the lean provision context;
  runs before provisioning for every provider, including the demo provider.

Because registrations are keyed by `check_type` + `rule_id`, the same extension can
safely register a check under each type.
