# Hosted agent memory snapshots in azd

Status: Proposed. The YAML property name and placement require PM confirmation.
This document proposes azd integration, not changes to the snapshot protocol.

Related work:

- [Hosted agent snapshot hook specification](https://github.com/coreai-microsoft/foundrysdk_specs/pull/354).
- [Python AgentServer snapshot hook implementation](https://github.com/Azure/azure-sdk-for-python/pull/49217).

## Problem

Foundry can capture an initialized hosted agent process as a version-level memory
snapshot and restore it when preparing session runtimes. This avoids repeating
startup work; it is not a backup of conversation history or each session's latest
memory. The service opt-in field is `definition.enable_memory_snapshot`.

The `azure.ai.agents` extension currently constructs deployment requests without
this field. A compatible hosting library alone therefore does not enable the
feature through azd.

## Configuration and usage

Propose an optional boolean `enableMemorySnapshot` on the hosted agent service in
`azure.yaml`, alongside existing agent definition properties. The following is a
configuration fragment; other deployment settings are omitted:

```yaml
services:
  agent:
    host: azure.ai.agent
    project: ./src/agent
    enableMemorySnapshot: true
```

Use the existing `azd deploy` or `azd up` command. No new CLI flag or command is
proposed. Support both container-image and code deployments, including supported
inline and referenced agent definitions. The setting applies to the new version
being deployed, not a mutation of an existing version.

| YAML setting | Deployment request |
| --- | --- |
| Absent | Omit `definition.enable_memory_snapshot`; preserve existing behavior |
| `false` | Send `definition.enable_memory_snapshot: false` |
| `true` | Send `definition.enable_memory_snapshot: true` |

Reject non-boolean values and use of the setting on non-hosted agents with an
actionable configuration error. Do not modify YAML during deployment.

## Proposed behavior

1. Preserve the optional value through configuration parsing, schema validation,
   and request mapping in the agent extension's existing deployment provider.
   No change to the core deploy command is required.
2. Leave snapshots disabled unless explicitly requested. Do not detect a hosting
   package and silently enable snapshots or change existing projects' dependencies.
3. Let the service validate subscription and regional availability. Do not maintain
   a client-side region list. An unavailable opt-in fails deployment with the
   service's validation error; do not retry with snapshots disabled.
4. Report success only after the service confirms the version is active, including
   snapshot capture when enabled. Check the existing polling budget against capture
   timing. On timeout, explain that the final outcome is unknown and direct the
   user to `azd ai agent show` rather than claiming capture failed.
5. Surface public capture errors and the failure stage and request ID when supplied.
   Provide log investigation guidance for callback failures. Do not echo raw hook
   responses or promise detailed callback exception text: the service currently
   returns platform-generated safe messages, not the callback's custom error body.

## Compatibility and ownership

Enabling requires a snapshot-compatible hosting library or a custom server that
implements both private hooks. Foundry owns capture, restore, and hook invocation;
the hosting library owns protocol handling. Agent authors handle any application
resources that require cleanup before capture or rebuilding after restore.
The azd extension does not implement or invoke these hooks.

Update snapshot-supported templates in their source repository and azd-owned test
fixtures after a compatible SDK release is available. Update dependency declarations
and lock files together; rebuild prebuilt images where applicable. Confirm the full
hosting dependency combination, not only the core package version. Existing user
projects are not automatically upgraded, and template updates alone do not opt in.

The current service policy for ordinary customer opt-in does not preserve session
state across idle replacement and provides no session data volume. Confirm the
release policy with the platform team and document it before opening this feature.
Do not describe snapshots as a transparent optimization with unchanged durability.

## Validation

- Test absent, false, and true mapping for both deployment modes, including referenced
  definitions; verify requests for existing projects remain unchanged.
- Test invalid configuration, unavailable opt-in, capture failure, and polling timeout.
- In an enabled environment, verify capture, first-session restore, idle replacement,
  correct session identity, and subsequent agent invocation using the chosen template.
- Verify missing hooks produce an actionable deployment failure. Separate local hook
  tests from real snapshot restoration evidence.

## Decisions to confirm

- Is `services.<name>.enableMemorySnapshot` the desired YAML name and placement?
- Is YAML-only, default-off behavior the agreed first-release experience?
- Which published hosting dependencies, service API contract, and enabled test
  environment are approved for validation and release?
- What session durability behavior and capture timing should the released feature
  document, and where can users retrieve capture-time agent diagnostics?