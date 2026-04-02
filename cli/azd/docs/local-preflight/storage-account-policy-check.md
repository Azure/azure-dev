# Storage Account Policy Check — Investigation Summary

> **Status**: Not implemented — false positive rate too high for the warning to be actionable.
> Investigation completed March 2026.

## Goal

Detect Azure Policy assignments that deny storage account deployments when local
authentication (shared key access) is enabled. Warn users **before** deployment so
they can set `allowSharedKeyAccess: false` in their Bicep templates instead of
waiting for a late `RequestDisallowedByPolicy` failure.

## Background

Enterprise subscriptions commonly enforce security policies through Azure Policy
at the management group level. A typical deny policy targeting storage accounts
looks like:

```json
{
  "if": {
    "allOf": [
      { "field": "type", "equals": "Microsoft.Storage/storageAccounts" },
      {
        "anyOf": [
          { "field": "Microsoft.Storage/storageAccounts/allowSharedKeyAccess", "exists": "false" },
          { "field": "Microsoft.Storage/storageAccounts/allowSharedKeyAccess", "equals": "true" }
        ]
      }
    ]
  },
  "then": { "effect": "deny" }
}
```

When a template deploys a storage account without `allowSharedKeyAccess: false`,
ARM returns:

```
RequestDisallowedByPolicy: Resource 'st-contoso-dev-01' was disallowed by policy.
```

This error appears only after the deployment has been submitted and partially
processed (often several minutes into the operation).

## Approaches Investigated

### Approach 1: Client-Side Policy Rule Parsing (ARM Policy SDK)

**How it works:**

1. List all policy assignments for the subscription using
   `AssignmentsClient.NewListPager()` from the ARM policy SDK. This returns assignments from
   all scopes — subscription-level, resource-group-level, and inherited from
   parent management groups.
2. Fetch each assignment's policy definition (with caching) and inspect the rule
   for `field` conditions targeting `allowSharedKeyAccess` or `disableLocalAuth`.
3. Resolve parameterized effects (e.g. `[parameters('effect')]`) using the
   assignment's parameter values.
4. If a deny-effect policy targets storage accounts with local auth fields, and
   the Bicep snapshot contains storage accounts without `allowSharedKeyAccess:
   false`, emit a warning.

**Result: Detects real denials but produces false positives.**

The list pager correctly returns management-group-inherited assignments. This is
critical because enterprise deny policies almost always originate from management
groups, not from the subscription itself. The SDK handles the inheritance
automatically.

However, enterprise policies often include **gating conditions** that the
client-side parser cannot evaluate:

- **Opt-in tags**: `[contains(subscription().tags, parameters('optInTagName'))]`
  — the policy only applies if the subscription has a specific tag.
- **Region gates**: `[split(subscription().tags[parameters('optInTagName')], ',')]`
  — the policy applies only in regions the subscription has opted into.
- **Skip tags**: `[concat('tags[', parameters('skipTagName'), ']')]` — resources
  with a specific tag are exempt.
- **Resource group filters**: `[resourceGroup().managedBy]` — managed resource
  groups are exempt.

These are ARM template expressions (`[...]`) that require runtime context
(subscription tags, resource group metadata) to evaluate. The client-side parser
sees the deny-effect rule and the `allowSharedKeyAccess` field, but cannot
determine whether the gating conditions would exclude the deployment.

**Example — false positive scenario:**

A management group assigns the policy "Storage — Disable Local Auth (Opt-In)"
with `effect: deny`. The policy rule includes:

```json
{
  "allOf": [
    { "field": "type", "equals": "Microsoft.Storage/storageAccounts" },
    { "field": "Microsoft.Storage/storageAccounts/allowSharedKeyAccess", "equals": "true" },
    { "value": "[contains(subscription().tags, parameters('optInTagName'))]", "equals": "true" },
    { "anyOf": [
        { "field": "location", "in": "[split(subscription().tags[parameters('optInTagName')], ',')]" },
        { "value": "all_regions", "in": "[split(subscription().tags[parameters('optInTagName')], ',')]" }
    ]}
  ]
}
```

