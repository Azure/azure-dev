# Design: Dynamic Parameter References in azd Metadata

## Problem Statement

Today, the `usageName` field in azd Bicep metadata only accepts **constant strings**:

```bicep
@metadata({azd: {
  type: 'location'
  usageName: ['OpenAI.Standard.gpt-4o, 10']
}})
param aiDeploymentsLocation string
```

The model name (`gpt-4o`) and capacity (`10`) are hardcoded. If a template author wants the user to **choose** the model or capacity at deploy-time via input parameters (e.g. `param modelName string`, `param modelCapacity int`), there is no way to wire those values into the quota validation metadata. The quota check runs against the constant, regardless of what the user actually picks.

Additionally, the user is expected to provide the **full SKU usage name** (e.g. `OpenAI.Standard.gpt-4o`), but users typically only know the model name (e.g. `gpt-4o`), not which SKU class (`Standard`, `GlobalStandard`, etc.) applies to it.

## Current Architecture

### How `ensureParameters` works today

1. **Load parameters file** — resolves `main.parameters.json`; parameters mapped to env vars get their values.
2. **Iterate parameters alphabetically** (`sortedKeys`) — for each parameter:
   - If it has a value from the parameters file → use it.
   - If it has a default value → skip.
   - If it has a saved config value → use it.
   - If it has `{type: "generate"}` metadata → auto-generate and save.
   - Otherwise → add to `parameterPrompts` list.
3. **Prompt for remaining parameters** — iterate `parameterPrompts` linearly and call `promptForParameter()` for each.

### How quota validation works today

Inside `promptForParameter()`, when a parameter has `type: "location"` and `usageName` metadata:

1. Parse each `usageName` entry as `"UsageName"` or `"UsageName, Capacity"` (constant strings).
2. Call `locationsWithQuotaFor()` → builds `[]ai.QuotaRequirement` from the constants.
3. Call `aiModelService.ListLocationsWithQuota()` → queries Azure Cognitive Services usage API per location.
4. Filter the location picker to only show locations with sufficient quota.

### Key limitations

1. The `usageName` values are parsed as literal strings at the time the location parameter is prompted. There is no mechanism to reference another parameter's value.
2. The flow is single-pass and parameters are processed in alphabetical order with no dependency awareness.
3. Users must know the full SKU usage name (e.g. `OpenAI.Standard.gpt-4o`), not just the model name.

## Proposed Solution: Parameter References with `$(p:paramName)` + SKU Auto-Resolution

### Reference Syntax

Introduce a reference expression syntax in `usageName` metadata values:

```
$(p:paramName)     — resolves to the value of another input parameter
```

The `p:` prefix scopes the reference to **p**arameters. This is intentionally extensible:
- `$(p:paramName)` — parameter value (this proposal)
- `$(env:VAR_NAME)` — environment variable (future)
- `$(config:key)` — azd config value (future)

### SKU Auto-Resolution

When `usageName` contains parameter references, users only need to provide the **model name** (e.g. `gpt-4o`), not the full SKU usage name. azd will:

1. Resolve the referenced parameter values (e.g. `modelName` → `"gpt-4o"`, `modelCapacity` → `10`)
2. Query the AI model catalog to find all available SKUs for that model name
3. If multiple SKUs exist (e.g. `Standard`, `GlobalStandard`), prompt the user to select one
4. If only one SKU exists, use it automatically
5. Build the full usage name from the selected SKU (e.g. `OpenAI.Standard.gpt-4o`) and apply the capacity for quota validation

### Bicep authoring example

```bicep
param modelName string          // user picks "gpt-4o", "gpt-4o-mini", etc.
param modelCapacity int         // user picks 10, 20, etc.

@metadata({azd: {
  type: 'location'
  usageName: ['$(p:modelName), $(p:modelCapacity)']
}})
param aiDeploymentsLocation string
```

When azd processes `aiDeploymentsLocation`:
1. It detects that `usageName` references `modelName` and `modelCapacity`
2. It ensures those parameters are prompted/resolved **first** (dependency-aware ordering)
3. It substitutes the values: `"gpt-4o, 10"`
4. It queries the AI model catalog for `gpt-4o` SKUs → finds `OpenAI.Standard.gpt-4o`, `OpenAI.GlobalStandard.gpt-4o`
5. If multiple SKUs: prompts user "Select a SKU for gpt-4o:" → user picks `Standard`
6. Uses the selected SKU's `UsageName` (`OpenAI.Standard.gpt-4o`) with capacity `10` for quota validation
7. Filters the location picker to only show locations with sufficient quota

