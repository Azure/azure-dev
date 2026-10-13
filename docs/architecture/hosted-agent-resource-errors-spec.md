# Hosted Agent Resource Error Guidance

- **Status:** Proposed for product review
- **Date:** 2026-10-13
- **Component:** `azure.ai.agents` extension
- **PR scope:** Specification only; no runtime behavior changes.

## Summary

When Foundry Service rejects a hosted agent's CPU or memory configuration,
`azd deploy` currently shows a technical HTTP error and the service's JSON
response. The rejection is not hidden, but users must connect the response back
to the correct service and configuration values themselves.

This proposal makes that failure actionable: identify the service, show the
resource values actually submitted, retain the service's explanation, and tell
the user where to make the correction and how to retry.

It does **not** add a resource tier, change which tiers are accepted, or announce
when a tier becomes available in a region. Foundry Service remains the authority
for supported resource combinations.

## User problem and motivation

A developer changes a hosted agent's resources, for example to request more CPU.
Deployment can fail because the requested tier is unavailable or because CPU
and memory do not form a supported pair.

The current error has three usability gaps:

1. It identifies an HTTP operation and agent API URL, not necessarily the
   `azure.yaml` service the user needs to edit.
2. It does not include the effective CPU and memory values in the error summary.
   Those values can differ from what the user expects when a field is omitted.
3. It does not provide a configuration path or a retry command.

Users may have seen the allocation earlier in deployment output, but the error
should remain understandable on its own, including when copied from a CI log.

### Current configuration and validation boundaries

| Configuration | Current behavior | Scope of this proposal |
| --- | --- | --- |
| CPU `"4"` and memory `"4Gi"` | Local parsing succeeds; a service that does not accept this pair rejects the request. | Improve the service rejection message. |
| CPU `"8"` and memory `"16Gi"` | Local parsing succeeds; Foundry decides whether this tier is supported. | Improve the message if the service rejects it. |
| CPU `"4"` with memory omitted | azd submits memory `"1Gi"`, its existing default; the service may reject the resulting pair. | Show both effective values in the error. |
| Numeric `cpu: 4` | Local parsing fails because the CPU field expects a string. | Unchanged; improving local type errors is separate work. |
| Unknown `core` instead of `cpu` | The resource Schema rejects it, but CLI parsing can ignore it and apply the CPU default. | Unchanged; this is a separate validation gap. |

The existing [resource Schema](../../cli/azd/extensions/azure.ai.agents/schemas/azure.ai.agent.json)
already checks field names, types, and string formats. Editor diagnostics do not
automatically block a terminal command, and CLI execution does not apply every
Schema rule.

The unknown-field case is important: if defaults form a valid pair, deployment
can succeed without an error. Better error presentation cannot fix that case.
Rejecting unknown resource fields needs a separate, explicitly scoped change.

## Before and after

> These are **offline reproductions**, not logs from a live Azure deployment.
> The same simulated HTTP 400 response was passed through the real Azure SDK,
> extension error serialization, and azd error formatter before and after a local
> prototype. The URLs, service name, error code, and supported pairs are test
> fixtures. A production response may contain a different set of supported pairs.

The example request contains CPU `"4"` and memory `"4Gi"` for service `my-agent`.

### Before: technical response without a correction step

The existing error is reproduced below as an excerpt; repeated JSON details
and separator lines are omitted.

```text
ERROR: create_agent: POST https://example.test/agents/my-agent/versions
RESPONSE 400: 400 Bad Request
ERROR CODE: invalid_parameters
{
  "error": {
    "message": "CPU and Memory must be specified as a valid resource tier. Valid tiers: (0.25, 0.5Gi), (0.5, 1Gi), (1, 2Gi), (2, 4Gi), (4, 8Gi)",
    "param": "definition",
    "type": "invalid_request_error"
  }
}
```

### After: contextual error and an actionable suggestion

```text
ERROR: Foundry rejected the resource configuration for service "my-agent" (CPU="4", memory="4Gi").
CPU and Memory must be specified as a valid resource tier. Valid tiers: (0.25, 0.5Gi), (0.5, 1Gi), (1, 2Gi), (2, 4Gi), (4, 8Gi)
Service response: HTTP 400 (create_agent.invalid_parameters).

Suggestion: Update container.resources.cpu and container.resources.memory for service "my-agent" in azure.yaml (or its $ref file) to a supported pair, then retry 'azd deploy "my-agent"'.
```

The same presentation applies when the service rejects another requested tier.
If only CPU was supplied, the summary would show the effective values
`CPU="4", memory="1Gi"` rather than implying that memory was calculated from CPU.

Supported combinations come from the service response, not a new list in azd.

## Proposed behavior

For a confidently identified hosted-agent CPU or memory validation failure:

1. Identify the **azd service name**, even when it differs from the Foundry agent
   name.
