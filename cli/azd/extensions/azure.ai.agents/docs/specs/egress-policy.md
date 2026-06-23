# Spec: azd Support for Hosted-Agent Egress Policy

- **Owner:** zhihuan
- **Extension:** `azure.ai.agents`
- **Base branch:** `huimiu/foundry-azure-yaml`

## Upstream dependencies

| Surface | PR / Spec | What it adds |
|---------|-----------|--------------|
| Control-plane (ARM) | [`Azure/azure-rest-api-specs#43974`](https://github.com/Azure/azure-rest-api-specs/pull/43974) | A nested `egressPolicy` property (`RaiEgressPolicyConfig`) on the **existing** RAI Policy resource `Microsoft.CognitiveServices/accounts/{account}/raiPolicies/{name}` (`2026-05-15-preview`). |
| Data-plane (Foundry) | [`Azure/azure-rest-api-specs#43399`](https://github.com/Azure/azure-rest-api-specs/pull/43399) | The hosted-agent RAI-policy binding for egress. |
| Experience spec | [`coreai-microsoft/foundrysdk_specs#176`](https://github.com/coreai-microsoft/foundrysdk_specs/pull/176) | Product context: hosted-agent egress policy journey and the binding contract. |

> These contracts are not yet final; see [Risks](#8-risks).

## 1. Summary

`azure.yaml` is the source of truth for hosted agents: an agent is declared inline under
`services.<name>.agents[]` with `host: microsoft.foundry`. This feature lets a developer declare an
egress policy for such an agent in `azure.yaml`. Egress is expressed as a nested `egress` body on the
RAI policy the agent references (`Microsoft.CognitiveServices/accounts/{account}/raiPolicies/{name}`),
the same resource that already carries the content-filter RAI policy. Two coordinated pieces of azd
behavior:

- **Tier A (data-plane binding):** carry the RAI-policy reference from the `azure.yaml` agent block
  into the hosted-agent version definition through the **existing** `policies[]` → `rai_config`
  path during `azd deploy`. Egress travels with the RAI policy. Omitting it preserves current
  behavior.
- **Tier B (provisioning):** during `azd provision`, create (or extend) the referenced RAI Policy
  resource so its `properties.egressPolicy` carries the egress rules for greenfield projects, and
  feed its resource ID back so Tier A can bind the version to it via `rai_policy_name`.

The hosted-agent definition carries only a **reference** to the RAI Policy resource; it does not
embed the egress rule body.

## 2. Scope

### In scope — Tier A (data-plane binding)

- An `egress` body on the existing `rai_policy` entry of the `azure.yaml` hosted-agent block
  (`services.<name>.agents[]`), plus the corresponding schema and Go struct extensions.
- Binding the referenced RAI policy into the hosted-agent version definition through the
  **existing** `policies[]` → `mapRaiConfig` → `rai_config.rai_policy_name` path for both **image**
  and **code** deploy modes.
- Extending the agent-manifest policy validation to accept the egress body on a `rai_policy` entry.
- Structured error mapping for RAI-policy reference validation and authorization failures.

### In scope — Tier B (provisioning)

- Synthesizing a `raiPolicies` resource carrying `properties.egressPolicy` into the provisioning
  template from a greenfield `azure.yaml` declaration.
- Regenerating the compiled ARM template.
- Surfacing the RAI-policy resource ID as a provisioning output so `azd deploy` can bind it.

### Out of scope

- A rich authoring UX for the full rule body (FQDN rules, `Transform` / `Rewrite` actions, secret
  or managed-identity header injection) beyond pass-through of what is declared. Rule bodies are
  authored in Bicep or out-of-band control-plane tooling.
- Runtime `egress_policy_denied` handling beyond rendering the structured failure cleanly at
  `azd ai agent invoke` time. Enforcement and denial are owned by the data plane.
- Policy hot-reload into running sessions.
- MCP Tool Policies, PII detection, traffic analysis, audit-log exploration, or SIEM hooks.
- Brownfield synthesis of the RAI policy resource (when `endpoint:` is set, the RAI-policy ID is
  pass-through only).

## 3. azure.yaml shape

Egress reuses the **existing** `policies[]` mechanism on the hosted-agent block. Today a hosted
agent already references a RAI policy via a `policies` entry of `type: rai_policy` carrying
`rai_policy_name` (the full ARM resource ID of the RAI policy — see `yaml.go:194-209`). Egress is
authored as a nested `egress` body on that same `rai_policy` entry, because the egress rules live on
the RAI Policy resource itself. Two forms, chosen by whether azd owns the RAI Policy resource:

**Form A — declare egress azd provisions (greenfield).** The developer does **not** supply a
resource ID. They give the RAI policy a `name` and an `egress` body; `azd provision` creates (or
extends) `.../raiPolicies/<name>` with `properties.egressPolicy`, and azd resolves the resulting
resource ID and binds it as `rai_policy_name` at `azd deploy` time (see
[4.6 Policy ID resolution](#46-policy-id-resolution)). This is the primary form and avoids the
chicken-and-egg of needing an ID before the resource exists.

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
        policies:
          - type: rai_policy
            name: support-agent-rai        # azd provisions raiPolicies/<name>
            egress:
              defaultAction: Deny          # Allow | Deny (required by the RAI policy)
              rules:
                - name: allow-openai
                  ruleType: Fqdn
                  match: { host: "*.openai.com" }
                  action: { actionType: Allow }
```

**Form B — reference an existing RAI policy by resource ID.** Use this when the `raiPolicies`
resource **already exists** (created out-of-band, by an earlier `azd up`, or in a brownfield
project). The developer supplies its full ARM resource ID via `rai_policy_name`; azd does not
provision it. Any egress rules are managed on that existing RAI policy out-of-band.

> `rai_policy_name` is the existing field on the `rai_policy` entry (`yaml.go:209`); despite its
> name it already carries the **full ARM resource ID** of the RAI policy, not a short name. This
> spec reuses it unchanged.

```yaml
        policies:
          - type: rai_policy
            rai_policy_name: /subscriptions/<sub>/resourceGroups/<rg>/providers/Microsoft.CognitiveServices/accounts/<account>/raiPolicies/<name>
```

- `name` + `egress` body (**Form A**) is the greenfield declaration azd synthesizes (Tier B). The
  developer never writes a resource ID; azd resolves it after provisioning and binds it as
  `rai_policy_name`.
- `rai_policy_name` (**Form B**) is the **full ARM resource ID** of an *already-existing*
  `raiPolicies` resource (full ARM ID only — see [Decisions](#9-decisions)). Do not use it for a
  resource that does not exist yet — the ID is not knowable until the resource is provisioned.
- Exactly one of `name` (declare) or `rai_policy_name` (reference) is expected on the entry. The
  `egress` body is the new field on the existing `Policy` shape, which has no egress field today, so
  it must be added explicitly.

## 4. Tier A design (data-plane binding)

All paths are under `cli/azd/extensions/azure.ai.agents/`.

### 4.1 azure.yaml deploy translation

Tier A binds the RAI policy (which now also carries egress) into the **hosted-agent version
definition** at deploy time, built from the `azure.yaml` agent block. The deploy path decodes
`services.<name>.agents[]` into the hosted-agent struct (`agent_yaml.ContainerAgent`), whose
`Policies []Policy` field (`yaml.go:207`) already feeds the data-plane `rai_config`. The egress body
attaches to that existing `rai_policy` entry and flows through the same translation as every other
agent field — no new top-level field is introduced.

### 4.2 Schema

`schemas/Agent.json`

The `HostedAgent` definition (`Agent.json:52-138`) already references the `policies` array. Add an
`egress` property to the `rai_policy` policy entry. Shape:

```json
"egress": {
  "type": "object",
  "description": "Egress rules carried on the referenced RAI policy (raiPolicies.properties.egressPolicy).",
  "additionalProperties": false,
  "properties": {
    "defaultAction": { "type": "string", "enum": ["Allow", "Deny"] },
    "rules":         { "type": "array", "items": { "type": "object" } }
  },
  "required": ["defaultAction"]
}
```

`defaultAction` is required whenever an `egress` body is present (the RAI Policy resource requires
it). The `microsoft.foundry.json` service schema needs no change (it references `Agent.json`).

### 4.3 azure.yaml agent struct

The existing `Policy` struct (`yaml.go:207-210`) gains an optional `Egress` field; no new top-level
struct on the agent block:

```go
type Policy struct {
    Type          PolicyType `json:"type" yaml:"type"`
    Name          string     `json:"name,omitempty" yaml:"name,omitempty"`                     // Form A: azd provisions raiPolicies/<name>
    RaiPolicyName string     `json:"raiPolicyName,omitempty" yaml:"rai_policy_name,omitempty"` // Form B: existing ARM ID
    Egress        *Egress    `json:"egress,omitempty" yaml:"egress,omitempty"`
}

type Egress struct {
    DefaultAction string        `yaml:"defaultAction,omitempty"`
    Rules         []EgressRule  `yaml:"rules,omitempty"`
}
```

### 4.4 Validation

The agent-manifest validation switch (`parse.go:389-401`) currently accepts only `rai_policy`
(requiring `rai_policy_name`) and **rejects** every other type. Extend the `rai_policy` case to
accept the new declare-vs-reference shapes and the egress body:

- `type: rai_policy` with `rai_policy_name` (Form B): `rai_policy_name` must be non-empty and
  well-formed (`.../providers/Microsoft.CognitiveServices/accounts/<account>/raiPolicies/<name>`).
- `type: rai_policy` with `name` + `egress` (Form A): `name` is required, and when `egress` is
  present `egress.defaultAction` is required (Tier B will synthesize the resource).
- Reject providing both `rai_policy_name` and a `name` + `egress` body on the same entry.

### 4.5 Mapping

In the azure.yaml deploy mapping, resolve the bound RAI-policy ID (see
[4.6 Policy ID resolution](#46-policy-id-resolution)) into the existing `rai_config` binding. This
is the **already-wired** path: `mapRaiConfig` (`map.go:87-97`) flattens `policies[]` into
`agent_api.RaiConfig{RaiPolicyName: ...}`, set on the definition at `map.go:412` (code deploy) and
`map.go:437` (image deploy). For Form A, substitute the provisioned resource ID for the declared
`name` before `mapRaiConfig` runs (or have `mapRaiConfig` prefer the resolved ID).

### 4.6 Policy ID resolution

This is how a RAI-policy resource ID gets bound onto an agent, and it differs by form:

**Form B (`rai_policy_name`):** the developer already supplied the full ARM resource ID. Use it
directly as the `rai_config.rai_policy_name` binding. No resolution needed.

**Form A (`name` + `egress` body):** the developer supplied only a name, because the resource ID is
**not knowable until `azd provision` creates the resource**. azd resolves the ID **after
provisioning**, at `azd deploy` time, by one of:

1. **Provisioning output (preferred).** Tier B's `resources.bicep` emits the created RAI-policy ID
   as an output (e.g. `AZURE_AI_RAI_POLICY_ID`). `azd provision` writes provisioning outputs into
   the azd environment; `azd deploy` reads that value and uses it as the binding. This is the normal
   azd provision→deploy data flow and keeps the ID authoritative (the actual ARM ID of the created
   resource).
2. **Deterministic construction (fallback).** The ARM ID is fully determined by
   `subscription / resourceGroup / account / raiPolicies/<name>`. Since azd knows the subscription,
   resource group, and Foundry account after provisioning, it can compose the ID from the declared
   `name` without reading an output. Use this only if a provisioning output is unavailable; it is
   more brittle (must track the exact account name and resource-type casing).

In both Form A mechanisms the developer **never writes the ID into `azure.yaml`** — the name is the
stable handle, and the ID is derived. If multiple agents reference the same RAI policy `name`, they
all resolve to the same ID. The order is enforced by azd's normal lifecycle: `provision` (creates
the resource + publishes the ID) runs before `deploy` (reads the ID + binds it). On `azd up` both
run in sequence; running `azd deploy` against an un-provisioned Form A policy is an error (the ID
cannot be resolved — surface a clear "run azd provision first" message).

### 4.7 Payload model

`internal/pkg/agents/agent_api/models.go`

Egress is carried by the existing `rai_config` (`RaiConfig` at `models.go:66-68`,
`AgentDefinition.RaiConfig` at `models.go:74`), which serializes as `rai_config.rai_policy_name`.
The same field that already carries the content-filter RAI policy now also carries egress, since
egress lives on that RAI Policy resource. Verify the existing custom `MarshalJSON` (`models.go:197`)
and `UnmarshalJSON` (`models.go:223`) round-trip `rai_config` in **both** image
(`container_protocol_versions`) and code (`protocol_versions`) deploy modes.

### 4.8 Preview opt-in header

`internal/pkg/agents/agent_api/operations.go`

The create / version / code-deploy requests continue to send the existing `Foundry-Features`
literals — `HostedAgents=V1Preview` (`operations.go:374`) and
`CodeAgents=V1Preview,HostedAgents=V1Preview` (`operations.go:492`). The `rai_config` binding is
accepted under these existing opt-ins, the same way the content-filter RAI policy is today.

### 4.9 Error mapping

Map RAI-policy reference validation and authorization failures to structured extension errors
(`internal/exterrors`, codes in `internal/exterrors/codes.go`):

| Service response | Extension error |
|------------------|-----------------|
| `400` invalid / malformed RAI-policy reference | `exterrors.Validation` (fix the `rai_policy_name`) |
| `404` referenced RAI policy not found | `exterrors.Dependency` (create / grant access to the resource) |
| `403` (no read access to the referenced RAI policy) | `exterrors.Auth` / `exterrors.Dependency` |

Runtime `egress_policy_denied` is surfaced by the data plane at invoke time; `azd ai agent invoke`
should render the structured failure (`response.error.code`) cleanly without wrapping it as a
transport error.

## 5. Tier B design (provisioning egress on the RAI Policy resource)

> The provisioning template on this base branch is **module-delegated**:
> `internal/synthesis/templates/main.bicep` (`targetScope = 'subscription'`) calls
> `module resources 'modules/resources.bicep'`, which declares the Foundry account and its nested
> resources. Tier B changes target **`modules/resources.bicep`**, not `main.bicep` directly.

### 5.1 Parameter synthesis

`internal/synthesis/synthesizer.go`

`Synthesize()` (synthesizer.go:110) currently derives only `deployments` and `includeAcr` into
`Parameters` (synthesizer.go:154-156), decoding `azure.yaml` via the `foundryService` /
`agentBlock` structs (synthesizer.go:87-101). Extend `agentBlock` to read the `rai_policy` entry's
`name` + `egress` body and emit a `raiPolicy` parameter block when a greenfield agent declares one:

```go
Parameters: map[string]any{
    "deployments": deployments,
    "includeAcr":  includeAcr,
    "raiPolicy":   raiPolicy, // name + egressPolicy { defaultAction + rules }
},
```

Agents that reference an existing RAI policy by `rai_policy_name` (not `name`) require **no**
synthesis. Brownfield (`endpoint:` set) continues to short-circuit with `ErrEndpointBrownfield`.

### 5.2 Bicep resource

`internal/synthesis/templates/modules/resources.bicep`

Add a nested `raiPolicies` child resource under `resource foundryAccount` (resources.bicep:76), as a
sibling of `modelDeployments` (resources.bicep:102) and `project` (resources.bicep:112), carrying
egress under `properties.egressPolicy`:

```bicep
resource raiPolicy 'raiPolicies@2026-05-15-preview' = if (!empty(raiPolicyConfig)) {
  name: raiPolicyConfig.name
  properties: {
    // egress is a nested property on the RAI policy (RaiEgressPolicyConfig)
    egressPolicy: {
      defaultAction: raiPolicyConfig.egress.defaultAction // 'Allow' | 'Deny' (required)
      rules: raiPolicyConfig.egress.?rules ?? []
    }
  }
}
```

`mode` is intentionally omitted: azd does not surface it in `azure.yaml` for v1, so the egress policy
is created with the server default (`Enforced`). The exact `raiPolicies` API version and the
`egressPolicy` property shape must match #43974 once frozen; pin them at implementation time.

Add the corresponding `param` and a user-defined type for the config block. Thread the parameter
through `main.bicep` into the `module resources` invocation.

### 5.3 Compiled ARM template

`internal/synthesis/templates/main.arm.json` must be regenerated from the updated Bicep (the
extension ships the embedded ARM JSON; the `deployments` / `includeAcr` params appear at
main.arm.json:96/103 and again in the nested module). Do not hand-edit.

### 5.4 Provider params and outputs

`internal/project/foundry_provisioning_provider.go`

- Surface the new `raiPolicy` parameter through the provider's ARM parameter wiring.
- Add the RAI-policy resource ID as a provisioning output (alongside `AZURE_AI_PROJECT_ID` etc. at
  resources.bicep:157) so `azd deploy` can read it and feed Tier A's `rai_config.rai_policy_name`
  binding (see [4.6 Policy ID resolution](#46-policy-id-resolution)).

## 6. End-to-end flows

### Greenfield, declared egress (`azd up`)

1. Author declares a `rai_policy` entry with a `name` + `egress` body on the agent in `azure.yaml`
   (no ID).
2. `azd provision`: `Synthesize()` emits the `raiPolicy` param; the template creates
   `.../raiPolicies/<name>` with `properties.egressPolicy`; the resource ID is published as a
   provisioning output and written to the azd environment.
3. `azd deploy`: azd resolves the RAI-policy ID from that output (see
   [4.6 Policy ID resolution](#46-policy-id-resolution)) and binds it into the hosted-agent version
   definition via the existing `rai_config.rai_policy_name`.
4. New sessions route to the version bound to the policy; existing sessions stay pinned.

### Reference an existing RAI policy

1. Author declares a `rai_policy` entry with `rai_policy_name` pointing at an existing `raiPolicies`
   resource (egress managed on it out-of-band).
2. No synthesis; `azd deploy` binds the version to the supplied ID via `rai_config`.

### Brownfield (`endpoint:` set)

Pass-through only: the supplied `rai_policy_name` is bound at deploy time; azd does not provision the
RAI policy.

## 7. Test plan

- **Unit (Tier A):** table-driven tests for the azure.yaml agent mapping — assert that a Form B
  `rai_policy_name` and a Form A Tier-B-resolved ID both land in the payload as
  `rai_config.rai_policy_name` in image and code deploy modes (extend `map_test.go`); assert the
  validation switch accepts the egress body on a `rai_policy` entry and rejects an empty / malformed
  reference and the both-`rai_policy_name`-and-`name`+`egress` case (extend `parse_test.go`). Add
  `rai_config` round-trip coverage in `models_test.go`.
- **Schema:** add an `azure.yaml` example exercising both egress forms under `schemas/examples/` and
  validate it against `Agent.json` (mirror `complex.azure.yaml`); update
  `testdata/hosted-agent-with-rai.yaml` to cover the nested `egress` body.
- **Unit (Tier B):** extend `synthesizer_test.go` for the new `raiPolicy` parameter (declared egress
  vs. reference-by-id vs. brownfield); add / update an ARM-template snapshot for the regenerated
  output.
- **Pre-commit:** `gofmt -s -w .`, `go fix ./...`, `golangci-lint run ./...`,
  `cspell lint`, copyright check (per extension `AGENTS.md`).

## 8. Risks

1. **Confirm the RAI-policy binding is sufficient on the data plane.** This spec binds egress via the
   `rai_config.rai_policy_name` reference. #43399 also defines an `egress_policy_id_preview` field on
   `HostedAgentDefinition` (gated by `EgressPolicy=V1Preview`). Confirm with the service team that
   the `rai_config` binding alone is accepted; if the service requires the dedicated field, Tier A
   (§4.5/§4.7/§4.8) would also set that field plus the opt-in.
2. **Upstream contract not yet final.** The `RaiEgress*` shape, the `raiPolicies` API version, and
   the `egressPolicy` property are still in flux. Pin the exact field names, API version, and rule
   shape at implementation time once they stabilize, and regenerate the compiled ARM template.
3. **Naming across artifacts.** The agent binds via the **RAI policy** (`rai_config.rai_policy_name`,
   resource `raiPolicies/<name>`); egress is `properties.egressPolicy` on that resource. Sample docs
   and error messages must reference the **RAI policy**.
4. **Module template drift.** This base branch uses `modules/resources.bicep`; other branches
   (e.g. an inline `main.bicep`) differ. Tier B must target whichever layout is current at
   implementation time and regenerate `main.arm.json` accordingly.

## 9. Decisions

Resolved design choices for v1:

1. **Binding via the existing RAI path:** egress is expressed on the agent's `rai_policy` entry and
   bound through the existing `policies[]` → `mapRaiConfig` → `rai_config.rai_policy_name` flow,
   reusing the existing content-filter binding and the existing `HostedAgents=V1Preview` /
   `CodeAgents=V1Preview` opt-ins.
2. **azure.yaml shape:** an `egress` body on the existing `rai_policy` policy entry.
3. **Two forms, both supported:** `name` + `egress` body (azd provisions the RAI policy), and
   `rai_policy_name` (reference an existing RAI policy).
4. **Identifier form:** `rai_policy_name` is a **full ARM resource ID** only. No short /
   project-local name in v1.
5. **Provisioning behavior:** azd **creates / extends** the `raiPolicies` resource (with
   `properties.egressPolicy`) when `name` is specified (Tier B), and **skips** provisioning when
   `rai_policy_name` is specified (reference only).
6. **Audit mode:** **not** surfaced in `azure.yaml` for v1. The egress policy is created with the
   server default mode (`Enforced`); azd does not expose a `mode` field.
