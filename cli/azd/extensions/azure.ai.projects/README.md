# Foundry Projects

Manage Microsoft Foundry Project resources from your terminal. (Preview)

## Extension telemetry API

Extension code can report best-effort usage events through the shared
`pkg/foundry/telemetry` package:

```go
reporter := telemetry.NewReporter(azdClient.Telemetry(), nil)
reporter.Report(ctx, telemetry.Event{
  Name: "project.operation.completed",
  Attributes: map[string]string{
    "operation": "provision",
  },
})
```

The example is illustrative; this extension does not currently emit a product
usage event through this interface. Existing provisioning span attributes are
unchanged by this adapter. Add an event only after its product question, bounded
values, documentation, and privacy review are agreed. Before emitting, declare each
`ext.*` attribute in `cli/azd/extensions/telemetry/fields.go` — reuse an existing
declaration only when its meaning, allowed values, classification, and purpose are
identical, otherwise declare a distinct key — as enforced by
`go test ./extensions/telemetry`.

`Report` has no return value and never changes command or provider behavior. It
uses a one-second timeout, does not retry, and does not log attribute values or
transport error details. Put approved event builders and finite-value types in
`internal/telemetry/events.go`; do not call `ReportUsage` directly from command,
service-target, or provisioning code. Never include project endpoints, ARM IDs,
deployment names, resource names, paths, URLs, or other customer content. The azd
host records events only for extensions installed from the official registry.

## Non-interactive automation

See the shared [AI extension non-interactive input reference](../ai-non-interactive.md)
for every prompt's flag, environment/configuration input, or deterministic
no-prompt behavior.

## `azure.yaml` ownership

This extension owns `host: azure.ai.project` services and the `microsoft.foundry` provisioning provider. A project service carries account-level settings such as an existing project endpoint, model deployments, and private networking.

```yaml
infra:
  provider: microsoft.foundry

services:
  my-project:
    host: azure.ai.project
    endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          name: GlobalStandard
          capacity: 50
```

When `endpoint` is omitted, `azd provision` creates a Foundry account and project. When it is set, provisioning reuses that project and reconciles the declarations that can be applied to an existing account.

### Deployment preview

`azd deploy --preview` performs no work for an `azure.ai.project` service, just
like its normal package/publish/deploy lifecycle. The provider returns no readable
message and does not emit an unsupported-preview warning. It does not read Azure
resources, initialize deployment state, or perform provisioning.

The host still displays its own per-service progress line, for example
`Done: Previewing service ai-project`. The provider message is empty and omitted
from host JSON; `data` contains `status: "noOp"`, `scope: "deployment"`, and
`infrastructurePreviewed: false`. This is not a comparison of project resources,
model deployments, networking, or RBAC; those remain provisioning-owned.

### Developer access

The built-in provisioning template does not assign a role to the developer. Before deploying a prompt, hosted, or voice agent, the agent extension checks project access and attempts to grant Foundry User when needed. If that grant is denied, it warns and deployment continues; an administrator may need to assign the role.

User-authored on-disk Bicep templates can still declare their own role assignments. Supply any required `principalId` and `principalType` values in the template's parameter file; the provider does not generate them.

### Infrastructure layers

For projects that use `infra.layers`, declare exactly one layer with
`provider: microsoft.foundry` and leave the root provider available for the
other layers:

```yaml
infra:
  provider: bicep
  layers:
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
```

When `azd ai project add --infra` is used with layers, infrastructure is
ejected into the Foundry layer's configured path and module. The root
`microsoft.foundry` provider cannot be combined with named layers.

When `project add` migrates a legacy Foundry service, retired
`network.mode`, `network.byo`, and `network.managed` fields are rejected
instead of being copied into the new project service. Rewrite the block using
the current `peSubnet` schema (and `agentSubnet` or `isolationMode` as needed)
before retrying.

## Project authoring

Add or adopt a Foundry project in the current azd workspace. If `azure.yaml`
is missing, the command first creates a minimal azd project:

