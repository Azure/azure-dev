<!-- cspell:ignore privatelink azurecr vnet subnet subnets myprivacr cognitiveservices UAMI hund -->

# Private networking for `host: microsoft.foundry`

## Problem

Foundry network isolation is unreachable from `azure.yaml` today. Per the [unified `azure.yaml` design](../unify-azure-yaml/spec.md) (§1.4), VNet binding is an **Account** setting while the unified shape models the **Project**, so customizing it requires ejecting to Bicep. A developer who needs a network-secured agent must hand-author the service team's standard network-secured template and maintain it by hand. There is no `network:` surface, and the in-memory synthesizer that [bicep-less provisioning](../bicepless-foundry/spec.md) introduced always emits a public account (`publicNetworkAccess: 'Enabled'`).

This spec adds a declarative `network:` block to the `host: microsoft.foundry` service and teaches the existing synthesizer to provision a network-bound account from it.

### Why the block sits on the service

VNet binds at the Account level, yet `network:` sits on a service entry that reads as a project. This is intentional and unambiguous in the greenfield flow: the synthesizer provisions **one Account plus one Project per Foundry service** (1:1), so "network on the service" is exactly "network on that service's account." azd does not model multiple projects sharing a single network-bound account — multiple Foundry services produce multiple accounts, each with its own `network:`. Brownfield (`endpoint:`) ignores `network:` because the account is already bound by whoever created it (see §7), so the service-level declaration never reconciles against an account azd did not create. If Foundry later needs N projects under one network-bound account, the block would promote to an account-scoped surface (see §9, open questions).

## Solution

Extend the Foundry provisioning synthesizer (`internal/synthesis`, introduced by the bicep-less work) to read a new `network:` block on the service body and emit the network primitives a secured agent account requires: a VNet with an agent subnet and a private-endpoint subnet, account-level `networkInjections`, an account private endpoint, and the AI private DNS zones — each created or referenced per the developer's declaration.

The block is purely additive. When `network:` is absent the synthesizer behaves exactly as today (public account). When present, the account flips to private and the network modules are included. No new provider, no new gRPC surface, no core change beyond the schema slice — the block rides the same `AdditionalProperties` channel every other Foundry key already uses.

Dependent stores (Cosmos DB, AI Search, Storage) stay **platform-managed**, which is supported under VNet isolation. This removes the bulk of the sample template (BYO stores, their private endpoints, role assignments, and both capability hosts) from azd's responsibility.

The example below illustrates one scenario (BYO VNet with explicit subnets); it is not the full schema. See §4 for the field reference and the create-vs-reference rules.

```yaml
infra:
  provider: microsoft.foundry

services:
  my-project:
    host: microsoft.foundry

    # New: private networking for the Foundry account.
    # Omitted => public account (unchanged behavior).
    network:
      mode: byo                       # byo | managed
      byo:
        vnet:
          id: ${AZURE_VNET_ID}        # required for byo (v1)
        agentSubnet:
          name: agent-subnet
          prefix: 192.168.0.0/24
        peSubnet:
          name: pe-subnet
          prefix: 192.168.1.0/24
      dns:
        resourceGroup: rg-private-dns
        subscription: ${AZURE_DNS_SUBSCRIPTION_ID}

    deployments:
      - name: gpt-4.1-mini
        model: { format: OpenAI, name: gpt-4.1-mini, version: "2025-04-14" }
        sku:   { name: GlobalStandard, capacity: 10 }
    agents:
      - name: my-agent
        kind: hosted
        project: src/my-agent
        image: myprivacr.azurecr.io/agents/my-agent:v1   # BYO image (no ACR)
```

## Scope

**In scope**

- **BYO VNet** — bring an existing VNet; create or reference its subnets.
- **Foundry-managed VNet** — with a selectable isolation mode.
- **A network-bound (private) Foundry account** — its private endpoint and the AI private DNS zones, created or referenced.
- **Platform-managed dependent stores** under VNet isolation.
- **BYO container image** as the registry story for secured agents.

**Out of scope**

- BYO dependent stores and the capability-host wiring they require — managed stores are used instead.
- Tool subnet, user-assigned managed identity (UAMI), customer-managed keys (CMK) — not core to the secured-agent scenario.
- Local build into a private ACR — secured agents bring a pre-built image; the developer owns ACR networking. See §8.
- The bicep-less synthesizer/provider itself and the unified `azure.yaml` shape — prerequisites tracked by their own specs (see §3).

## §3 Relationship to in-flight work

Private networking is the last layer on a stack already in flight on the `huimiu/foundry-azure-yaml` feature branch. It does not start from `main`; it lands as a follow-on into that branch after the synthesizer merges.