## Implementation Plan

### Phase 1: Reference parsing utilities

**File:** `pkg/azure/arm_template.go`

1. Add a function `ExtractParamReferences(s string) []string` that scans a string for `$(p:...)` patterns and returns the referenced parameter names.
2. Add a function `ResolveParamReferences(s string, values map[string]any) (string, error)` that substitutes `$(p:paramName)` with the corresponding value from the map. Returns an error if a referenced parameter has no value.
3. Add a method `AzdMetadata.ParamDependencies() []string` that collects all parameter references across all `usageName` entries.
4. Add a function `HasParamReferences(s string) bool` that returns true if the string contains any `$(p:...)` patterns.

```go
// Example:
// ExtractParamReferences("$(p:modelName), $(p:modelCapacity)")
// → ["modelName", "modelCapacity"]
//
// ResolveParamReferences("$(p:modelName), $(p:modelCapacity)",
//   map[string]any{"modelName": "gpt-4o", "modelCapacity": 10})
// → "gpt-4o, 10"
//
// HasParamReferences("$(p:modelName), 10") → true
// HasParamReferences("OpenAI.Standard.gpt-4o, 10") → false
```

### Phase 2: SKU resolution for model-name-only references

**File:** `pkg/infra/provisioning/bicep/prompt.go`

When `usageName` entries contain parameter references (detected via `HasParamReferences`), the resolved value is treated as `"modelName, capacity"` instead of the full `"UsageName, capacity"`. azd must then resolve the model name to its full SKU usage name:

1. After resolving `$(p:...)` references, parse the result as `"modelName, capacity"`.
2. Call `aiModelService.ListModels()` to find SKUs for the given model name.
3. Collect all unique SKUs across all versions for that model.
4. **Filter to standard-tier SKUs only** (`GlobalStandard`, `DataZoneStandard`, `Standard`). Provisioned (PTU-based), Batch (async 24-hr), and Developer SKUs have fundamentally different billing/usage models and are excluded. If no standard-tier SKUs exist, fall back to all SKUs.
5. If **`GlobalStandard`** is among the candidates: **auto-select it**. It has the highest default quota and broadest availability — Microsoft recommends it as the starting point for most workloads.
6. If `GlobalStandard` is not available and **one candidate** remains: use it automatically.
7. If `GlobalStandard` is not available and **multiple candidates** remain: prompt the user to select one (e.g. "Select a deployment SKU for gpt-4o:").
8. If **no SKUs** exist: return an error ("model 'xyz' not found in the AI model catalog").
9. Use the selected SKU's `UsageName` (e.g. `OpenAI.GlobalStandard.gpt-4o`) combined with the capacity for quota validation.

#### SKU type reference

| Category | SKU Names | Billing | Included? |
|---|---|---|---|
| Standard (pay-per-token) | `GlobalStandard`, `DataZoneStandard`, `Standard` | Pay-per-token | **Yes** |
| Provisioned (reserved) | `GlobalProvisionedManaged`, `DataZoneProvisionedManaged`, `ProvisionedManaged` | Reserved PTU | No (filtered out) |
| Batch (async) | `GlobalBatch`, `DataZoneBatch` | 50% discount, 24-hr | No (filtered out) |
| Developer | `DeveloperTier` | Evaluation only | No (filtered out) |

