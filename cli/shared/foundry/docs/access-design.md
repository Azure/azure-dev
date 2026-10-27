# Access package design

This is the access-specific design accompanying the independent-module work in
#9834, not a bulk move of product-specific Azure clients into shared code.

## Boundary

The module owns a narrow Azure backend for ARM project endpoint verification and
the current principal's project-scoped Foundry User assignment. These primitives
are needed by at least toolboxes and agents, with the other consumers listed in
the module README. It does not own agent/toolbox/skill/dataset CRUD, deployment
orchestration, hooks, console output, configuration persistence, or gRPC clients.

`IdentityResolver` returns a module-owned `Principal` and a tenant ID.
`Environment` reads named values through plain Go methods. Extensions adapt the
existing host account/environment APIs to these interfaces. Credential providers
must authenticate the same identity as the resolver. The default uses the Azure
Identity SDK's azd CLI credential at runtime; callers can supply another provider.
Neither option adds a Go dependency on azd core or requires a newer core release
to consume changes to this library.

## Readiness and grant policy

`Service.Ensure` returns `Allowed`, `Denied`, or `Unknown`. Callers must inspect
both the state and error. Unknown/error results never authorize a role write.
`CheckOnly` is the zero/default policy. Extensions explicitly select
`GrantFoundryUser` to preserve documented automatic grants for remote mutations
and deployment; local/read-only/preview commands must not silently opt into it.
The extension owns its skip setting, consent policy, warnings, and command selection.

Probes must be non-mutating and capability-specific. `CollectionReadProbe` checks
only a read prerequisite; it does not prove subsequent write authorization. Its
public-cloud implementation recognizes only the specific Foundry RBAC error
identifying the verified current principal and an AIServices read action. Network
403s, other identities, and ambiguous errors remain unknown. Other clouds or
capabilities supply their own probe.

`ResolveEnvironment` never attaches an explicit endpoint to a different
environment's resource ID. The Azure backend verifies the full endpoint advertised
by ARM, supporting custom account subdomains. It does not infer ARM account names
from DNS. Failed ARM verification cannot trigger a grant.

## Concurrency and propagation

There is no persistent or process-wide access cache. Each call checks its own
target and identity. New role assignments use a deterministic project/principal/
role GUID. Compatible unconditional existing assignments are reused. A
`RoleAssignmentExists` conflict is accepted only after compatibility is verified;
unrelated/conditional assignments are not treated as effective access.

The backend rechecks identity before a write. Grants are limited to Foundry User,
the signed-in principal, and the verified project scope. Assignment success is
not readiness: the probe must return Allowed after bounded, cancellable
propagation checks. Unknown or probe errors stop retries. Failed operations do
not replay the intended write or delete an assignment already created.

## Errors

`access.Error` exposes a stable `Kind`, `Code`, safe `Message`, `Suggestion`, and
an unwrap-capable `Cause`. Its rendered message omits cause text, which may contain
credential-bearing URLs. It imports no extension SDK for error plumbing.
Extensions translate it to their established structured-error representation at
their boundary. Context and Azure errors remain available through `errors.Is`
and `errors.AsType` for classification; warning-only consumers must not silently
convert failure into readiness.