The assignment sets `optInTagName` = `"Az.Sec.DisableLocalAuth.Storage::OptIn"`.

A subscription `contoso-dev-sub` does **not** have the
`Az.Sec.DisableLocalAuth.Storage::OptIn` tag. The `contains()` condition evaluates
to `false`, which does not match `"equals": "true"`, so the `allOf` short-circuits
and the policy never fires — deployments with `allowSharedKeyAccess: true` succeed.

The client-side parser cannot evaluate `subscription().tags[...]` or `split()`
expressions. It sees the deny + `allowSharedKeyAccess` pattern and warns the
user, even though the policy would not actually block the deployment.

On a different subscription `contoso-prod-sub` with
`Az.Sec.DisableLocalAuth.Storage::OptIn = all_regions`, the same policy **does**
fire, and the warning would be correct.

Both subscriptions produce identical deny policy detection results from the
client-side parser — it cannot distinguish between them.

**Considered mitigations:**

- *Evaluate `contains(subscription().tags, ...)` by fetching subscription tags*:
  Handles the opt-in pattern but not the full range of ARM expressions (region
  gating with `split()`, `resourceGroup().managedBy`, `count`/`where` blocks).
  Each new expression pattern would require custom parsing logic, creating an
  ever-growing mini ARM expression evaluator.
- *Skip policies with conditions that cannot be evaluated locally*: Would suppress the false positive
  but also suppress warnings for real denials. The `"Storage Accounts - Safe
  Secrets Standard"` policy (which **does** block deployments) uses the same
  ARM expression patterns for skip-tag exemptions.

### Approach 2: Server-Side Policy Evaluation (`checkPolicyRestrictions` API)

**How it works:**

The `Microsoft.PolicyInsights` resource provider offers a
`checkPolicyRestrictions` API that evaluates hypothetical resources against all
assigned policies server-side. You submit a resource's content (type, location,
properties) and Azure returns which policies would deny it and why.

```
POST /subscriptions/{id}/providers/Microsoft.PolicyInsights/checkPolicyRestrictions
     ?api-version=2022-03-01

{
  "resourceDetails": {
    "resourceContent": {
      "type": "Microsoft.Storage/storageAccounts",
      "location": "eastus2",
      "properties": { "allowSharedKeyAccess": true }
    },
    "apiVersion": "2023-05-01"
  }
}
```

The API:
- Evaluates ALL conditions (ARM expressions, tag lookups, region gates)
- Handles exemptions and parameter overrides
- Returns policy evaluation results and field-level restrictions
- Requires only `Microsoft.PolicyInsights/*/read` (included in Reader role)

**Result: Accurate evaluation but does not see management-group-inherited policies.**

Testing against a subscription with a management-group-assigned deny policy
("Storage Accounts - Safe Secrets Standard", assigned at the tenant root
management group) confirmed that the API returns **empty results**
(`policyEvaluations: []`, `fieldRestrictions: []`) even when:

- The deny policy is confirmed active (`policyStates` reports it with
  `effect: deny` for existing storage accounts)
- The resource content explicitly sets the denied property
- Both subscription-scope and resource-group-scope endpoints are tried
- A `PendingFields` parameter is included in the request

The API correctly detects subscription-level policies — for example, a
subscription-scoped `modify` policy for `allowBlobPublicAccess` returned the
expected `fieldRestrictions` with `result: Required`. Only management-group-
inherited policies are invisible.

#### `api-version=2024-10-01` follow-up (April 2026)