```
unified azure.yaml (huimiu)            #8590 docs → branch huimiu/foundry-azure-yaml
├─ bicep-less provisioning (hund030)   #8577 docs / #8643 impl  ← integration point
├─ foundry service target + init       #8629 / #8675
└─ BYO image                           #8689  (+ #8645 remote-build skip, merged)
```

Two consequences matter here:

- **No core change.** Unknown service keys ride `ServiceConfig.AdditionalProperties` to the extension (unify §2.1), and the synthesizer reads the raw `azure.yaml` bytes directly, so the only required core edit is the JSON schema slice (§4).
- **ACR is off the critical path.** `--image` (#8689) and the merged #8645 (skip remote build for VNET-injected accounts) keep registry work out of v1 (see §8).

## §4 `azure.yaml` surface

`network:` is a sibling of `deployments:` / `agents:` on the service body.

```yaml
network:
  mode: byo | managed              # required when network is present

  byo:                             # used only when mode: byo
    vnet:
      id: <existing VNet ARM id>   # required for byo in v1
    agentSubnet:                   # optional; see tri-state below
      name: <subnet name>
      prefix: <CIDR>
    peSubnet:                      # optional; same tri-state
      name: <subnet name>
      prefix: <CIDR>

  managed:                         # used only when mode: managed
    isolationMode: AllowInternetOutbound | AllowOnlyApprovedOutbound

  dns:                             # optional
    resourceGroup: <rg name>       # when set, zones are referenced, not created
    subscription: <sub id>
```

### Field semantics

| Field | Rule |
| --- | --- |
| `mode` | Required when `network:` is present. `byo` and `managed` are mutually exclusive; the matching sub-block is required and the other must be absent. |
| `byo.vnet.id` | Required in v1. The VNet must already exist. `${VAR}` is resolved from the azd environment before synthesis. |
| `byo.agentSubnet` / `byo.peSubnet` | **Tri-state.** Omitted → azd creates a default subnet (`agent-subnet` / `pe-subnet`) with a default prefix. Name only → azd **references** an existing subnet and validates it (delegation for the agent subnet, PE network policies for the PE subnet). Name **and** prefix → azd **creates** the subnet with that name and prefix. |
| `managed.isolationMode` | One of `AllowInternetOutbound`, `AllowOnlyApprovedOutbound`. Maps to the account managed-network isolation setting. |
| `dns.resourceGroup` | When omitted (or the whole `dns:` block omitted), azd **creates** the required private DNS zones and links them. When set, azd **references** existing zones in that resource group. |
| `dns.subscription` | Optional. Defaults to the deployment subscription. Accepts a bare GUID or `${VAR}`. Only meaningful alongside `dns.resourceGroup`. |

`${VAR}` resolves client-side from the azd environment for `byo.vnet.id` and `dns.subscription`. `${{...}}` Foundry expressions are not expected in network fields and are passed through verbatim if present, consistent with the shared expander.

## §5 Synthesizer and template changes (high level)

The bicep-less work landed `internal/synthesis/synthesizer.go` plus an embedded `templates/` tree (`main.bicep`, `modules/acr.bicep`, `abbreviations.json`, and a precompiled `main.arm.json` fallback). The account today is hardcoded to public. The changes are additive and local to this package.

- **Read `network:`** — extend the `foundryService` view the synthesizer decodes so it captures the `network` sub-tree (mode, byo, managed, dns), and resolve `${VAR}` in `byo.vnet.id` / `dns.subscription` before emitting parameters. Absent block → all network parameters default off and output is byte-identical to today.
- **Emit parameters** — add the network parameter set to the synthesizer `Result.Parameters` (mode, vnet id, subnet tri-state descriptors, isolation mode, DNS zone map + subscription).
- **`main.bicep`** — guard the network path on a single `enableNetworkIsolation` condition. When on: include `modules/network.bicep`, set the account's `publicNetworkAccess: 'Disabled'`, `networkAcls.defaultAction: 'Deny'`, and `networkInjections` (scenario `agent`) from the agent subnet, and include `modules/private-endpoint-dns.bicep`. When off: the account block is exactly today's.
- **New modules** — `modules/network.bicep` (VNet + two subnets, create or reference, agent-subnet delegation) and `modules/private-endpoint-dns.bicep` (account private endpoint + the three AI DNS zones `privatelink.services.ai.azure.com`, `privatelink.openai.azure.com`, `privatelink.cognitiveservices.azure.com` + VNet links, create or reference).
- **Regenerate `main.arm.json`** — the precompiled ARM fallback must be rebuilt from the extended `main.bicep` and committed alongside it. The byte-stability contract from the bicep-less spec applies: same `azure.yaml` → byte-identical output within a patch version.

The provisioning provider, on-disk/eject behavior, and parameter wiring need no structural change — they consume whatever `Result.Parameters` carries.

## §6 Validation pipeline additions

Network validation slots into the synthesizer's existing pre-synthesis checks and runs on every `provision`, `preview`, and eject:

- **Mode coherence** — `mode` is `byo` or `managed`; the matching sub-block is present and the other is absent.
- **BYO VNet** — `byo.vnet.id` is present (v1) and is a well-formed `Microsoft.Network/virtualNetworks` ARM id after `${VAR}` resolution.
- **Subnet tri-state** — for each of `agentSubnet` / `peSubnet`: reject `prefix` without `name`; validate CIDR shape when `prefix` is present; surface a clear field path on failure.
- **DNS** — when `dns.resourceGroup` is set, validate the resource-group name; normalize `dns.subscription` to a bare GUID (accept `/subscriptions/<guid>` or a bare GUID), matching the sample's behavior.
- **Env resolvability** — every `${VAR}` in a network field resolves from the azd environment; an unresolved reference fails with the variable name.

Failures surface with the service-scoped field path, e.g. `services.my-project.network.byo.agentSubnet: prefix set without name`.

## §7 Brownfield interaction

`endpoint:` on the service already short-circuits synthesis — the synthesizer returns `ErrEndpointBrownfield` and the provider connects to the existing project without provisioning. A network-secured account reached this way is **already** network-bound by whoever created it.

Therefore, when `endpoint:` is present, `network:` is **ignored** (the account's network posture is fixed and not azd's to change). This is documented as explicit precedence: `endpoint:` wins, and a project that wants azd to manage its network posture must be greenfield (no `endpoint:`). If both are present, azd warns that `network:` has no effect in brownfield mode.

## §8 ACR private networking — decision and RoI

A secured agent still needs its image to come from somewhere reachable inside the VNet. Two paths:

| Path | What azd must do | Effort | v1 |
| --- | --- | --- | --- |
| **BYO image (`--image`)** | Nothing. The developer brings `registry/image:tag` and owns the registry's SKU, private endpoint, DNS, and firewall. | ~0 (lands with #8689) | **Chosen** |
| **Local build → private ACR** | Provision a Premium ACR with a private endpoint and `privatelink.azurecr.io`, then solve build connectivity: a remote ACR-task build must egress to a private registry, or a local push must reach it from outside the VNet (developer IP allowlist). | High; many connectivity edge cases | **Deferred** |

The hard part of the local-build path is not creating the registry — it is making the build actually reach a network-isolated registry, which drags in build-agent network placement azd does not control. v1 therefore standardizes on BYO image for secured agents; #8645 (merged) already skips remote build for network-injected accounts, so the flow is coherent end to end. Revisit local-build-into-private-ACR after telemetry shows `--image` adoption and real demand.

## §9 Telemetry, docs, and open questions

**Telemetry**

- `provision.network_mode` — `none` | `byo` | `managed`, emitted at provision start. Lets us measure secured-agent adoption and BYO-vs-managed split. Implementation PRs add it to `docs/reference/telemetry-data.md`.

**Docs**

- New env vars consumed for network fields (`AZURE_VNET_ID`, `AZURE_DNS_SUBSCRIPTION_ID`, or whatever the synthesizer reads) are documented in `cli/azd/docs/environment-variables.md` with format and default.
- Extension README documents the `network:` block and the BYO-image requirement for secured agents.

**Open questions**

1. **Agent-subnet delegation target.** Confirm the exact delegation the agent subnet requires for `networkInjections` scenario `agent`, and whether the reference path must validate it or may assume the platform team set it.
2. **`managed` reference template.** Template 15 is BYO-only. The Foundry-managed VNet (`mode: managed`, `isolationMode`) needs a service-team reference for the account-level managed-network ARM shape before that branch can be authored.
3. **DNS collision on create.** When azd creates the AI DNS zones but a zone of the same name already exists in the target RG (created out of band), define whether azd references it, fails, or warns.
4. **Subnet reference validation depth.** For name-only subnets, decide how much azd validates at synthesis time (existence, delegation, PE policies) versus deferring to ARM, given synthesis runs before any ARM call.
5. **Account-scoped surface.** If Foundry adds multiple projects under one network-bound account, decide whether `network:` promotes from the service entry to an account-scoped surface (see "Why the block sits on the service").

## References

- Service team's standard network-secured agent template (source of truth for the ARM shape, primitives, and the keep/drop decisions in this spec): [`15-private-network-standard-agent-setup`](https://github.com/microsoft-foundry/foundry-samples/tree/main/infrastructure/infrastructure-setup-bicep/15-private-network-standard-agent-setup).
- [Unified `azure.yaml` design](../unify-azure-yaml/spec.md) — establishes the `host: microsoft.foundry` shape and defers VNet to this work (§1.4).
- [Bicep-less provisioning](../bicepless-foundry/spec.md) — the synthesizer and provider this spec extends.