2. Show the **CPU and memory actually submitted**, including existing defaults.
3. Show the **service's validation explanation**, retaining supported
   combinations when the service supplies them. Do not invent a supported list.
4. Retain the **HTTP status, service error code, and request ID**, when available.
5. Point to `container.resources.cpu` and `container.resources.memory` on that
   service in `azure.yaml`, or its explicit `$ref` file.
6. Suggest retrying `azd deploy "<service>"` after the user corrects the values.
7. Keep the command unsuccessful; do not retry automatically, change resources,
   or report a successful deployment.

The guidance must survive the extension-to-host error boundary and remain
available in non-interactive runs. It must not introduce new telemetry fields or
change the existing service-error classification.

Only resource validation receives this presentation. Authentication,
authorization, quota, network, unrelated validation, and server failures retain
their existing handling. If a response cannot be safely identified, or combines
resource errors with unrelated violations, preserve the existing error rather
than hiding another problem.

Error rendering must not disclose credentials from request URLs.

## Scope and non-goals

### In scope

- Hosted container agent version creation, including creation of a new version
  for an existing agent.
- Hosted code/ZIP agent creation and updates.
- Resource-specific service error guidance, regression tests, and user-facing
  documentation.

### Not in scope

- New CPU, memory, or SKU-selection flags or prompts.
- Changes to the default allocation: CPU `"0.5"` and memory `"1Gi"`.
- A client-maintained SKU or CPU/memory-pair allowlist.
- Changes to editor Schema association or full runtime Schema validation.
- Fixing unknown resource fields or local numeric/string type errors.
- Changes to successful deployment behavior or the deployment API contract.
- Foundry regional rollout, quota policy, or resource availability.
- Resource selection in the Foundry Toolkit graphical deployment flow.

## Why this approach

| Alternative | Decision |
| --- | --- |
| Keep the raw HTTP/JSON error only | Retains diagnostics, but leaves users to infer the affected service and correction. |
| Add a client SKU allowlist | Not proposed. It duplicates service policy and can reject tiers the service adds later. |
| Rely only on editor Schema diagnostics | Not sufficient for terminal and CI users; existing format rules do not describe dynamic service availability. |
| Introduce full runtime Schema validation now | Separate behavior change with a wider compatibility scope than error presentation. |
| Improve service error presentation | Proposed first step: improves recovery without changing accepted configurations or defaults. |

This is deliberately a presentation change, not a promise that every invalid
configuration will be caught before a deployment request is sent.

## Acceptance criteria

- [ ] A mismatched CPU/memory rejection shows the service name, submitted values,
  service explanation, and configuration correction.
- [ ] A rejected tier is explained using the service response, without adding a
  client allowlist.
- [ ] An omitted value is represented by the effective default sent to the
  service, not by an inferred value.
- [ ] Container and code/ZIP create/update paths provide consistent guidance.
- [ ] The HTTP status, service error code, request ID when present, and service
  validation reasons remain available.
- [ ] Guidance remains intact after extension error serialization.
- [ ] Unrelated errors, malformed responses, and mixed validation failures retain
  the existing diagnostic information.
- [ ] No request URL credentials appear in the enhanced message or suggestion.
- [ ] Existing success behavior, exit behavior, defaults, and Schema rules remain
  unchanged.

## Evidence and rollout

Current behavior was investigated against azd commit
`f2a9f03b1152086d6cb4e0ae941fdb8ca120e02c`. Offline tests exercised resource parsing,
service errors, error serialization, and the proposed presentation.

A local prototype also exercised container version creation and code/ZIP
creation and update paths with intercepted HTTP responses. This spec PR does
not include that implementation.

After product review, an implementation PR should carry the regression tests
and documentation. Live verification should confirm the actual error returned
by an authorized test project and region; no live deployment was performed for
the examples in this spec.

No new feature flag is proposed for this targeted error-presentation change.
This document does not provide a service deployment date or SKU availability
guarantee.

## Product review checklist

- [ ] Agree that service resource rejection guidance is the first delivery scope.
- [ ] Approve showing effective CPU/memory values and the service's supported
  combinations in the error.
- [ ] Approve the proposed correction and retry wording.
- [ ] Keep unknown-field rejection and local type-error wording as separate
  follow-up work, rather than implying that this proposal fixes them.

## Implementation references

- [Existing deployment paths](../../cli/azd/extensions/azure.ai.agents/internal/project/service_target_agent.go)
- [Existing Azure service error conversion](../../cli/azd/extensions/azure.ai.agents/internal/exterrors/errors.go)
- [Extension error serialization](../../cli/azd/pkg/azdext/extension_error.go)
- [Error presentation](../../cli/azd/cmd/middleware/ux.go)
- [Agent deployment documentation](../../cli/azd/extensions/azure.ai.agents/README.md#deploying-agents)
