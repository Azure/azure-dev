<!-- cspell:ignore privatelink azurecr vnet subnet subnets myprivacr cognitiveservices UAMI hund hund030 huimiu -->

# Private networking for `host: microsoft.foundry`

## Problem

Foundry network isolation is unreachable from `azure.yaml` today. Per the [unified `azure.yaml` design](https://github.com/Azure/azure-dev/pull/8590) (§1.4), VNet binding is an **Account** setting while the unified shape models the **Project**, so customizing it requires ejecting to Bicep. A developer who needs a network-secured agent must hand-author the service team's standard network-secured template and maintain it by hand. There is no `network:` surface, and the in-memory synthesizer that [bicep-less provisioning](https://github.com/Azure/azure-dev/pull/8577) introduced always emits a public account (`publicNetworkAccess: 'Enabled'`).

This spec adds a declarative `network:` block to the `host: microsoft.foundry` service and teaches the existing synthesizer to provision a network-bound account from it.

### Why the block sits on the service

VNet binds at the Account level, yet `network:` sits on a service entry that reads as a project. This is intentional and unambiguous in the greenfield flow: the synthesizer provisions **one Account plus one Project per Foundry service** (1:1), so "network on the service" is exactly "network on that service's account." azd does not model multiple projects sharing a single network-bound account — multiple Foundry services produce multiple accounts, each with its own `network:`. Brownfield (`endpoint:`) ignores `network:` because the account is already bound by whoever created it (see the Brownfield interaction section), so the service-level declaration never reconciles against an account azd did not create. If Foundry later needs N projects under one network-bound account, the block would promote to an account-scoped surface.

## Solution

Extend the Foundry provisioning synthesizer (`internal/synthesis`, introduced by the bicep-less work) to read a new `network:` block on the service body and emit the network primitives a secured agent account requires: optional agent runtime VNet injection, an account private endpoint, and the AI private DNS zones — each created or referenced per the developer's declaration.

The block is purely additive. When `network:` is absent the synthesizer behaves exactly as today (public account). When present, the account data plane is **always private**: the account has `publicNetworkAccess: 'Disabled'`, its network ACL default action is `Deny`, and an account private endpoint is provisioned in `peSubnet`. No new provider, no new gRPC surface, no core change beyond the schema slice — the block rides the same `AdditionalProperties` channel every other Foundry key already uses.

Dependent stores (Cosmos DB, AI Search, Storage) stay **platform-managed**, which is supported under VNet isolation. This removes the bulk of the sample template (BYO stores, their private endpoints, role assignments, and both capability hosts) from azd's responsibility.

The example below illustrates one scenario (BYO egress with explicit subnets); it is not the full schema. See the `azure.yaml` surface section for the field reference and the create-vs-reference rules.

```yaml
infra:
  provider: microsoft.foundry

services:
  my-project:
    host: microsoft.foundry

    # New: private networking for the Foundry account.
    # Omitted => public account (unchanged behavior).
    # Present => private data plane (always).
    network:
      agentSubnet:
        vnet: ${AZURE_VNET_ID}
        name: agent-subnet
        prefix: 192.168.10.0/24
      peSubnet:
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24
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

- **Private data plane** — declaring `network:` always disables account public access and provisions an account private endpoint.
- **BYO egress** — inject the hosted-agent runtime into an existing customer VNet by declaring `agentSubnet`.
- **Microsoft-managed egress** — omit `agentSubnet` and optionally choose a managed-network `isolationMode`.
- **Create or reference subnets** — `prefix` present creates a subnet; `prefix` absent references an existing subnet.
- **Private DNS** — create/link the AI private DNS zones by default, or reference central/shared zones with `dns:`.
- **Platform-managed dependent stores** under VNet isolation.
- **BYO container image** as the registry story for secured agents.

**Out of scope**

- Public opt-out when `network:` is declared — omit `network:` for a public account.
- Cross-VNet v1 topologies — `agentSubnet` and `peSubnet` must target the same VNet when both are declared.
- BYO dependent stores and the capability-host wiring they require — managed stores are used instead.
- Tool subnet, user-assigned managed identity (UAMI), customer-managed keys (CMK) — not core to the secured-agent scenario.
- Local build into a private ACR — secured agents bring a pre-built image; the developer owns ACR networking. See the ACR private networking section.
- The bicep-less synthesizer/provider itself and the unified `azure.yaml` shape — prerequisites tracked by their own specs (see the Relationship to in-flight work section).

## Relationship to in-flight work

Private networking is the last layer on a stack already in flight on the `huimiu/foundry-azure-yaml` feature branch. It does not start from `main`; it lands as a follow-on into that branch after the synthesizer merges.

```
unified azure.yaml (huimiu)            #8590 docs → branch huimiu/foundry-azure-yaml
├─ bicep-less provisioning (hund030)   #8577 docs / #8643 impl  ← integration point
├─ foundry service target + init       #8629 / #8675
└─ BYO image                           #8645 remote-build skip, merged
```

Two consequences matter here:

- **No core change.** Unknown service keys ride `ServiceConfig.AdditionalProperties` to the extension (unify §2.1), and the synthesizer reads the raw `azure.yaml` bytes directly, so the only required core edit is the JSON schema slice (see the `azure.yaml` surface section).
- **ACR is off the critical path.** The BYO-image flow and the merged remote-build skip for VNet-injected accounts keep registry work out of v1 (see the ACR private networking section).

## `azure.yaml` surface

`network:` is a sibling of `deployments:` / `agents:` on the service body.

```yaml
services:
  my-project:
    host: microsoft.foundry

    network:                          # present => private data plane (always). Omit for public.

      # ── EGRESS: agent runtime network ──────────────────────────────
      # agentSubnet PRESENT -> inject agent into YOUR subnet  (useMicrosoftManagedNetwork: false)
      # agentSubnet ABSENT  -> Microsoft-managed VNet         (useMicrosoftManagedNetwork: true)
      agentSubnet:
        vnet: ${AZURE_VNET_ID}        # required — full VNet ARM id
        name: agent-subnet            # required — subnet leaf name
        prefix: 192.168.10.0/24       # optional — PRESENT=create, ABSENT=reference existing

      # Managed-egress outbound posture. VALID ONLY when agentSubnet is ABSENT.
      # -> properties.managedNetwork.isolationMode
      isolationMode: AllowOnlyApprovedOutbound      # | AllowInternetOutbound

      # ── INGRESS: account data plane (REQUIRED) ─────────────────────
      # Always provisions an account private endpoint -> publicNetworkAccess: Disabled.
      peSubnet:
        vnet: ${AZURE_VNET_ID}        # required
        name: pe-subnet               # required
        prefix: 192.168.11.0/24       # optional — PRESENT=create, ABSENT=reference existing

      # Private DNS zones for the account PE. Optional; defaults to create + link
      # to the PE's VNet. Override to reference central/shared zones. Requires peSubnet.
      dns:
        resourceGroup: my-dns-rg
        subscription: ${AZURE_DNS_SUBSCRIPTION_ID}
```

### Field semantics

| Field | Rule |
| --- | --- |
| `network` | Optional. Omitted means the public-account behavior is unchanged. Present means the data plane is private in every egress mode. |
| `agentSubnet` | Optional. Present selects BYO egress and sets the account agent `networkInjections` entry to `useMicrosoftManagedNetwork: false`. Absent selects Microsoft-managed egress (`useMicrosoftManagedNetwork: true`). |
| `agentSubnet.vnet` | Required when `agentSubnet` is present. Full ARM id of the customer VNet. `${VAR}` is resolved from the azd environment before synthesis. |
| `agentSubnet.name` | Required when `agentSubnet` is present. Subnet leaf name. |
| `agentSubnet.prefix` | Optional. Present means azd creates the subnet with that prefix. Absent means azd references an existing subnet with `name`. |
| `isolationMode` | Optional. Valid only when `agentSubnet` is absent (managed egress). One of `AllowInternetOutbound`, `AllowOnlyApprovedOutbound`. Maps to `properties.managedNetwork.isolationMode` on the account managed network. |
| `peSubnet` | Required when `network:` is present. It is the account private endpoint subnet, and its presence is what makes the account data plane private. |
| `peSubnet.vnet` | Required. Full ARM id of the customer VNet. `${VAR}` is resolved from the azd environment before synthesis. |
| `peSubnet.name` | Required. Subnet leaf name. |
| `peSubnet.prefix` | Optional. Present means azd creates the subnet with that prefix. Absent means azd references an existing subnet with `name`. |
| `dns.resourceGroup` | Optional. When omitted (or the whole `dns:` block is omitted), azd creates the required private DNS zones and links them to the PE VNet. When set, azd references existing zones in that resource group. |
| `dns.subscription` | Optional. Defaults to the deployment subscription. Accepts a bare GUID or `${VAR}`. Only meaningful alongside `dns.resourceGroup`. |

`${VAR}` resolves client-side from the azd environment for VNet ids and `dns.subscription`. `${{...}}` Foundry expressions are not expected in network fields and are passed through verbatim if present, consistent with the shared expander.

## Synthesizer and template changes (high level)

The bicep-less work landed `internal/synthesis/synthesizer.go` plus an embedded `templates/` tree (`main.bicep`, `modules/acr.bicep`, `abbreviations.json`, and a precompiled `main.arm.json` fallback). The account today is hardcoded to public. The changes are additive and local to this package.

- **Read `network:`** — extend the `foundryService` view the synthesizer decodes so it captures the flat `network` sub-tree (`agentSubnet`, `isolationMode`, `peSubnet`, `dns`) and resolves `${VAR}` in VNet ids / `dns.subscription` before emitting parameters. Absent block → all network parameters default off and output is byte-identical to today.
- **Derive egress** — do not expose a `mode` enum. `agentSubnet == nil` selects Microsoft-managed egress; `agentSubnet != nil` selects BYO egress.
- **Emit parameters** — add the network parameter set to the synthesizer `Result.Parameters`: `enableNetworkIsolation`, `useManagedEgress`, the subnet descriptors, managed isolation mode, and DNS zone RG/subscription.
- **`main.bicep`** — guard the network path on a single `enableNetworkIsolation` condition. When on: include `modules/network.bicep`, set the account's `publicNetworkAccess: 'Disabled'`, `networkAcls.defaultAction: 'Deny'`, and the account agent `networkInjections`, and include `modules/private-endpoint-dns.bicep`. When off: the account block is exactly today's.
- **BYO egress** — when `agentSubnet` is present, provision/reference the agent subnet and write an account `networkInjections` entry pointing at that subnet with `useMicrosoftManagedNetwork: false`.
- **Managed egress** — when `agentSubnet` is absent, write an account agent `networkInjections` entry with `useMicrosoftManagedNetwork: true`; when `isolationMode` is set, provision the `managedNetworks/default` child resource with that value.
- **Ingress** — when `network:` is present, `peSubnet` is required and the private endpoint is always provisioned in it. There is no managed-mode public data-plane exception.
- **New modules** — `modules/network.bicep` (reference the VNet + create/reference subnets, agent-subnet delegation when creating) and `modules/private-endpoint-dns.bicep` (account private endpoint + the three AI DNS zones `privatelink.services.ai.azure.com`, `privatelink.openai.azure.com`, `privatelink.cognitiveservices.azure.com` + VNet links, create or reference).
- **Regenerate `main.arm.json`** — the precompiled ARM fallback must be rebuilt from the extended `main.bicep` and committed alongside it. The byte-stability contract from the bicep-less spec applies: same `azure.yaml` → byte-identical output within a patch version.

The provisioning provider, on-disk/eject behavior, and parameter wiring need no structural change — they consume whatever `Result.Parameters` carries.

## Validation pipeline additions

Network validation slots into the synthesizer's existing pre-synthesis checks and runs on every `provision`, `preview`, and eject:

- **Private means private** — if `network:` is present, `peSubnet` is required; missing `peSubnet` is an error, never a silent public fallback.
- **Subnet shape** — each declared subnet (`agentSubnet`, `peSubnet`) requires `vnet` and `name`; `prefix` is optional and must be a valid CIDR when present.
- **VNet ids** — every subnet `vnet` is a well-formed `Microsoft.Network/virtualNetworks` ARM id after `${VAR}` resolution.
- **Single VNet v1** — when both subnets are declared, `agentSubnet.vnet` and `peSubnet.vnet` must resolve to the same VNet.
- **Egress coherence** — `isolationMode` is valid only when `agentSubnet` is absent (managed egress). If `agentSubnet` is present, managed-network isolation is rejected with a clear error.
- **DNS** — when `dns.resourceGroup` is set, validate the resource-group name; normalize `dns.subscription` to a bare GUID (accept `/subscriptions/<guid>` or a bare GUID), matching the sample's behavior.
- **Env resolvability** — every `${VAR}` in a network field resolves from the azd environment; an unresolved reference fails with the variable name.

Failures surface with the service-scoped field path, e.g. `services.my-project.network.agentSubnet: isolationMode is only valid when agentSubnet is absent`.

## Update behavior

Re-running `azd provision` against the same environment re-synthesizes from the current `azure.yaml` and performs an ARM incremental update against the same account name. Whether a particular edit succeeds is constrained by the Foundry resource provider:

| Edit | Expected behavior |
| --- | --- |
| Add `network:` to a public greenfield account | Supported when the service accepts the resulting account update and private endpoint creation. |
| Change DNS from create to reference (or the reverse) | Supported as an infrastructure update if the referenced zones and VNet links already exist. |
| Tighten managed `isolationMode` | Supported in the service direction of more restriction (for example `AllowInternetOutbound` → `AllowOnlyApprovedOutbound`). |
| Relax managed `isolationMode` or disable managed network after enabling it | Not supported by the service. Create a new environment/account instead. |
| Switch BYO egress ↔ managed egress after agents/capability host exist | Not a v1 contract. Create a new environment/account instead. |

The docs should present network topology changes as provision-time configuration, with update support limited to safe/idempotent re-provisioning and service-supported tightening.

## Brownfield interaction

`endpoint:` on the service already short-circuits synthesis — the synthesizer returns `ErrEndpointBrownfield` and the provider connects to the existing project without provisioning. A network-secured account reached this way is **already** network-bound by whoever created it.

Therefore, when `endpoint:` is present, `network:` is **ignored** (the account's network posture is fixed and not azd's to change). This is documented as explicit precedence: `endpoint:` wins, and a project that wants azd to manage its network posture must be greenfield (no `endpoint:`). If both are present, azd warns that `network:` has no effect in brownfield mode.

## ACR private networking — decision and RoI

A secured agent still needs its image to come from somewhere reachable from the agent runtime. Two paths:

| Path | What azd must do | Effort | v1 |
| --- | --- | --- | --- |
| **BYO image (`--image`)** | Nothing. The developer brings `registry/image:tag` and owns the registry's SKU, private endpoint, DNS, and firewall. | ~0 | **Chosen** |
| **Local build → private ACR** | Provision a Premium ACR with a private endpoint and `privatelink.azurecr.io`, then solve build connectivity: a remote ACR-task build must egress to a private registry, or a local push must reach it from outside the VNet (developer IP allowlist). | High; many connectivity edge cases | **Deferred** |

The hard part of the local-build path is not creating the registry — it is making the build actually reach a network-isolated registry, which drags in build-agent network placement azd does not control. v1 therefore standardizes on BYO image for secured agents. Revisit local-build-into-private-ACR after telemetry shows `--image` adoption and real demand.

## Telemetry and docs

**Telemetry**

- `provision.network_mode` — `none` | `byo` | `managed`, emitted at provision start. The value is derived from `network:` presence and `agentSubnet` presence. Lets us measure secured-agent adoption and BYO-vs-managed split. Implementation PRs add it to `docs/reference/telemetry-data.md`.

**Docs**

- New env vars consumed for network fields (`AZURE_VNET_ID`, `AZURE_DNS_SUBSCRIPTION_ID`, or whatever the synthesizer reads) are documented in `cli/azd/docs/environment-variables.md` with format and default.
- Extension README documents the `network:` block and the BYO-image requirement for secured agents.

## References

- Service team's standard network-secured agent template (source of truth for the ARM shape, primitives, and the keep/drop decisions in this spec): [`15-private-network-standard-agent-setup`](https://github.com/microsoft-foundry/foundry-samples/tree/main/infrastructure/infrastructure-setup-bicep/15-private-network-standard-agent-setup).
- Service team's managed-network sample for private data plane + Microsoft-managed egress: [`18-managed-virtual-network`](https://github.com/microsoft-foundry/foundry-samples/tree/main/infrastructure/infrastructure-setup-bicep/18-managed-virtual-network).
- [Unified `azure.yaml` design](https://github.com/Azure/azure-dev/pull/8590) — establishes the `host: microsoft.foundry` shape and defers VNet to this work (§1.4).
- [Bicep-less provisioning](https://github.com/Azure/azure-dev/pull/8577) — the synthesizer and provider this spec extends.