```sh
azd ai project add
azd ai project deployment add --model <model-name>
azd provision
```

`project deployment add` creates the azd workspace and Foundry project
configuration when they are missing, then adds the deployment. In automation,
provide the project identity and Azure environment values required by
`project add`; incomplete non-interactive input fails before the deployment is
changed.

Managed deployment resolution requires one Azure location. Pass `--location`,
set `AZURE_AI_DEPLOYMENTS_LOCATION`, or set `AZURE_LOCATION`. Interactive runs
prompt for a location when none is configured; `--no-prompt` returns actionable
guidance instead of searching all subscription regions.

Use `--force` with an explicit `--project-id` or `--project-endpoint` when
replacing a different configured project. The command rejects `--force`
without an explicit target instead of silently ignoring the flag.

Use `--new-project` when a new project must be created even if the workspace
already has a project endpoint or project ID configured:

```sh
azd ai project add --new-project
```

`--new-project` cannot be combined with `--project-id` or
`--project-endpoint`.

To use an existing project in automation, initialize it with its full ARM
resource ID. This stores the project identity in the active azd environment
and allows managed deployment declarations to be reconciled:

```sh
azd ai project add --project-id "<project-resource-id>"
azd ai project deployment add --model <model-name>
```

An endpoint-only project is suitable for configuration that does not manage
resources on the existing project. If managed deployments are already
declared, endpoint-only setup stops before clearing the project ID.
Use the full project resource ID before adding managed deployments.

### Host agents on a customer-owned AKS cluster

For a newly created Foundry account, the experimental `agentHosting` input on the
`azure.ai.project` service identifies the customer-owned cluster:

```yaml
infra:
  provider: microsoft.foundry

services:
  my-project:
    host: azure.ai.project
    agentHosting:
      clusterResourceId: ${AZURE_AKS_CLUSTER_ID}
    network:
      agentSubnet:
        vnet: ${FOUNDRY_VNET_RESOURCE_ID}
        name: ${FOUNDRY_AGENT_SUBNET_NAME}
      peSubnet:
        vnet: ${FOUNDRY_VNET_RESOURCE_ID}
        name: ${FOUNDRY_PE_SUBNET_NAME}
```

BYO AKS requires explicit `network.agentSubnet` and `network.peSubnet`.
The VNet must already exist; `vnet` is its full ARM resource ID and `name`
is the subnet name, not a resource ID. Both descriptors must select the same
VNet and different subnet names. Both `vnet` and `name` support azd environment
references; unresolved or empty names fail before Azure writes. Omit `prefix` to reference an existing subnet;
set an explicit available CIDR in either descriptor to create that subnet through
the standard network template. Agent subnet creation adds
`Microsoft.App/environments` delegation. No default VNet or CIDR is selected.
For managed-VNet AKS pools without `vnetSubnetID`, read-only N02 validation
resolves the node subnet from the cluster's `nodeResourceGroup`, verifies that
the resource group's `managedBy` points to that cluster, and reads the VMSS
network profiles tagged with the selected pool's `aks-managed-poolName`.
This requires resource-group and VMSS read access in the node resource group,
plus subnet read access. Missing or inconsistent evidence remains `UNVERIFIED`;
the client does not infer generated network names or change the AKS configuration.
Missing Agent-subnet injection, including PE-only Microsoft-managed egress,
is rejected before authentication, preflight persistence or Azure writes.

This standard network path disables public access to the Foundry data plane
and creates an account private endpoint. Provisioning/deployment/invocation
must run with the necessary private routing and DNS resolution; an ordinary
public Internet connection is not sufficient. By default the template creates
and links the three AI private DNS zones. With `network.dns.resourceGroup`,
it references existing zones without creating their VNet links.
BYO-created ACR remains Premium with public access enabled and no ACR private
endpoint. Supplied ACR configuration is not changed by account isolation.

