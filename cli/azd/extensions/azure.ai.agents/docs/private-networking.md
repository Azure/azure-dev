# Private networking for `host: azure.ai.project`

A Foundry project service can be provisioned as a **network-secured (VNet-bound)** account by adding a `network:` block to the `host: azure.ai.project` service in `azure.yaml`. When `network:` is omitted, the account uses public networking.

Do **not** place `network:` on `host: azure.ai.agent`. Agent services describe deployable agents and depend on the project through `uses:`; the project service owns account-level provisioning inputs such as `endpoint:`, `deployments:`, and `network:`.

The `azure.ai.projects` extension owns the project service and the `microsoft.foundry` provider. `azd ai agent init` continues to author the block and eject its IaC during the staged ownership migration.

When `network:` is present, azd always provisions an **account private endpoint** and disables public data-plane access. Without `agentHosting:`, dependent stores (Cosmos DB, AI Search, Storage) stay platform-managed.

Automatic Application Insights monitoring works for a private network with your own agent subnet (BYO egress, `agentSubnet` set) and for managed egress (see below). For a component azd creates, azd creates the Application Insights resource and Log Analytics workspace with public ingestion disabled and public query enabled, an Azure Monitor Private Link Scope (ingestion Private Only, query Open, matching the Foundry samples), an `azuremonitor` private endpoint in the `peSubnet`, and the four Azure Monitor private DNS zones, then connects the project with an ApiKey connection. Ingestion is private only. Right after a fresh deployment, private ingestion can return 403 for a while (observed up to about 20 minutes) before traces arrive. Live testing confirmed that Foundry service-side traces from a deployed prompt agent arrive and can be read from inside the network with this configuration.

Provisioning is never blocked by monitoring in unsupported cases. With your own agent subnet you can also connect an existing Application Insights resource: azd creates its own Private Link Scope, private endpoint and DNS zones and links the component and its workspace to the scope without modifying the component (local authentication must stay enabled). Because azd does not own the component, it cannot re-update it; if ingestion returns 403 after provisioning, touching the component (for example, updating a tag) can clear it.

Shared private DNS zones (`network.dns`), a virtual network that already has an `azuremonitor` private endpoint (a virtual network can use only one Azure Monitor Private Link Scope; azd does not adopt an existing one), or a network azd cannot inspect skip automatic monitoring, and azd prints a warning (also on stderr).

With managed egress (no `agentSubnet`), azd also adds two private-endpoint outbound rules to the managed network and grants the account identity the Azure AI Enterprise Network Connection Approver role on the resource group so the managed private endpoints are approved:

- `ampls-monitor-rule` (target `azuremonitor`) lets the agent runtime reach the Private Link Scope.
- `foundry-account-pe` (target `account`, to the account itself) is required for hosted agents. Without it, hosted-agent calls fail with `Public access is disabled`; prompt agents are not affected.

If `isolationMode` is not set, azd uses `AllowInternetOutbound` so the managed network resource exists. Hosted agents export telemetry with their own instance identity, which only exists after the agent is deployed. On `azd deploy`, azd grants that identity `Monitoring Metrics Publisher` on the Application Insights component. This is best effort: if the grant fails, azd prints the `az role assignment create` command, and hosted-agent export returns 403 until the role exists. Role propagation can take several minutes. In a live run with `AllowOnlyApprovedOutbound`, hosted-agent traces reached the workspace and were read from inside the network once both rules existed and the identity had this role.

**Impact on a shared virtual network.** The Azure Monitor private DNS zones and Private Link Scope change Azure Monitor routing for the whole virtual network, not only this project. With ingestion Private Only, any workload in the network (or in a network sharing its DNS zones) can send telemetry only to Application Insights or Log Analytics resources that are in the scope; telemetry to resources outside it stops arriving. A virtual network can use only one Private Link Scope, so to keep other workloads' telemetry, add their Application Insights components or Log Analytics workspaces as scoped resources of the Private Link Scope that azd created. To avoid any change to Azure Monitor routing, set `monitoring: {enabled: false}`. Check for existing workloads before you provision.

