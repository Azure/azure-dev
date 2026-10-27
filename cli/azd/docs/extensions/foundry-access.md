# Shared Foundry project access

`pkg/foundryaccess` provides an opt-in prerequisite for Foundry data-plane
operations across independently versioned AI extensions. It addresses the
architecture behind [#10718](https://github.com/Azure/azure-dev/issues/10718): a
toolbox or another dependency can require developer access before an agent's
predeploy hook runs.

**This infrastructure does not change any command or provisioning behavior.**
Extensions must adopt it in a subsequent release. It does not replace hosted-agent
identity grants, ACR permissions, or service-specific roles for other Azure services.

## Responsibilities

| Component | Responsibility |
| --- | --- |
| Extension | Resolve the intended project, choose a capability-specific read-only probe, authorize the grant policy, interpret the result, and render diagnostics. |
| `Service` | Probe access, never grant on unknown/error results, and verify propagation after an explicitly authorized grant. |
| `AzureBackend` | Resolve tenant and current principal through azd, verify the ARM/data-plane project binding, and ensure the current principal's project-scoped Foundry User assignment. |

The host already exposes `Account.LookupTenant` and `Account.GetCurrentPrincipal`.
No additional gRPC service or extension dependency is required. The default
credential is an `AzureDeveloperCLICredential` scoped to the host-selected
user-access tenant. Principal resolution uses the host's resource-tenant identity
API, supporting guest users and service principals without Graph `/me` or local
token decoding. Alternate credential providers must authenticate the same identity
as the host account client.

## Integration boundary

Call `Ensure` after resolving the target context and before the first protected
cloud operation. Cover **both** service-target deployment and standalone remote
commands; do not rely on an agent predeploy hook or on another service's order.

Do not invoke the component during local authoring, help, schema validation,
preview, or infrastructure provisioning. Provisioning must remain usable without
permission to create developer role assignments.

An extension can construct the component using:

```go
backend, err := foundryaccess.NewAzureBackend(azdClient.Account(), nil)
if err != nil {
    return err
}
access, err := foundryaccess.NewService(backend, nil)
if err != nil {
    return err
}
result, err := access.Ensure(ctx, foundryaccess.Request{
    Target: foundryaccess.Target{
        ProjectID: projectResourceID,
        Endpoint:  projectEndpoint,
    },
    Operation: "toolboxes.write",
    Policy:    foundryaccess.CheckOnly,
    Probe:     probeToolboxWriteAccess,
})
```

In this example, `probeToolboxWriteAccess` is an extension-supplied implementation
of `foundryaccess.Probe`, not a function shipped by the package. Implement it
against the service's supported non-mutating authorization mechanism. The probe
must use the supplied verified project and credential and honor context cancellation.

Extensions must check `result.State` **and** `err`. Only `Allowed` with no error
confirms readiness. An extension may surface `Unknown` as a diagnostic and attempt
the original operation under its own policy, but it must not silently turn
`Unknown` into permission to grant a role. Do not retry an uncertain write as an
access probe.

## Access evidence and grant policy

- `Allowed`: authoritative evidence of access for the requested capability,
  regardless of whether access comes from inherited assignments, groups, or a
  custom role. A successful read alone does **not** establish write permission.
- `Denied`: a confirmed RBAC denial for that capability and identity. A generic
  HTTP 403, private-network restriction, policy failure, or absence of a familiar
  role ID is not sufficient evidence.
- `Unknown`: insufficient evidence, including ambiguous errors. If the service
  has no suitable non-mutating check, return `Unknown` rather than guessing.

The default policy is `CheckOnly`. For deployments, remote mutations, or explicit
access setup, callers may use `GrantFoundryUser` through consent, an explicitly
configured authorization, or documented automatic-grant command behavior.
`--no-prompt` alone is not authorization. Read-only
commands and diagnostics should use `CheckOnly`.

`GrantFoundryUser` authorizes only the current principal's Foundry User role at
the verified project scope. It cannot grant an arbitrary role, grant another
principal access, or widen scope to an account/subscription. Do not use this
policy for capabilities that require a different role.

Both the project ARM ID and endpoint are required. The backend compares the
endpoint with ARM's advertised `AI Foundry API` endpoint; it does not infer an ARM
account name from DNS. If the identity cannot read the ARM project, verification
returns an error and no grant is attempted. Endpoint-only commands must retain
their ordinary API path with appropriate diagnostics, or obtain a verified ARM
binding before opting into grants. Never pair an explicit endpoint with an
unrelated active environment's project ID.

## Concurrency, propagation, and errors

- Services/backends are safe for concurrent calls and retain no access cache.
  Each call resolves its own project and identity and rechecks capability access.
  An operation can still fail if permissions are revoked after the check.
- Assignment names are deterministic for the project, principal, and role.
  Existing unconditional assignments are reused. After `RoleAssignmentExists`,
  the backend accepts the conflict only if it can find the matching unconditional
  assignment at the exact project scope. Other conflicts remain errors.
- Identity and endpoint are reverified before a grant; a changed principal or
  tenant fails rather than receiving a role based on another identity's probe.
- The backend reads role assignments only when a grant is explicitly authorized
  after a confirmed denial, not to infer effective access for ordinary checks.
- After ensuring a grant, the service immediately probes again, then retries
  confirmed denials with exponential backoff (one second, capped at ten seconds).
  The assignment and propagation phase defaults to two minutes and respects a
  shorter caller deadline. `Unknown` or probe errors stop retries immediately.
- `AssignmentCreated` reports a successful creation by this call;
  `GrantEnsured` also includes existing/concurrently created assignments. Neither
  field establishes effective access; only a subsequent `Allowed` probe does.
- Failure/cancellation does not delete a grant that was already created. Return
  the result alongside the error so callers can explain this partial outcome.
- Errors preserve underlying Azure/gRPC/context errors for caller classification.
  The package prints nothing and performs no telemetry or local persistent writes.

## Adoption and validation

`Prepare` is the optional SDK adapter for environment-bound extension operations.
Pass the resolved endpoint, selected environment name, and a collection name.
With `AutoGrant: true`, it preserves the agents extension's documented automatic
Foundry User grant on deploy/mutation commands. It honors
`AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true` in either the azd environment or process.
Read-only callers leave `AutoGrant` false. Warnings use the supplied callback;
grant/check failures remain warning-only, while cancellation is propagated.

The adapter verifies the environment endpoint matches the selected target before
using `AZURE_AI_PROJECT_ID`. It skips automatic grants for endpoint-only commands
without an environment binding. Its GET collection probe confirms only the read
prerequisite, not permission to perform a subsequent write. A grant is permitted
only for the specific Foundry `UserError`/`ForbiddenError` RBAC message naming
the verified current principal and an AIServices read action. Network/policy
403s, other identities, and ambiguous errors never trigger a grant.

Land and release the core azd module first. Extensions should then consume that
published version and replace only their shared developer-project-access logic.
Do not merge temporary local module replacements. Keep agent/ACR checks separate.

For each extension, test standalone commands as well as a deployment graph whose
first data-plane consumer is a non-agent service. Cover inherited/custom access
without role-assignment write permission, an authorized fresh-project grant,
denied grants, ambiguous/network 403s, guest and application identities,
endpoint/ARM mismatches, concurrent first grants, propagation timeouts, and
cancelled operations. Do not require live role changes in ordinary unit tests.