`hostingType` defaults to `ManagedCluster` and `name` to `aks`. Optional
`storageAccountResourceId` selects an existing Storage account.
Select an existing container registry through `AZURE_CONTAINER_REGISTRY_RESOURCE_ID`.
`hostingManagementIdentityResourceId` and `workloadIdentityResourceId` independently
select existing user-assigned identities. Omit either field to create that identity;
the two identities must be distinct. Supplied identities retain their exact ARM IDs,
including same-tenant cross-subscription or cross-resource-group locations, and are
not tagged or owned by azd. Explicit empty, null, malformed or missing supplied
identities are rejected. The public schema has `additionalProperties: false`;
Agent subnet configuration belongs in standard `network.agentSubnet`.

The workflow is `azd init`, `azd provision`, then `azd deploy`. Provision first
collects read-only resource and scoped authorization evidence. Mandatory `FAIL`
or `UNVERIFIED` results stop dependency creation and Foundry deployment. Preview
does not create identities, grants, or other resources. Resource checks read ARM
directly and report service errors; they do not first infer permission to read
from role assignments. I02 validates the AKS,
supplied Storage, ACR, identities and target subscription references and owning tenants;
same-tenant cross-subscription references are not rejected merely for using
different subscriptions. The existing Agent subnet selected through standard
`network.agentSubnet` is not an I02 referenced resource. Its independent N02
basic subnet checks still apply.

Recovery of an existing managed-identity registry connection whose GET response
masks credentials also reads its current identity using the exact connection's
`Microsoft.CognitiveServices/accounts/projects/connections/listSecrets/action`.
This read-only POST does not update the resource. Its real response supplies
permission evidence without a separate P08 role-assignment permission check;
denied or incomplete responses stop retry before another deployment.

**P06 G01-G03 grant authority** checks the operator's ordinary, unconditional
`Microsoft.Authorization/roleAssignments/write` permission:

- Check only grants selected for the ARM graph. G01 is written only for a new
  hosting identity. G02 is written when either identity is azd-created; G03 when
  the workload identity or Storage is azd-created.
- G01 uses the existing AKS scope. G02 uses an existing Workload identity scope
  or the future identity's existing parent. G03 uses existing Storage, or its
  existing parent for a new account. Creation parents are the existing target RG,
  or subscription when the RG does not exist. Identical scopes are checked once.
- Match the operator object ID and explicit token group claims against applicable
  assignments, including RG/subscription inheritance. Evaluate role definitions'
  `Actions/NotActions`; conditioned or delegated assignments do not provide this
  ordinary unconditional allow.
- No future resource or managed-identity principal is needed. P06 does not query
  Graph or evaluate complex conditions, delegation or deny assignments. Missing
  unconditional permission is `FAIL`; unreadable or incomplete required evidence
  is `UNVERIFIED`. Both block provisioning.
- This check runs only in preflight. A pass establishes the basic unconditional
  allow, not a guarantee of effective permission under every policy; ARM
  authorizes each actual grant during deployment.

**Supplied-identity permissions** are checked against each identity's actual
principal, not the operator. A supplied hosting identity requires AKS `Reader`
and `Kubernetes Extension Contributor`, plus `Managed Identity Federated Identity
Credential Contributor` on an existing workload identity. A supplied workload
identity requires `Storage Blob Data Contributor` on existing Storage. Applicable
direct/RG/subscription inherited or provably equivalent unconditional role
permissions are accepted; unsupported conditional/delegated evidence or unreadable
required data is `UNVERIFIED`. Missing confirmed permissions fail without repairing
customer-preexisting grants. No Graph query or write probe runs.

If azd will create the dependency, that identity-permission check is
`NOT_APPLICABLE` with an explicit deferral to the same ARM graph. On retry, a
verified unfinished old deployment intent can finish its exact missing grant
even after the azd-created dependency exists. Completed or revoked grants,
unrelated resources, and active or unknown old attempts cannot use this exception.

**P07 kubelet registry authorization** uses the AKS kubelet object ID:

