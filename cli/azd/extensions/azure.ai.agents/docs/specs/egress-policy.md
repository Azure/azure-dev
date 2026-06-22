# Spec: azd Support for Hosted-Agent Egress Policy

- **Owner:** zhihuan
- **Extension:** `azure.ai.agents`
- **Base branch:** `huimiu/foundry-azure-yaml`

## Upstream dependencies

| Surface | PR / Spec | What it adds |
|---------|-----------|--------------|
| Data-plane (Foundry) | [`Azure/azure-rest-api-specs#43399`](https://github.com/Azure/azure-rest-api-specs/pull/43399) | `egress_policy_id_preview?: string` on `HostedAgentDefinition`, gated by the `EgressPolicy=V1Preview` opt-in. |
| Control-plane (ARM) | [`Azure/azure-rest-api-specs#43974`](https://github.com/Azure/azure-rest-api-specs/pull/43974) | `Microsoft.CognitiveServices/accounts/{account}/agentGuardrails/{name}` resource (`2026-05-15-preview`). |
| Experience spec | [`coreai-microsoft/foundrysdk_specs#176`](https://github.com/coreai-microsoft/foundrysdk_specs/pull/176) | Product context: hosted-agent egress policy journey and the data-plane binding contract. |

> Both REST PRs are **OPEN with unresolved review**. The field name
> (`egress_policy_id_preview`), opt-in key (`EgressPolicy=V1Preview`), resource type
> (`agentGuardrails`), and API version (`2026-05-15-preview`) must finalize upstream
> before azd implementation lands. See [Risks](#8-risks).

## 1. Summary

`azure.yaml` is the source of truth for hosted agents: an agent is declared inline under
`services.<name>.agents[]` with `host: microsoft.foundry`. This feature lets a developer declare an
egress policy for such an agent in `azure.yaml`. Two coordinated pieces of azd behavior:

- **Tier A (data-plane binding):** carry the policy resource ID from the `azure.yaml` agent block
  into the hosted-agent version definition as `egress_policy_id_preview` during `azd deploy`.
  Omitting it preserves current behavior.
- **Tier B (provisioning):** during `azd provision`, synthesize and create the referenced
  `agentGuardrails` ARM resource for greenfield projects, and feed its resource ID back so Tier A
  can bind the version to it.

The hosted-agent definition carries only a **reference** to the policy resource; it does not embed
the rule body.

## 2. Scope

### In scope — Tier A (data-plane binding)

- An `egressPolicy` field on the `azure.yaml` hosted-agent block (`services.<name>.agents[]`),
  plus the corresponding schema and Go struct.
- Mapping that field into the hosted-agent version definition (`egress_policy_id_preview`) for both
  **image** and **code** deploy modes.
- Adding the `EgressPolicy=V1Preview` opt-in to the `Foundry-Features` header on the
  create / version / code-deploy requests.
- Structured error mapping for policy-reference validation and authorization failures.

### In scope — Tier B (provisioning)

- Synthesizing an `agentGuardrails` resource into the provisioning template from a greenfield
  `azure.yaml` declaration.
- Regenerating the compiled ARM template.
- Surfacing the synthesized resource ID as a provisioning output so `azd deploy` can bind it.

### Out of scope

- A rich authoring UX for the full rule body (FQDN rules, `Transform` / `Rewrite` actions, secret
  or managed-identity header injection) beyond pass-through of what is declared. Rule bodies are
  authored in Bicep or out-of-band control-plane tooling.
- Runtime `egress_policy_denied` handling beyond rendering the structured failure cleanly at
  `azd ai agent invoke` time. Enforcement and denial are owned by the data plane.
- Policy hot-reload into running sessions.
- MCP Tool Policies, PII detection, traffic analysis, audit-log exploration, or SIEM hooks.
- Brownfield synthesis of the policy resource (when `endpoint:` is set, the policy ID is
  pass-through only).

## 3. azure.yaml shape

The egress policy is declared on the hosted-agent block in `azure.yaml`. Two forms, chosen by the
developer based on whether azd owns the policy resource:

**Form A — declare a policy azd provisions (greenfield).** The developer does **not** supply a
resource ID. They give the policy a `name` (and rule body); `azd provision` creates
`.../agentGuardrails/<name>`, and azd resolves the resulting resource ID and binds it at
`azd deploy` time (see [4.6 Policy ID resolution](#46-policy-id-resolution)). This is the primary
form and avoids the chicken-and-egg of needing an ID before the resource exists.

```yaml
services:
  ai:
    host: microsoft.foundry
    agents:
      - name: support-agent
        kind: hosted
        project: ./agents/support
        runtime:
          stack: python
          version: "3.12"
        startupCommand: python main.py
        protocols:
          - protocol: responses
            version: "v0.1.1"
        egressPolicy:
          name: support-agent-egress
          defaultAction: Deny        # Allow | Deny (required by the ARM resource)
          rules:
            - name: allow-openai
              ruleType: Fqdn
              match: { host: "*.openai.com" }
              action: { actionType: Allow }
```

**Form B — reference an existing policy by resource ID.** Use this only when the
`agentGuardrails` resource **already exists** (created out-of-band, by an earlier `azd up`, or in a
brownfield project). The developer supplies its full ARM resource ID; azd does not provision it.

```yaml
        egressPolicy:
          id: /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.CognitiveServices/accounts/<account>/agentGuardrails/<name>
```

- `egressPolicy.name` + body (**Form A**) is the greenfield declaration azd synthesizes (Tier B).
  The developer never writes a resource ID; azd resolves it after provisioning.
- `egressPolicy.id` (**Form B**) is the **full ARM resource ID** of an *already-existing*
  `agentGuardrails` resource (full ARM ID only — see [Decisions](#9-decisions)).
  Do not use `id` for a resource that does not exist yet — the ID is not knowable until the resource
  is provisioned.
- Exactly one of `name` (declare) or `id` (reference) is expected. This is the new field; the
  `HostedAgent` schema today has `additionalProperties: false` and no policy field, so it must be
  added explicitly.

## 4. Tier A design (data-plane binding)

All paths are under `cli/azd/extensions/azure.ai.agents/`.

### 4.1 azure.yaml deploy translation

Tier A binds the policy ID into the **hosted-agent version definition** at deploy time, built from
the `azure.yaml` agent block. The deploy path decodes `services.<name>.agents[]` into a Go struct
for the hosted-agent block (name, kind, image/docker/runtime, project, startupCommand,
container.resources, protocols, env, toolboxes — plus the new `egressPolicy`) and maps it to
`agent_api.HostedAgentDefinition`. The egress-policy work below attaches to that azure.yaml → payload
mapping; the `egressPolicy` field flows through the same translation as every other agent field.

### 4.2 Schema

`schemas/Agent.json`

Add an `egressPolicy` property to the `HostedAgent` definition (`Agent.json:52-138`, which is
`additionalProperties: false`). Shape:

```json
"egressPolicy": {
  "type": "object",
  "description": "Restricts hosted-agent application egress to a referenced agentGuardrails policy.",
  "additionalProperties": false,
  "oneOf": [
    { "required": ["id"] },
    { "required": ["name", "defaultAction"] }
  ],
  "properties": {
    "id":            { "type": "string", "description": "Full ARM resource ID of an existing agentGuardrails resource." },
    "name":          { "type": "string", "description": "Name of a policy azd provisions for greenfield." },
    "defaultAction": { "type": "string", "enum": ["Allow", "Deny"] },
    "rules":         { "type": "array", "items": { "type": "object" } }
  }
}
```

The `microsoft.foundry.json` service schema needs no change (it references `Agent.json`).

### 4.3 azure.yaml agent struct

The hosted-agent Go struct in the deploy translator (see [4.1](#41-azureyaml-deploy-translation))
gains an optional field:

```go
EgressPolicy *EgressPolicy `yaml:"egressPolicy,omitempty"`

type EgressPolicy struct {
    ID            string         `yaml:"id,omitempty"`
    Name          string         `yaml:"name,omitempty"`
    DefaultAction string         `yaml:"defaultAction,omitempty"`
    Rules         []EgressRule   `yaml:"rules,omitempty"`
}
```

### 4.4 Validation

Validate the agent block at deploy time (and ideally surface in `azd ai agent doctor`):

- `egressPolicy` with `id`: `id` must be non-empty and well-formed
  (`.../providers/Microsoft.CognitiveServices/accounts/<account>/agentGuardrails/<name>`).
- `egressPolicy` with `name`: `name` and `defaultAction` are required (Tier B will synthesize it).
- Reject providing both `id` and a `name` + body.

### 4.5 Mapping

In the azure.yaml deploy mapping, resolve the bound policy ID (see
[4.6 Policy ID resolution](#46-policy-id-resolution)) and set it on the `HostedAgentDefinition` as
`egress_policy_id_preview` for **both** image and code deploy paths.

### 4.6 Policy ID resolution

This is the heart of how a policy resource ID gets bound onto an agent, and it differs by form:

**Form B (`egressPolicy.id`):** the developer already supplied the full ARM resource ID. Use it
directly as the binding. No resolution needed.

**Form A (`egressPolicy.name` + body):** the developer supplied only a name, because the resource
ID is **not knowable until `azd provision` creates the resource**. azd resolves the ID **after
provisioning**, at `azd deploy` time, by one of:

1. **Provisioning output (preferred).** Tier B's `resources.bicep` emits the created resource's ID
   as an output (e.g. `AZURE_AI_AGENT_GUARDRAILS_ID`). `azd provision` writes provisioning outputs
   into the azd environment; `azd deploy` reads that value and uses it as the binding. This is the
   normal azd provision→deploy data flow and keeps the ID authoritative (the actual ARM ID of the
   created resource).
2. **Deterministic construction (fallback).** The ARM ID is fully determined by
   `subscription / resourceGroup / account / agentGuardrails/<name>`. Since azd knows the
   subscription, resource group, and Foundry account after provisioning, it can compose the ID from
   the declared `name` without reading an output. Use this only if a provisioning output is
   unavailable; it is more brittle (must track the exact account name and resource-type casing).

In both Form A mechanisms the developer **never writes the ID into `azure.yaml`** — the name is the
stable handle, and the ID is derived. If multiple agents reference the same policy `name`, they all
resolve to the same ID. The order is enforced by azd's normal lifecycle: `provision` (creates the
resource + publishes the ID) runs before `deploy` (reads the ID + binds it). On `azd up` both run in
sequence; running `azd deploy` against an un-provisioned Form A policy is an error (the ID cannot be
resolved — surface a clear "run azd provision first" message).

### 4.7 Payload model

`internal/pkg/agents/agent_api/models.go`

This data-plane model is stable regardless of the source format. Add the field to
`HostedAgentDefinition` (models.go:185), or to `AgentDefinition` next to `RaiConfig` (models.go:74):

```go
EgressPolicyId string `json:"egress_policy_id_preview,omitempty"`
```

Verify the custom `MarshalJSON` (models.go:197) and `UnmarshalJSON` (models.go:223) round-trip the
field in **both** image (`container_protocol_versions`) and code (`protocol_versions`) deploy
modes.

### 4.8 Preview opt-in header

`internal/pkg/agents/agent_api/operations.go`

Per PR 43399 the field is `required_previews: EgressPolicy=V1Preview`; the service returns
`400 missing_required_preview_feature` if the field is present without the opt-in. Append
`EgressPolicy=V1Preview` to the `Foundry-Features` header on the create / version / code-deploy
requests (today these are hardcoded literals such as `HostedAgents=V1Preview` at operations.go:374
and `CodeAgents=V1Preview,HostedAgents=V1Preview` at operations.go:492).

**Decided:** extract the header value into a **single shared helper** (appending
`EgressPolicy=V1Preview` when an egress policy is present on the definition) rather than editing the
scattered string literals individually. This keeps the diff coherent and the opt-in set in one
place.

### 4.9 Error mapping

Map data-plane validation and authorization failures to structured extension errors
(`internal/exterrors`, codes in `internal/exterrors/codes.go`):

| Service response | Extension error |
|------------------|-----------------|
| `400 invalid_egress_policy_id_preview` | `exterrors.Validation` (fix the ID) |
| `404 egress_policy_not_found` | `exterrors.Dependency` (create / grant access to the resource) |
| `403` (no read access to the referenced resource) | `exterrors.Auth` / `exterrors.Dependency` |

Runtime `egress_policy_denied` is surfaced by the data plane at invoke time; `azd ai agent invoke`
should render the structured failure (`response.error.code`) cleanly without wrapping it as a
transport error.

## 5. Tier B design (provisioning the `agentGuardrails` resource)

> The provisioning template on this base branch is **module-delegated**:
> `internal/synthesis/templates/main.bicep` (`targetScope = 'subscription'`) calls
> `module resources 'modules/resources.bicep'`, which declares the Foundry account and its nested
> resources. Tier B changes target **`modules/resources.bicep`**, not `main.bicep` directly.

### 5.1 Parameter synthesis

`internal/synthesis/synthesizer.go`

`Synthesize()` (synthesizer.go:110) currently derives only `deployments` and `includeAcr` into
`Parameters` (synthesizer.go:154-156), decoding `azure.yaml` via the `foundryService` /
`agentBlock` structs (synthesizer.go:87-101). Extend `agentBlock` to read `egressPolicy` and emit
an `agentGuardrails` parameter block when a greenfield agent declares a policy with a `name` + body:

```go
Parameters: map[string]any{
    "deployments":     deployments,
    "includeAcr":      includeAcr,
    "agentGuardrails": agentGuardrails, // name + policyType + defaultAction + rules
},
```

Agents that reference a policy by `id` (not `name`) require **no** synthesis. Brownfield
(`endpoint:` set) continues to short-circuit with `ErrEndpointBrownfield`.

### 5.2 Bicep resource

`internal/synthesis/templates/modules/resources.bicep`

Add a nested child resource under `resource foundryAccount` (resources.bicep:76), as a sibling of
`modelDeployments` (resources.bicep:102) and `project` (resources.bicep:112):

```bicep
resource agentGuardrails 'agentGuardrails@2026-05-15-preview' = if (!empty(agentGuardrailsConfig)) {
  name: agentGuardrailsConfig.name
  properties: {
    policyType: agentGuardrailsConfig.policyType    // 'Egress'
    defaultAction: agentGuardrailsConfig.defaultAction // 'Allow' | 'Deny' (required)
    rules: agentGuardrailsConfig.?rules ?? []
  }
}
```

`mode` is intentionally omitted: azd does not surface it in `azure.yaml` for v1, so the resource is
created with the server default (`Enforced`).

Add the corresponding `param` and a user-defined type for the config block. Thread the parameter
through `main.bicep` into the `module resources` invocation.

### 5.3 Compiled ARM template

`internal/synthesis/templates/main.arm.json` must be regenerated from the updated Bicep (the
extension ships the embedded ARM JSON; the `deployments` / `includeAcr` params appear at
main.arm.json:96/103 and again in the nested module). Do not hand-edit.

### 5.4 Provider params and outputs

`internal/project/foundry_provisioning_provider.go`

- Surface the new `agentGuardrails` parameter through the provider's ARM parameter wiring.
- Add the synthesized resource ID as a provisioning output (alongside `AZURE_AI_PROJECT_ID` etc. at
  resources.bicep:157) so `azd deploy` can read it and feed Tier A's `egress_policy_id_preview`
  binding (see [4.6 Policy ID resolution](#46-policy-id-resolution)).

## 6. End-to-end flows

### Greenfield, declared policy (`azd up`)

1. Author declares `egressPolicy` with a `name` + body on the agent in `azure.yaml` (no ID).
2. `azd provision`: `Synthesize()` emits the `agentGuardrails` param; the template creates
   `.../agentGuardrails/<name>`; the resource ID is published as a provisioning output and written
   to the azd environment.
3. `azd deploy`: azd resolves the policy ID from that output (see
   [4.6 Policy ID resolution](#46-policy-id-resolution)) and maps it into the hosted-agent version
   definition as `egress_policy_id_preview`; the request carries
   `Foundry-Features: ...,EgressPolicy=V1Preview`.
4. New sessions route to the version bound to the policy; existing sessions stay pinned.

### Reference an existing policy

1. Author declares `egressPolicy.id` pointing at an existing `agentGuardrails` resource.
2. No synthesis; `azd deploy` binds the version to the supplied ID.

### Brownfield (`endpoint:` set)

Pass-through only: the supplied `egressPolicy.id` is bound at deploy time; azd does not provision
the resource.

## 7. Test plan

- **Unit (Tier A):** table-driven tests for the azure.yaml agent mapping — assert
  `egressPolicy.id` and the Tier-B-synthesized ID both land in the payload as
  `egress_policy_id_preview` in image and code deploy modes; assert validation rejects an empty /
  malformed ID and the both-`id`-and-`name` case. Assert the `Foundry-Features` header includes
  `EgressPolicy=V1Preview` (extend `operations_test.go`). Add payload round-trip tests in
  `models_test.go`.
- **Schema:** add an `azure.yaml` example exercising both `egressPolicy` forms under
  `schemas/examples/` and validate it against `Agent.json` (mirror `complex.azure.yaml`).
- **Unit (Tier B):** extend `synthesizer_test.go` for the new `agentGuardrails` parameter (declared
  vs. reference-by-id vs. brownfield); add / update an ARM-template snapshot for the regenerated
  output.
- **Pre-commit:** `gofmt -s -w .`, `go fix ./...`, `golangci-lint run ./...`,
  `cspell lint`, copyright check (per extension `AGENTS.md`).

## 8. Risks

1. **Upstream not final.** Both REST PRs are OPEN. PR 43974 renamed the resource
   `policies` → `agentGuardrails` and still has unresolved contract gaps (optional rule `action`,
   `RewriteTarget` / `RuleMatch` "required but all-optional", `Transform`/`Rewrite` vs `Fqdn` rule
   type). PR 43399 has unrelated changes riding along. azd should wait for the field name, opt-in
   key, resource type, and API version to stabilize.
2. **Naming mismatch across artifacts.** The binding is `egress_policy_id_preview`, the policy
   `policyType` is `Egress`, but the ARM resource family is `agentGuardrails`. Sample docs and error
   messages must reference `agentGuardrails`, not `policies`.
3. **Module template drift.** This base branch uses `modules/resources.bicep`; other branches
   (e.g. an inline `main.bicep`) differ. Tier B must target whichever layout is current at
   implementation time and regenerate `main.arm.json` accordingly.

## 9. Decisions

Resolved design choices for v1:

1. **azure.yaml field name:** `egressPolicy` (camelCase), on the hosted-agent block. Not nested
   under a generic `policies` map.
2. **Two forms, both supported:** `egressPolicy.name` + rule body (azd provisions), and
   `egressPolicy.id` (reference an existing resource).
3. **Identifier form:** `egressPolicy.id` is a **full ARM resource ID** only. No short / project-local
   name in v1.
4. **Provisioning behavior:** azd **creates** the `agentGuardrails` resource when `name` is specified
   (Tier B), and **skips** provisioning when `id` is specified (reference only).
5. **`Foundry-Features` header:** add the `EgressPolicy=V1Preview` opt-in via a **single shared
   helper**, not by editing the scattered string literals individually.
6. **Audit mode:** **not** surfaced in `azure.yaml` for v1. The `agentGuardrails` resource is created
   with the server default mode (`Enforced`); azd does not expose a `mode` field.

The remaining genuinely-open item is upstream, not an azd design choice: the two REST PRs (43399,
43974) must finalize their field name, opt-in key, resource type, and API version before
implementation lands (tracked in [Risks](#8-risks)).