See [Microsoft docs on deployment types](https://learn.microsoft.com/en-us/azure/ai-foundry/foundry-models/concepts/deployment-types) for details.

```go
// resolveModelSkuUsageName resolves a model name to a full SKU usage name.
// Filters to standard-tier SKUs, auto-selects GlobalStandard when available.
func (p *BicepProvider) resolveModelSkuUsageName(
    ctx context.Context,
    subId string,
    modelName string,
) (string, error) {
    models, err := p.aiModelService.ListModels(ctx, subId, nil)
    // find model by name, collect unique SKUs
    // filter to standard-tier only (GlobalStandard, DataZoneStandard, Standard)
    // if GlobalStandard found → return it
    // if len(candidates) == 1 → return candidates[0].UsageName
    // if len(candidates) > 1 → prompt user to select
    // if len(candidates) == 0 → error
}
```

### Phase 3: Dependency-aware parameter ordering

**File:** `pkg/infra/provisioning/bicep/bicep_provider.go` (in `ensureParameters`)

Currently, parameters that need prompting are collected in a flat list and prompted in alphabetical order. We need to introduce **topological sorting** so that parameters referenced by other parameters' metadata are prompted first.

1. After collecting `parameterPrompts`, build a dependency graph:
   - For each parameter in `parameterPrompts`, inspect its `AzdMetadata` for `ParamDependencies()`.
   - Each dependency creates an edge: `dependencyParam → thisParam` (dependency must come first).
2. Topologically sort the `parameterPrompts` list.
   - If a cycle is detected, return a clear error: `"circular parameter reference: X → Y → X"`.
3. Prompt parameters in the topologically sorted order instead of alphabetically.

**Important considerations:**
- A referenced parameter may **already have a value** (from parameters file, env var, config, or default). In that case it's not in `parameterPrompts` — it was resolved earlier. The dependency is already satisfied; no reordering needed.
- A referenced parameter may be in `parameterPrompts` (needs prompting). In that case, it must be prompted before the parameter that references it.
- A referenced parameter may not exist in the template at all → return a clear error.

### Phase 4: Reference resolution + SKU resolution at prompt time

**File:** `pkg/infra/provisioning/bicep/prompt.go` (in `promptForParameter`)

1. Modify `promptForParameter` to accept a `resolvedValues map[string]any` parameter containing all previously resolved parameter values.
2. Before calling `locationsWithQuotaFor`, check if `usageName` entries have parameter references:
   - **No references (constant strings):** Existing behavior — pass them directly to `locationsWithQuotaFor`.
   - **Has references:** Resolve `$(p:...)` substitutions, then for each resolved entry:
     a. Parse as `"modelName, capacity"`.
     b. Call `resolveModelSkuUsageName()` to get the full SKU usage name.
     c. Build the final `"UsageName, capacity"` string.
     d. Pass to `locationsWithQuotaFor`.

```go
if len(azdMetadata.UsageName) > 0 {
    resolvedUsageNames := make([]string, 0, len(azdMetadata.UsageName))
    for _, un := range azdMetadata.UsageName {
        if azure.HasParamReferences(un) {
            // Resolve parameter references
            resolved, err := azure.ResolveParamReferences(un, resolvedValues)
            // Parse as "modelName, capacity"
            // Resolve model SKU usage name via AI catalog
            // Build "SKU.UsageName, capacity"
        } else {
            // Existing behavior: use as-is
            resolvedUsageNames = append(resolvedUsageNames, un)
        }
    }
    withQuotaLocations, err := p.locationsWithQuotaFor(
        ctx, p.env.GetSubscriptionId(), allowedLocations, resolvedUsageNames)
}
```

### Phase 5: Update `ensureParameters` prompting loop

**File:** `pkg/infra/provisioning/bicep/bicep_provider.go`

The prompting loop must now:
1. Use the topologically sorted order from Phase 3.
2. Maintain a `resolvedValues map[string]any` that accumulates all known values (pre-resolved + prompted).
3. Pass `resolvedValues` to `promptForParameter` on each iteration.

```go
// Build resolvedValues from all sources already determined:
resolvedValues := make(map[string]any)
for k, v := range configuredParameters {
    resolvedValues[k] = v.Value
}
// Also include parameters with defaults
for k, param := range template.Parameters {
    if param.DefaultValue != nil {
        if _, alreadySet := resolvedValues[k]; !alreadySet {
            resolvedValues[k] = param.DefaultValue
        }
    }
}

// Prompt in dependency order
for _, prompt := range sortedParameterPrompts {
    value, err := p.promptForParameter(ctx, prompt.key, prompt.param, locationParameters, resolvedValues)
    resolvedValues[prompt.key] = value
    configuredParameters[prompt.key] = azure.ArmParameter{Value: value}
}
```

### Phase 6: Validation and error messages

1. **Unknown reference:** If `$(p:foo)` references a parameter `foo` that doesn't exist in the template → error: `"parameter 'aiDeploymentsLocation' metadata references unknown parameter 'foo'"`.
2. **Circular reference:** If A references B and B references A → error: `"circular parameter dependency detected: aiDeploymentsLocation → modelName → aiDeploymentsLocation"`.
3. **Model not found:** If the resolved model name doesn't exist in the catalog → error: `"model 'xyz' not found in the AI model catalog"`.
4. **Unresolvable at prompt time:** Safety net error: `"cannot resolve reference '$(p:modelName)': parameter 'modelName' has no value"`.

### Phase 7: Tests

**File:** `pkg/azure/arm_template_test.go`
- `TestExtractParamReferences` — various patterns, no refs, single ref, multiple refs, edge cases.
- `TestResolveParamReferences` — substitution, missing values, mixed literal and refs.
- `TestHasParamReferences` — detection of reference patterns.

**File:** `pkg/infra/provisioning/bicep/prompt_test.go`
- `TestPromptForParameterWithModelReference` — location parameter with `usageName: ['$(p:modelName), $(p:cap)']`, verify SKU resolution and quota check.
- `TestResolveModelSkuUsageName` — single SKU auto-select, multiple SKU prompt, model not found.

**File:** `pkg/infra/provisioning/bicep/bicep_provider_test.go`
- `TestEnsureParametersWithDependencyOrdering` — verify that `modelName` is prompted before `aiDeploymentsLocation`.
- `TestEnsureParametersCircularDependencyError` — verify clear error on cycles.
- `TestEnsureParametersReferenceToPreResolvedParam` — verify pre-resolved params satisfy dependencies.

### Phase 8: Documentation and scaffold template update

1. Update `resources/scaffold/templates/main.bicept` to optionally use `$(p:...)` when the template includes user-selectable model parameters.
2. Add documentation explaining the reference syntax and behavior.

## End-to-End Flow Example

Given this Bicep:
```bicep
param modelName string          // e.g. "gpt-4o"
param modelCapacity int         // e.g. 10

@metadata({azd: {
  type: 'location'
  usageName: ['$(p:modelName), $(p:modelCapacity)']
}})
param aiDeploymentsLocation string
```

1. azd parses the template and identifies that `aiDeploymentsLocation` has metadata dependencies on `modelName` and `modelCapacity`.
2. azd topologically sorts: `modelName` → `modelCapacity` → `aiDeploymentsLocation`.
3. azd prompts: "Enter a value for 'modelName':" → user types `gpt-4o`.
4. azd prompts: "Enter a value for 'modelCapacity':" → user types `10`.
5. azd resolves `usageName`: `"$(p:modelName), $(p:modelCapacity)"` → `"gpt-4o, 10"`.
6. azd queries AI catalog for `gpt-4o` SKUs → finds `Standard` (`OpenAI.Standard.gpt-4o`) and `GlobalStandard` (`OpenAI.GlobalStandard.gpt-4o`).
7. azd prompts: "Select a deployment SKU for gpt-4o:" → user picks `Standard`.
8. azd builds quota requirement: `{UsageName: "OpenAI.Standard.gpt-4o", MinCapacity: 10}`.
9. azd queries quota per location → filters to locations with ≥10 capacity remaining.
10. azd prompts: "Select a location for 'aiDeploymentsLocation':" → shows only qualifying locations.

## Non-Goals (Future Work)

- **`$(env:VAR_NAME)`** — resolving environment variables in metadata.
- **`$(config:key)`** — resolving azd config values in metadata.
- **References in other metadata fields** — only `usageName` supports references initially.
- **Complex expressions** — no arithmetic, conditionals, or function calls. Just simple string interpolation.

## Summary of Files to Change

| File | Change |
|------|--------|
| `pkg/azure/arm_template.go` | Add `ExtractParamReferences`, `ResolveParamReferences`, `HasParamReferences`, `AzdMetadata.ParamDependencies()` |
| `pkg/azure/arm_template_test.go` | Tests for reference parsing/resolution |
| `pkg/infra/provisioning/bicep/bicep_provider.go` | Dependency graph + topoSort in `ensureParameters`; pass `resolvedValues` |
| `pkg/infra/provisioning/bicep/prompt.go` | Resolve `$(p:...)` in `usageName`; SKU auto-resolution via AI catalog; add `resolvedValues` param |
| `pkg/infra/provisioning/bicep/prompt_test.go` | Tests for reference resolution and SKU resolution |
| `pkg/infra/provisioning/bicep/bicep_provider_test.go` | Tests for dependency ordering and end-to-end |
| `resources/scaffold/templates/main.bicept` | Optional: use `$(p:...)` for dynamic model params |
| Docs | Document the `$(p:paramName)` syntax |