- Supplied ACR: existing applicable pull authorization passes without requiring
  operator grant-write authority. Deployment reuses the exact assignment, including
  RG/subscription inheritance, without another G04 write. Customer grants remain
  unowned; journaled owned grants retain their cleanup responsibility.
- Without existing pull authorization, inspect the operator's applicable role
  assignments and definitions at that ACR for
  `Microsoft.Authorization/roleAssignments/write` through `Actions/NotActions`.
- New ACR: check that basic action at the existing target RG, or subscription
  when the RG does not exist; do not query the future registry.
- Ordinary ACR accepts `AcrPull`, `AcrPush` or equivalent pull actions. ABAC ACR
  requires `Container Registry Repository Reader` or equivalent repository read
  data actions, not ARM `Reader`.
- Read failures remain blocking. P07 matches the operator object ID and explicit
  token group claims with basic role permissions. It does not discover additional
  group memberships or evaluate complex delegation/deny conditions.
  A basic-action pass does not guarantee the eventual fine-grained grant; actual
  ARM authorization errors remain failures.

P09 covers only BYO-added nested deployments outside the Foundry target resource
group and the project's new customer-registry, extra-scope or ABAC grant branches.
It does not repeat ordinary Foundry, model, connection, top-level/target-group
deployment or standard-network permission checks. Full resource-group IDs are
compared, including subscription; ordinary target-group deployments are excluded
even before that group exists. An owned registry resolved from its journaled
environment output does not become a customer-registry branch on retry. Azure
ARM still authorizes every actual operation during deployment.
P02-P05 and P09 match the operator object ID and explicit token group claims against
applicable direct or inherited assignments and check role definitions'
`Actions/NotActions` for the selected action. They do not read deny assignments,
query Graph, evaluate conditions/delegation or require complete
future resource or identity-recipient evidence. Unknown groups supply no confirmed
allow and do not themselves cause `UNVERIFIED`. Condition, condition-version and
delegated-identity fields do not by themselves negate a basic action allow.
No confirmed matching action allow is `FAIL`; missing, unreadable or undecodable
basic token, assignment or role-definition evidence is `UNVERIFIED`. Both remain
mandatory. A pass establishes only the basic action allow; ARM authorizes actual
conditions, deny and policy.
Operator assignments returned by ARM's target-scope `atScope()` query may also
inherit from a management group. ARM supplies that applicability evidence; azd
does not infer management-group ancestry from resource-ID prefixes or discover
additional group memberships locally. If the ARM token reports group overage,
azd also queries ARM `assignedTo('<operator-object-id>')` to obtain service-proven
operator assignments, including transitive group assignments. Their actual
scopes, role permissions and local `NotActions` still apply; child-scope grants
cannot authorize parent operations. Missing required ARM evidence remains
`UNVERIFIED`. P06 still rejects conditioned/delegated grants and requires an
unconditional allow; supplied identities retain their separate principal checks.
P02 checks identity write only for created identities, with necessary read/assign
actions retained. P04/P05 follow the selected Storage/ACR create or reference
operation; referencing already-created owned Storage on retry does not require a
Storage PUT. Existing unconditional idempotent PUTs still require their permissions.
Developer access retains the agent predeploy lifecycle, not an additional
provision-time grant.

**S03 Storage public baseline** applies only to supplied Storage:

- A valid HTTPS Blob endpoint, `publicNetworkAccess=Enabled` and firewall
  `defaultAction=Allow` pass. This proves only the public static configuration,
  not actual connectivity.
- With valid basic properties, restricted public access (`Enabled/Deny`) or
  disabled public access is `NOT_APPLICABLE` (skipped), with an explicit warning
  that the network path was not verified. This S03 result does not block provisioning.
- S03 does not select an AKS source subnet or read service endpoints, Private
  Endpoints, VNets/peerings or Private DNS configuration.
- Storage resource-read errors, absent supplied resources, invalid HTTPS Blob
  endpoints and missing required basic properties remain mandatory and block
  provisioning; they are not treated as skipped network paths.
- New Storage is `NOT_APPLICABLE`, with no S03 reads or post-deployment network check.
  Independent N02 and C01 checks retain their own reads.

