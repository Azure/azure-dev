// Azure Container Registry for hosted agents that use docker:.
// Wires the registry as a connection on the Foundry project so the
// project's managed identity can pull images.
//
// Premium SKU is intentional: Foundry recommends Premium so the registry
// can support content trust and geo-replication if the user enables them
// post-provision.

// Parameters

@description('Azure region.')
param location string

@description('Tags applied to all resources.')
param tags object = {}

@description('Registry name. 5-50 alphanumeric chars.')
@minLength(5)
@maxLength(50)
param name string

@description('Name of the existing Foundry CognitiveServices account that hosts the project receiving the ACR connection.')
param foundryAccountName string

@description('Name of the existing Foundry project receiving the ACR connection.')
param foundryProjectName string

@description('Principal id of the Foundry project managed identity; receives AcrPull and is the connection credential identity.')
param foundryProjectPrincipalId string

@description('When true, the registry disables public network access to stay inside the isolation boundary.')
param enableNetworkIsolation bool = false

@description('Registry to reference without rewriting its configuration.')
param registryResourceId string = ''

@description('Use Container Registry Repository Reader for repository ABAC registries.')
param registryABAC bool = false

@description('Internal ownership marker on managed role assignments.')
param owner string = ''

// Variables

// Built-in role definition ids. See: https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
var acrPullRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  registryABAC ? 'b93aa761-3e63-49ed-ac28-beffa264f7ac' : '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)
var reuseRegistry = !empty(registryResourceId)
var effectiveRegistryId = reuseRegistry ? registryResourceId : registry!.id
var effectiveEndpoint = reuseRegistry ? existingRegistry!.properties.loginServer : registry!.properties.loginServer
var effectiveName = reuseRegistry ? last(split(registryResourceId, '/')) : name

// Resources

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' = if (!reuseRegistry) {
  name: name
  location: location
  tags: tags
  sku: {
    name: 'Premium'
  }
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    adminUserEnabled: false
    // Disable public access when network isolation is enabled so the registry
    // stays inside the VNet boundary. Docker-backed agents in isolated projects
    // must pull via the private endpoint; public access would leave a dependency
    // outside the isolation perimeter and can break pulls in locked-down egress.
    publicNetworkAccess: enableNetworkIsolation ? 'Disabled' : 'Enabled'
    zoneRedundancy: 'Disabled'
  }
}

resource existingRegistry 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = if (reuseRegistry) {
  scope: resourceGroup(split(registryResourceId, '/')[2], split(registryResourceId, '/')[4])
  name: last(split(registryResourceId, '/'))
}

// Grant the Foundry project's managed identity AcrPull on this registry so the
// hosted agent can pull images using the project identity.
resource foundryAcrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!reuseRegistry) {
  name: guid(registry.id, foundryProjectPrincipalId, acrPullRoleId)
  scope: registry
  properties: {
    principalId: foundryProjectPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: acrPullRoleId
    ...(!empty(owner) ? { description: owner } : {})
  }
}

module preparedRegistryAccess 'acr-pull-role-assignment.bicep' = if (reuseRegistry) {
  name: 'prepared-registry-project-access'
  scope: resourceGroup(split(registryResourceId, '/')[2], split(registryResourceId, '/')[4])
  params: {
    registryName: effectiveName
    principalId: foundryProjectPrincipalId
    owner: owner
    roleDefinitionId: subscriptionResourceId(
      split(registryResourceId, '/')[2],
      'Microsoft.Authorization/roleDefinitions',
      registryABAC ? 'b93aa761-3e63-49ed-ac28-beffa264f7ac' : '7f951dda-4ed3-4680-a7ca-43fe172d538d'
    )
  }
}

// Existing parent references so the connection can be nested under the
// project. Pinned to 2025-04-01-preview: GA 2025-06-01 fails to resolve the
// projects/connections ContainerRegistry sub-resource (MissingApiVersionParameter).
resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  name: foundryAccountName

  resource project 'projects' existing = {
    name: foundryProjectName

    // Project-scoped connection so Foundry can resolve the registry by name.
    resource acrConnection 'connections' = {
      name: '${effectiveName}-conn'
      properties: {
        category: 'ContainerRegistry'
        target: effectiveEndpoint
        authType: 'ManagedIdentity'
        // RegistryIdentity auth requires both the identity client id (the
        // project principal) and the registry resource id.
        credentials: {
          clientId: foundryProjectPrincipalId
          resourceId: effectiveRegistryId
        }
        isSharedToAll: true
        metadata: {
          ResourceId: effectiveRegistryId
        }
      }
      dependsOn: [
        foundryAcrPull
        preparedRegistryAccess
      ]
    }
  }
}

// Outputs

output loginServer string = effectiveEndpoint
output resourceId string = effectiveRegistryId
output connectionName string = foundryAccount::project::acrConnection.name
output roleAssignments array = reuseRegistry ? [preparedRegistryAccess!.outputs.grant] : [{
  id: foundryAcrPull!.id
  scope: registry!.id
  principalId: foundryProjectPrincipalId
  roleDefinitionId: acrPullRoleId
}]