The `2024-10-01` API version added a
[management group scope endpoint](https://learn.microsoft.com/en-us/rest/api/policyinsights/policy-restrictions/check-at-management-group-scope?view=rest-policyinsights-2024-10-01)
and an `includeAuditEffect` parameter on the subscription scope. We tested both
to see if they resolve the MG-inherited policy blind spot.

**Subscription-scope with `2024-10-01`:** Same empty results as `2022-03-01`.
The newer API version does not change the subscription-scope behavior — MG-
inherited policies remain invisible. Adding `includeAuditEffect: true` also
returned empty.

**Management group scope endpoint:** This endpoint only supports `pendingFields`
with a single `type` field — it rejects `resourceDetails` entirely:

```
POST /providers/Microsoft.Management/managementGroups/{mgId}
     /providers/Microsoft.PolicyInsights/checkPolicyRestrictions
     ?api-version=2024-10-01

# Only this request body is accepted:
{ "pendingFields": [{ "field": "type" }] }

# This is rejected with InvalidCheckRestrictionsRequest:
{ "resourceDetails": { ... } }
```

The error message confirms the limitation:
> *"The 'resourceDetails' property is not supported in requests at Management
> Group level. The request content can only have a single 'type' pending field."*

Testing at multiple levels of the MG hierarchy (direct parent, intermediate MGs,
tenant root) all returned empty `fieldRestrictions` even for the `type` field.
The MG-scope endpoint is designed to answer "which resource types are
restricted" at a management group level, not "would this specific resource
configuration be denied" — making it unsuitable for property-level checks like
`allowSharedKeyAccess`.

| Endpoint | API Version | Sees MG deny policies | Supports resource properties |
|---|---|---|---|
| Subscription scope | `2022-03-01` | ❌ | ✅ |
| Subscription scope | `2024-10-01` | ❌ | ✅ |
| Resource group scope | `2024-10-01` | ❌ | ✅ |
| MG scope | `2024-10-01` | Untested (empty for `type`) | ❌ (rejected) |

### Approach 3: Policy States API (`policyStates`)

The `policyStates` API (`Microsoft.PolicyInsights/policyStates/latest/queryResults`)
evaluates compliance of **existing** resources. It sees management-group-inherited
policies and correctly reports non-compliance.

However, it cannot evaluate **hypothetical** resources from a Bicep snapshot. It
only works with resources that have already been deployed. Since the goal is to
warn *before* deployment, this API is not applicable.

## Conclusion

| Approach | Sees MG policies | Evaluates all conditions | Limitation | Suitable |
|---|---|---|---|---|
| Client-side ARM policy SDK parsing | ✅ Yes | ❌ No (ARM expressions) | Cannot evaluate runtime expressions → false positives | ❌ |
| Server-side `checkPolicyRestrictions` (sub scope) | ❌ No | ✅ Yes | Misses MG-inherited policies (confirmed with both `2022-03-01` and `2024-10-01`) | ❌ |
| Server-side `checkPolicyRestrictions` (MG scope, `2024-10-01`) | Untested | ❌ No | Only supports `type` field; rejects `resourceDetails` | ❌ |
| `policyStates` API | ✅ Yes | ✅ Yes | Only evaluates already-deployed resources | ❌ |

No currently available approach provides both accurate policy detection
(including management-group-inherited policies) and correct evaluation of
complex policy conditions for hypothetical resources. The check was not shipped
to avoid showing incorrect warnings that would erode user trust in the preflight
system.

## Future Considerations

- **`checkPolicyRestrictions` MG-scope with `resourceDetails` support**: If
  Microsoft updates the MG-scope endpoint to accept `resourceDetails` (not just
  `type` pending fields), it would enable property-level evaluation against
  MG-inherited policies. This is the only server-side path that could solve the
  problem without client-side expression evaluation. As of `2024-10-01`, the
  MG-scope endpoint rejects `resourceDetails` requests.
- A hybrid approach (client-side detection + subscription tag evaluation for
  common patterns) could reduce false positives for the most common gating
  conditions, at the cost of maintaining a partial ARM expression evaluator.
- The ARM deployment validation API (`/validate`) and what-if API (`/whatIf`) do
  **not** evaluate Azure Policy deny effects. Only actual deployment submission
  triggers deny policy evaluation, which is equivalent to the server-side
  preflight that already runs after local preflight.