N02 validates basic subnet conditions only for an existing Agent
subnet selected through standard `network.agentSubnet`. Referenced-resource checks cover
ID, type, existence, readiness and owning-tenant consistency, not private-preview
subscription or region admission. The planned target resource group need not
exist during preflight. Terraform eject is unavailable for this path because it
does not run the extension's mandatory read-only preflight and owned-deployment
lifecycle.

`network.agentSubnet` is the sole customer Agent-subnet input. N02 is
`NOT_APPLICABLE` when selected for creation, without N02 configuration,
topology or creation-permission checks. BYO onboarding does not automatically
select a VNet, subnet or CIDR. Explicit subnet creation uses the existing standard
network module. Non-BYO network behavior remains unchanged:

| Standard `network` configuration | Account network injection | BYO AKS |
|---|---|---|
| Omitted | `null` for non-BYO | Rejected before writes |
| Valid, without `network.agentSubnet` | Microsoft-managed network for non-BYO | Rejected before writes |
| Valid, with `network.agentSubnet` | `scenario: agent`, standard subnet ID, `useMicrosoftManagedNetwork: false` | Required |

The standard network template's subnet creation/reference, private endpoint, DNS
and managed egress behavior and validation remain unchanged. P03 follows only the
selected standard-network operations: necessary reads and joins, subnet creation
when selected, Private Endpoint/zone-group writes, and DNS creation/linking or
existing-zone joins. It does not require AKS or VNet writes absent from the graph.
BYO provisioning checks operator permissions only in preflight;
there is no additional client-side permission check before the single ARM
deployment. ARM authorizes each actual deployment operation.
Selecting `network.agentSubnet` for creation skips only N02, not S03 or C01.

When an existing `network.agentSubnet` is selected, N02 compares its CIDRs **only
against the selected AKS pool's Node subnet**. Pod/Service, sibling-subnet and
direct-peering address inventories are not collected or required for overlap
checking. Downstream VPN/ExpressRoute/NVA address discovery is not required either.
Missing or invalid required Node-subnet metadata still blocks its check.
N02 reads only the selected Agent subnet, its parent VNet and the selected Node
subnet. It validates canonical private IPv4 CIDRs with /27 or larger capacity,
parent-VNet containment, selected Node-subnet non-overlap and
`Microsoft.App/environments` delegation. Parent VNet and Node-subnet identity and
readiness checks remain required. Missing required basic reads or metadata is
mandatory `UNVERIFIED`; explicit noncompliance or a missing resource is mandatory
`FAIL`. Both block provisioning.
N02 does not inspect peering state, NSG rules, route tables, Storage network
rules, Private Endpoints, Private DNS or custom-DNS configuration.
`PASS` proves only these basic subnet conditions, not routing, DNS or actual
connectivity. No workload DNS/TCP probe is performed.
N01 platform-telemetry Storage service-tag verification is deferred and is not
implemented by the client. A future service-owned Storage endpoint/FQDN contract
could support targeted connectivity verification; that contract is not provided here.
S03's supplied-Storage public baseline and non-blocking restricted/private skip
remain independent.
N02 is not rerun after resource creation. Supplied Storage and C01 checks remain
read-only preflight requirements; there is no preparation-time network verifier.
Existing standard subnet references are read-only.
Older automatic-subnet journals are retained for exact owned cleanup and block
new provisioning until that lifecycle is resolved. Retries cannot silently
restore a removed hosting field or switch network selections on an existing account.

After all preflight gates pass, provision submits **one subscription-scope ARM
deployment**. Nested modules create/reference the resource group, create/reference
each user-assigned identity, create/reference Storage and ACR, and declare selected G01-G04 with dependencies
before the Foundry account's hosting submission. After the project exists,
F01 uses its actual managed identity to grant ACR pull access, then the registry
connection depends on that grant. No SDK resource/grant-creation stage runs
before ARM, and no second deployment is needed for the connection.
Supplied Storage/ACR/UAMIs are referenced rather than rewritten.
New BYO Storage and identity names use the final effective `foundryProjectName`,
including an explicit template parameter when supplied. Lowercase that project
name, retain only ASCII `a-z` and `0-9`, then take at most the first six remaining
characters as `foundryPart`. A name with no remaining characters is rejected.

