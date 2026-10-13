# Hosted agent resource error guidance in azd

Status: Proposed. Error wording and first-delivery scope require PM confirmation.
This spec proposes error guidance, not changes to accepted resource tiers.

## Problem

When Foundry Service rejects a hosted agent's CPU or memory configuration,
`azd deploy` shows a technical HTTP error and JSON response. It retains the
service explanation, but the error summary does not identify the azd service,
the effective allocation, or the configuration location and retry command.

Developers must infer what to edit, particularly when a field was omitted or
they are reading copied terminal output or CI logs. Improving this guidance does
not require a new CPU/memory flag or a client-maintained SKU allowlist.

| Example | Current behavior |
| --- | --- |
| CPU `"4"`, memory `"4Gi"` | The service rejects the request if it does not support the pair. |
| CPU `"8"`, memory `"16Gi"` | Foundry decides whether the requested tier is supported. |
| CPU `"4"`, memory omitted | azd submits its existing memory default, `"1Gi"`; the resulting pair may be rejected. |

## Before and after

These are offline reproductions using the same simulated HTTP 400 response,
the real SDK, extension error serialization, and azd formatter. They are not
live Azure logs. Supported pairs below are test fixtures, not a regional
availability guarantee.

For service `my-agent`, with CPU `"4"` and memory `"4Gi"`:

Before, excerpt with repeated JSON details omitted:

```text
ERROR: create_agent: POST https://example.test/agents/my-agent/versions
RESPONSE 400: 400 Bad Request
ERROR CODE: invalid_parameters
{
  "error": {
    "message": "CPU and Memory must be specified as a valid resource tier. ..."
  }
}
```

Proposed:

```text
ERROR: Foundry rejected the resource configuration for service "my-agent" (CPU="4", memory="4Gi").
CPU and Memory must be specified as a valid resource tier. Valid tiers: (0.25, 0.5Gi), (0.5, 1Gi), (1, 2Gi), (2, 4Gi), (4, 8Gi)
Service response: HTTP 400 (create_agent.invalid_parameters).

Suggestion: Update container.resources.cpu and container.resources.memory for service "my-agent" in azure.yaml (or its $ref file) to a supported pair, then retry 'azd deploy "my-agent"'.
```

If memory was omitted, show `CPU="4", memory="1Gi"` so the error reflects the
values sent, rather than implying that memory was calculated from CPU.

## Proposed behavior

1. Cover container version creation and code/ZIP creation and updates through
   the existing deployment provider; no core command change.
2. Identify the azd service, even when its name differs from the Foundry agent
   name, and show the effective CPU and memory values submitted.
3. Retain the service's resource validation explanation and supported pairs
   when supplied. Do not invent or maintain a client-side supported-tier list.
4. Retain HTTP status, service error code, and request ID when available.
   Guidance must survive extension error serialization and work in CI.
5. Point to the service's `container.resources.cpu` and `memory` in `azure.yaml`
   or its `$ref` file, and suggest retrying `azd deploy "<service>"`.
6. Only enhance confidently identified resource validation failures. Preserve
   existing handling for unrelated, ambiguous, or mixed validation errors.
   Do not disclose request URL credentials.
7. Keep deployment unsuccessful. Do not retry automatically or silently change
   the allocation.

## Scope

Keep the current defaults: CPU `"0.5"` and memory `"1Gi"`. Do not change the API
contract, successful deployments, editor Schema association, or CLI validation
rules. This proposal does not promise resource-tier availability or a release date.

Unknown `core` instead of `cpu` can be ignored by CLI parsing and result in a
successful default-sized deployment, despite an editor Schema error. That needs
separate validation work; error guidance cannot fix a failure that never occurs.
Improving numeric/string type-error wording and graphical resource selection
are also separate work.

## Validation

- Test rejected tiers, mismatched pairs, and effective defaults across container
  and code/ZIP create/update paths.
- Verify service reasons, status/code/request IDs, and suggestions survive error
  serialization; unrelated and malformed responses retain existing diagnostics.
- Verify no request URL credentials appear in the enhanced output.
- Confirm actual service errors in an authorized test environment separately.
  No live deployment was performed for the examples in this spec.

## Decisions to confirm

- Is service resource rejection guidance the agreed first-delivery scope?
- Is showing effective allocation and service-provided supported pairs the
  desired error experience?
- Is the correction and retry wording appropriate for terminal and CI users?
- Should unknown-field rejection and local type-error guidance remain separate
  follow-up work?