For **existing AKS hosting**, set `agentHosting.clusterResourceId` on the project service. Without `network:`, the Foundry account uses public networking; with it, the account uses the private-networking configuration below. Hosting creates a Storage account and two separate managed identities. The native azd registry, its Project connection and image-pull grants are created only when an Agent requires building a container image, including with private account networking. Storage and any automatically created ACR expose public authenticated endpoints. See [Existing AKS hosting](../../azure.ai.projects/README.md#existing-aks-hosting) for cluster prerequisites and standard Foundry teardown behavior.

Legacy `network.mode`, `network.byo`, and `network.managed` fields are no
longer migrated into a project service. Rewrite legacy configuration using the
current `peSubnet` schema before running `project add`.

<!-- azd:doc-example partial -->
```yaml
infra:
  provider: microsoft.foundry

services:
  my-agent:
    host: azure.ai.agent
    project: src/my-agent
    uses:
      - ai-project
    image: myprivacr.azurecr.io/agents/my-agent:v1

  ai-project:
    host: azure.ai.project
    deployments:
      - name: gpt-4o-mini
        model: { format: OpenAI, name: gpt-4o-mini, version: "2024-07-18" }
        sku: { name: GlobalStandard, capacity: 50 }
    network:
      # ----- Egress: agent runtime network (pick ONE) -----
      #
      # (a) Managed egress (shown live below): omit agentSubnet so the agent
      #     runs in the Microsoft-managed network. isolationMode is valid only
      #     in this mode.
      isolationMode: AllowOnlyApprovedOutbound  # or AllowInternetOutbound
      #
      # (b) BYO egress: inject the agent into your subnet instead. Replace the
      #     isolationMode line above with an agentSubnet block (same VNet as
      #     peSubnet in v1):
      #   agentSubnet:
      #     vnet: ${AZURE_VNET_ID}
      #     name: agent-subnet
      #     prefix: 192.168.10.0/24   # omit prefix to reference an existing subnet

      # ----- Ingress: account private endpoint (REQUIRED) -----
      peSubnet:
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24       # omit prefix to reference an existing subnet

      # ----- Private DNS (optional) -----
      dns:
        resourceGroup: rg-private-dns               # omit to let azd create + link the zones
        subscription: ${AZURE_DNS_SUBSCRIPTION_ID}  # optional; defaults to deployment subscription
```

Run `azd ai agent init --no-prompt --agent-name my-agent --image <registry/image:tag>` to scaffold the agent. The generated `azure.yaml` contains an `azure.ai.agent` service and an `azure.ai.project` service named `ai-project`; add the `network:` block to the `ai-project` service.

## Field reference

| Field | Rule |
| --- | --- |
| `agentSubnet` | Optional. Present: the agent is injected into this customer subnet (BYO egress). Absent: the agent uses the Microsoft-managed network (managed egress). |
| `peSubnet` | **Required.** Subnet for the account private endpoint. Establishes the private data plane (public access disabled). |
| `isolationMode` | Optional. `AllowInternetOutbound` or `AllowOnlyApprovedOutbound`. Valid **only** when `agentSubnet` is omitted (managed egress). |
| subnet `vnet` | Required. ARM id of the VNet that holds (or will hold) the subnet. Supports `${VAR}`. When `agentSubnet` is present, it must reference the same VNet as `peSubnet`. |
| subnet `name` | Required. Subnet name. |
| subnet `prefix` | Optional. Omit to reference an existing subnet; set to create the subnet with that CIDR. |
| `dns.resourceGroup` | Omitted: azd creates and links the AI private DNS zones. Set: azd references existing zones in that resource group. Requires `peSubnet`. |
| `dns.subscription` | Optional. Defaults to the deployment subscription. Accepts a bare GUID or `${VAR}`. |

## Environment variables

Network fields support `${VAR}` references resolved client-side from the azd environment. The variable names are user-chosen; the examples use:

| Variable | Format | Used by |
| --- | --- | --- |
| `AZURE_VNET_ID` | ARM resource id of an existing `Microsoft.Network/virtualNetworks` | subnet `vnet` |
| `AZURE_DNS_SUBSCRIPTION_ID` | bare GUID or `/subscriptions/<guid>` | `network.dns.subscription` |

Configure them with `azd env set`, for example:

```bash
azd env set AZURE_VNET_ID "<vnet-resource-id>"
azd env set AZURE_DNS_SUBSCRIPTION_ID "<subscription-id>"
```

## Limitations

- **Project-owned network settings.** `network:` is supported only on the `host: azure.ai.project` service. Multiple agents may depend on the same project; they do not carry separate VNet settings.
- **Single VNet (v1).** When `agentSubnet` is present it must live in the same VNet as `peSubnet`. Managed egress is unaffected because it needs only the `peSubnet` VNet.
- **No auto-created registry without AKS hosting.** When `agentHosting:` is absent, private networking does not support an automatically created registry; use a pre-built image or code/ZIP deployment instead. The image belongs to the `azure.ai.agent` service; the VNet configuration belongs to the `azure.ai.project` service. The developer owns the registry's SKU, private endpoint, DNS, and firewall.
- **Brownfield (`endpoint:`) ignores `network:`.** When `endpoint:` is set on the project service, the account's network posture is fixed by whoever created it; azd warns and does not reconcile `network:`.
- **One default-DNS account per VNet.** Without a `dns:` block azd links the three `privatelink.*` AI zones to your VNet, and a VNet may hold only one link per namespace. A second account (or a brownfield hub that pre-links the zones) must use `dns:` reference mode to bind the private endpoint without re-linking.
- **Terraform IaC is not supported for private networking (v1).** Bicep-only today; `azd ai agent init --infra=terraform` is refused when `network:` is declared. Eject Bicep instead.

## Scenario 1 — Managed egress: private account, agent on Microsoft's network

Omit `agentSubnet` so the hosted-agent runtime uses a Microsoft-managed network. `peSubnet` is still required: the account data plane stays private behind an account private endpoint in your VNet, reachable from inside the VNet, a peered VNet, or VPN.

<!-- azd:doc-example partial -->
```yaml
infra:
  provider: microsoft.foundry

services:
  my-agent:
    host: azure.ai.agent
    project: src/my-agent
    uses:
      - ai-project
    image: myprivacr.azurecr.io/agents/my-agent:v1

  ai-project:
    host: azure.ai.project
    deployments: []
    network:
      isolationMode: AllowInternetOutbound
      peSubnet:
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24
```

Provision and deploy:

```bash
azd env set AZURE_SUBSCRIPTION_ID "<sub>"
azd env set AZURE_LOCATION westus
azd env set AZURE_RESOURCE_GROUP "<rg>"
azd env set AZURE_VNET_ID "<vnet-resource-id>"
azd provision --no-prompt
azd deploy --no-prompt
```

Run deploy and invoke from a host that can reach the account private endpoint:

```bash
azd ai agent invoke --new-session "hello"
```

## Scenario 2 — BYO egress: agent injected into your VNet subnet

Set `agentSubnet` to inject the hosted-agent runtime into your customer subnet. `agentSubnet` and `peSubnet` must reference the same VNet in v1.

<!-- azd:doc-example partial -->
```yaml
services:
  my-agent:
    host: azure.ai.agent
    project: src/my-agent
    uses:
      - ai-project
    image: myprivacr.azurecr.io/agents/my-agent:v1

  ai-project:
    host: azure.ai.project
    network:
      agentSubnet:
        vnet: ${AZURE_VNET_ID}
        name: agent-subnet
        prefix: 192.168.10.0/24
      peSubnet:
        vnet: ${AZURE_VNET_ID}
        name: pe-subnet
        prefix: 192.168.11.0/24
```

Common failures:

- `403 Public access is disabled`: the data plane is private in every network-bound mode — run deploy/invoke from inside the VNet, a peered VNet, or VPN.
- `ImageError: registry authentication failed`: grant ACR pull permission to the Foundry project managed identity.

## Scenario 3 — Eject and customize the Bicep (advanced)

The synthesized template covers the common private-networking shapes. When you need something it does not express — an extra subnet, a private endpoint for a BYO dependent store, custom DNS wiring, a non-default account property, or additional `networkInjections` rules — eject the Bicep, edit it directly, and let azd provision your edited tree.

```bash
# Scaffold first, declare network: on the azure.ai.project service, then eject:
azd ai agent init --infra
```

Eject reads `network:` from the `host: azure.ai.project` service and writes the full Bicep tree. For a Foundry-only project, it writes `infra/main.bicep`, `infra/modules/{resources,network,subnet,private-endpoint-dns,acr}.bicep`, and `infra/main.parameters.json`. If the project already has infrastructure, azd preserves it and adds a separate `infra/foundry` layer with the same file layout and its own `AZURE_FOUNDRY_RESOURCE_GROUP`. `${VAR}` placeholders are preserved in the generated parameters file and resolved from the azd environment at provision time.

```bash
azd provision --no-prompt
```
