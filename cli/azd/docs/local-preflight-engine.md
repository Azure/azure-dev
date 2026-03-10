# Local Preflight Engine

The `local_preflight` package provides a lightweight, client-side validation engine that
runs fast, dependency-free checks *before* interacting with Azure services. It was
introduced to surface common deployment blockers early — before provisioning starts — so
that users receive clear, actionable guidance instead of cryptic ARM errors mid-deployment.

## How it works today

Every time `azd provision` (or `azd up`) runs, the Bicep provider executes the local
preflight engine as the very first step of deployment, before the ARM template validation
API call:

```
azd provision
  └─ BicepProvider.Deploy()
       ├─ (1) runLocalPreflight()   ← client-side checks (this engine)
       ├─ (2) validatePreflight()   ← ARM API template validation
       └─ (3) deployModule()        ← actual deployment
```

If any local check fails, the deployment is aborted immediately with a clear error message
and a remediation suggestion. Users never wait on a slow ARM round-trip for a problem that
could have been detected locally.

### Built-in checks

| Check | What it validates |
|-------|-------------------|
| `AuthCheck` | The user is logged in and the stored credential can be exchanged for a valid access token. |
| `SubscriptionCheck` | `AZURE_SUBSCRIPTION_ID` and `AZURE_LOCATION` are both set in the active environment. |

## How to add a new check

Implement the `Check` interface:

```go
type Check interface {
    Name() string
    Run(ctx context.Context) Result
}
```

Then register it with the engine in `BicepProvider.runLocalPreflight()`:

```go
engine := local_preflight.NewEngine(
    local_preflight.NewAuthCheck(p.authManager),
    local_preflight.NewSubscriptionCheck(p.env),
    mypackage.NewMyCheck(someService),  // ← add here
)
```

A `Result` has three fields:

| Field | Type | Description |
|-------|------|-------------|
| `Status` | `Status` | `StatusPass`, `StatusWarn`, `StatusFail`, or `StatusSkipped` |
| `Message` | `string` | Short human-readable outcome |
| `Suggestion` | `string` | Optional remediation hint shown for `Warn` and `Fail` |

### Example: resource provider registration check

```go
type ResourceProviderCheck struct {
    subscriptionId  string
    requiredProvider string
    client          *armresources.ProvidersClient
}

func (c *ResourceProviderCheck) Name() string { return "Resource Provider: " + c.requiredProvider }

func (c *ResourceProviderCheck) Run(ctx context.Context) local_preflight.Result {
    provider, err := c.client.Get(ctx, c.requiredProvider, nil)
    if err != nil {
        return local_preflight.Result{
            Status:  local_preflight.StatusWarn,
            Message: fmt.Sprintf("Could not verify %s registration: %v", c.requiredProvider, err),
        }
    }
    if provider.RegistrationState == nil || *provider.RegistrationState != "Registered" {
        return local_preflight.Result{
            Status:  local_preflight.StatusFail,
            Message: fmt.Sprintf("Resource provider %q is not registered.", c.requiredProvider),
            Suggestion: fmt.Sprintf(
                "Run: az provider register --namespace %s --subscription %s",
                c.requiredProvider, c.subscriptionId),
        }
    }
    return local_preflight.Result{
        Status:  local_preflight.StatusPass,
        Message: fmt.Sprintf("Resource provider %q is registered.", c.requiredProvider),
    }
}
```

## Future: `azd validate` command

[Issue #6866](https://github.com/Azure/azure-dev/issues/6866) tracks a dedicated
`azd validate` command. The local preflight engine is designed so that such a command
can be implemented with minimal effort:

```go
// cmd/validate.go (future)
type validateAction struct {
    authManager         *auth.Manager
    env                 *environment.Environment
    subscriptionManager *account.SubscriptionsManager
    console             input.Console
}

func (a *validateAction) Run(ctx context.Context) (*actions.ActionResult, error) {
    engine := local_preflight.NewEngine(
        local_preflight.NewAuthCheck(a.authManager),
        local_preflight.NewSubscriptionCheck(a.env),
        // Add more checks as the feature matures:
        // preflight.NewRoleAssignmentCheck(a.authManager, a.subscriptionManager, a.env),
        // preflight.NewResourceProviderCheck(a.subscriptionManager, a.env, requiredProviders),
    )

    results, err := engine.Run(ctx)
    local_preflight.PrintResults(ctx, a.console, results)

    if err != nil {
        return nil, fmt.Errorf("validation failed: one or more checks did not pass")
    }

    return &actions.ActionResult{
        Message: &actions.ResultMessage{
            Header: "All validation checks passed.",
        },
    }, nil
}
```

### Roadmap for `azd validate`

The checks listed below address the most common provisioning failure patterns identified
in the community. They can be implemented incrementally:

1. **Authentication & identity** *(available now as `AuthCheck`)*
   - User is logged in with valid, non-expired credentials.

2. **Environment targeting** *(available now as `SubscriptionCheck`)*
   - `AZURE_SUBSCRIPTION_ID` and `AZURE_LOCATION` are set.
   - Subscription is accessible to the signed-in identity.

3. **Resource provider registration** *(planned)*
   - All resource providers required by the Bicep template are registered in the target
     subscription. Unregistered providers are a top source of silent deployment failures.

4. **RBAC / role assignments** *(planned)*
   - The signed-in identity (user or service principal) has at least `Contributor` on the
     target subscription or resource group, plus any resource-specific roles required by
     the template (e.g., `Key Vault Administrator`, `Cognitive Services Contributor`).

5. **Quota / capacity** *(planned, longer-term)*
   - The target region has sufficient quota for the SKUs requested by the template.
   - Particularly relevant for GPU-intensive AI workloads and OpenAI capacity.

6. **Required environment variables** *(planned)*
   - All `required` parameters in the Bicep template (those without a default value) are
     provided, either via `.env` file, shell environment, or `azure.yaml` configuration.

### Opting out of local preflight

Users who want to skip both the local preflight and the ARM template validation can set:

```bash
azd config set provision.preflight off
```

This is useful in advanced CI/CD scenarios where the checks are redundant (e.g., a managed
identity with known permissions is always used).