| Resource | Generated name | Maximum length |
|---|---|---|
| Hosted-agent Storage | `sa{foundryPart}{ownerToken}` | 24 |
| Hosting identity | `hosting-{foundryPart}{ownerToken}` | 30 |
| Workload identity | `workload-{foundryPart}{ownerToken}` | 31 |

`ownerToken` is the 16 lowercase hexadecimal characters encoding the first eight
bytes of `SHA256(UTF8(lowercase(parentResourceGroupId) + "|" + environmentName + "|" + projectPath))`.
Only the parent resource-group ID is lowercased; the environment name and raw
project-path string are not trimmed, lowercased, cleaned or made absolute.
The Foundry name and location are not part of the hash. For example,
`Sample-Project_123` produces `foundryPart=sample`. Supplied Storage keeps its
ARM ID and name; ACR and Foundry account/project naming remain unchanged.
Retries retain exact journaled ownership: a conflicting earlier name is not
adopted or replaced, and cleanup uses the persisted resource IDs.

New BYO Storage explicitly sets `StorageV2`, the Hot tier, HNS disabled,
public network access enabled and firewall default action `Allow`, with
`Standard_ZRS` by default. HTTPS-only access, minimum TLS 1.2, disabled shared keys
and disabled anonymous Blob access remain enforced.
Only locations in `storageZRSUnsupportedRegions` in
[`deployment_bindings.go`](internal/onboarding/deployment_bindings.go) select
`Standard_LRS` before creation. The list records its review date, Microsoft Learn
sources and the Storage SKU API used to refresh it. The location still comes from
`AZURE_LOCATION`; matching ignores case and surrounding whitespace without changing
that selection. Service errors do not trigger an LRS fallback, and supplied or
already created accounts retain their existing SKU on retry.
For supplied Storage, S01 reads the account's actual ARM SKU. ZRS, GZRS and
RA-GZRS provide zone redundancy; LRS, GRS and RA-GRS do not. Accounts without
zone redundancy receive a non-blocking recommendation after the preflight
report. An unavailable or unrecognized SKU produces an advisory uncertainty
warning. Neither recommendation changes the account or relaxes the mandatory
Storage type, Hot tier, HNS, security or network checks.
New BYO ACR uses API `2025-03-01-preview` with
`roleAssignmentMode: LegacyRegistryPermissions`, Premium SKU, the admin user
disabled and public network access enabled. These creation settings apply only
to the BYO prerequisite module; ordinary Foundry resource configuration is unchanged.
Supplied registries, including ABAC registries, are referenced without configuration
updates. Retries retain the bound registry selection and authorization mode.
ARM dependencies order configuration; they do not prove data-plane RBAC
propagation or workload connectivity. Service errors remain failures rather than
triggering a second client-side preparation/deployment loop.
Storage does not need to be dedicated to this workflow. Preserve the environment
value `AZD_FOUNDRY_BYO_AKS_PREPARATION`: its compatibility name now refers to the
deployment-ownership record, not an independent resource-preparation executor.
It retains exact deployment attempts, owned targets and cleanup progress, not
credentials or proof of data-plane propagation. Its separate `resourceProgress`
map has exactly `storageAccount`, `containerRegistry`, `hostingIdentity` and
`workloadIdentity`. Each entry records `resourceId`, original user-source
`supplied`, current-round `preflightPassed` and `preparationComplete`. Missing
legacy fields remain unconfirmed. Preflight always reruns, including after a
previous pass; preview never saves progress. Progress is not an ownership ledger.

Completion requires actual resource configuration and the selected preparation
operations: Storage plus workload access; hosting identity plus both AKS roles and
hosting-to-workload access; workload identity plus federation and Storage access;
ACR readiness/mode plus kubelet/project access and the project registry connection.
Existence or an owner tag alone is insufficient. Control-plane readback does not
prove RBAC propagation or data-plane reachability. Valid deployment intent and
actual resources allow recovery after a successful cloud write and failed local
save; missing grant evidence remains incomplete.

Template binding preserves existing resource-group
tags across retries while honoring explicit customer tag changes. Conflicting
private ownership tags, including case-insensitive aliases, are rejected.
The private account target, deployment attempts and ownership markers are
persisted for recovery.
BYO on-disk Bicep must retain the standard deployment resource bindings;
supported selections belong in configuration or parameter inputs. Altered
templates are rejected before deployment. Each new attempt records a SHA-256
digest of the submitted template content. Recovery and cleanup read the exact
deployment's exported template and verify that digest, along with ownership,
target parameters, correlation and terminal state. Template-level metadata and
ARM declaration-type capitalization are normalized; resource configuration,
including resource metadata, remains bound. The Bicep generator hash is not
compared with ARM's independently calculated service hash. Older attempts without
a content digest require the exported content to match the trusted built-in
standard template version identified by their recorded generator hash.
The independently observed ARM hash is retained after verification; changed or
unreadable evidence blocks recovery. A planned account's absence alone is not
sufficient evidence. Explicit resource-group and
location parameters must agree with the environment's resolved target.
When the Project API omits ownership tags, observation, retry and teardown
require an exact succeeded Project Create reached through succeeded Create
operations in that verified deployment graph, plus the exact owner-tagged parent
account. The project's ID, location, succeeded state, system-assigned identity
and tenant must match; an explicitly conflicting tag or changed recorded
principal still blocks recovery and cascading deletion.
When Connection GET masks managed-identity credentials, registry preparation
requires the exact succeeded connection Create in the content-bound deployment
graph, the verified project's actual system-assigned principal and owner-tagged
parent account, and its exact owned registry pull grant read back from ARM.
The connection must still name the expected registry endpoint and resource ID
and explicitly exclude workspace-managed identity. A read-only `listSecrets`
request must confirm the current ManagedIdentity client and registry IDs; the
historical Create and pull grant do not substitute for that current identity.
Explicit credential conflicts, user-assigned identity, conditional or changed
grants, and unreadable evidence block completion and retry. Response bodies are
not logged; only the required non-key managed-identity fields are decoded.
If ARM rejects a nested deployment during provider preflight validation before
registering it, recovery requires the exact failed parent Create operation and
recognized not-found responses for both the nested record and its operations.
That absent branch supplies no owned-grant evidence. Authorization failures,
unknown errors and missing operations on an existing nested deployment still block
recovery; successful sibling operations remain independently verifiable.
Foundry failures retain nested deployment-operation errors and correlation IDs;
unavailable diagnostic reads are reported separately from the original failure.

`azd down` uses the persisted ownership journal even if the current hosting
configuration changes. It never deletes the resource group. It first verifies
terminal deployment evidence, inventories projects and model deployments, deletes
only tracked models, and confirms deletion of the owned account. Service-owned
federated credentials must disappear before dependency grants are removed; azd
does not delete those credentials itself. Cleanup then deletes only proven-owned
assignments, identities, Storage, registry and historical owned subnet IDs. Supplied
resources (including supplied UAMIs), unrelated supplied-identity federated
credentials, reused customer grants and later-added resource-group resources are
preserved. Unknown ownership, an active deployment, unexpected account children,
or an in-use/changed subnet stops cleanup and retains the recoverable journal.
Progress survives permission failures and interruption; retry `azd down` after
resolving the reported condition. Successful cleanup invalidates resource progress;
failed cleanup preserves the recovery journal.

Normal account deletion is soft deletion and can reserve the name for 48 hours.
Only explicit `azd down --purge` requests irreversible purge; model deployments
are removed first to avoid charges persisting through soft deletion. Journals
from versions without authoritative Foundry lifecycle evidence are rejected
rather than used to infer account absence or delete dependencies.

Foundry agent hosting configuration is fixed when the account is created. It
cannot be added to an existing account or changed after creation, so
`agentHosting` cannot be used together with `endpoint`.

To reconcile deployments or a pending container registry on an existing project, set the project's full ARM resource ID in the active azd environment:

```sh
azd env set AZURE_AI_PROJECT_ID "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>"
```

`azd ai agent init` sets this value when initialized against an existing project. An endpoint-only service with no resources to reconcile does not require it.

Split `host: azure.ai.connection` services are reconciled during deploy by the
`azure.ai.connections` extension, never by Project provisioning. This applies
to embedded templates and newly ejected Bicep/Terraform alike. Project synthesis
does not read Connection or Toolbox payloads, environments, or credentials.
The system ACR connection used by the Project's registry remains Project-owned.

### Breaking migration

Upgrade the related Foundry extensions together. On-disk Bicep whose compiled
template declares generic Foundry Connection resources (including inline nested
deployments) is rejected with migration guidance. Remove those resources, their
associated parameters and aggregate readiness outputs, or regenerate the IaC
after saving custom changes. Unrelated user-owned parameters named `connections`
or `connectionCredentials` are allowed and retain normal parameter substitution;
parameter names alone do not identify the removed Foundry contract. Linked
templates are not fetched or inspected by this validation.

Only the generated system ACR connection's name, target, project identity and
registry resource ID wiring are exempt. Other `ContainerRegistry` connections,
including those using `ManagedIdentity`, belong in `azure.ai.connection` services.
Preserve that generated wiring when customizing ejected Bicep, or regenerate it.

Update previously ejected Terraform manually as well, including any
required state handoff to avoid destroying resources when removing declarations.
Extension upgrades do not automatically rewrite user-owned IaC.

Declare Connections and Toolboxes as independent services, keep Agent `uses`
dependencies, and run `azd deploy --all` after provisioning the Project. See the
[Connections migration guide](../azure.ai.connections/README.md#breaking-migration).

## Eject existing-project infrastructure

Generate editable infrastructure for an existing Foundry project with its full
ARM resource ID:

```sh
azd ai project add --project-id "<project-resource-id>" --infra
azd ai project add --project-id "<project-resource-id>" --infra=terraform
```

The default format is Bicep. The generated infrastructure references the
existing account and project without taking ownership of them. It manages only
declared model deployments, the system ACR connection, and any required container
registry resources. Endpoint-only setup cannot eject infrastructure; rerun
`project add` with the full project resource ID.

When an agent needs a registry, ejection preserves the registry state selected
during initialization: it creates a registry when none exists, connects an
existing registry when needed, or references an existing project connection
without managing it. Terraform registry output is always named
`container-registry.tf`; Bicep uses `modules/container-registry.bicep`.
Before writing generated files, existing registry endpoints are normalized to
remove URL credentials, query parameters, and fragments.

Project ejection does not read or emit split Connection payloads or credentials,
including values loaded from a local `$ref`. Keep those on `azure.ai.connection`
services, reconciled by the Connections extension during deployment.

Terraform ejection does not support private networking and cannot adopt a
registry already created by the `microsoft.foundry` provider. After ejection,
future `azd provision` runs use the generated files. To add another managed
model deployment after ejection, edit the generated Bicep
`<module>.parameters.json` or Terraform `<module>.tfvars.json` file directly,
then run `azd provision`; `project deployment add` does not update ejected
parameter files.

When provisioning reports insufficient Cognitive Services quota, check usage for the target region with
`az cognitiveservices usage list --location <region>` or request a quota increase in the Azure portal. If an
existing Foundry project should be reused instead, configure its endpoint and set `AZURE_AI_PROJECT_ID` to the
full project resource ID before retrying.

The `azd ai project set`, `show`, and `unset` commands manage the default Foundry project endpoint context. Use `azd ai project add` to author the project service in `azure.yaml`.
