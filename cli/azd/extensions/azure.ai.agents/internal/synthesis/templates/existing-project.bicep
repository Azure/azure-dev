// Editable infrastructure for an existing Foundry project. The account and
// project are referenced only; scoped modules manage project children and an
// optional adjunct resource group without taking ownership of the project.

targetScope = 'subscription'

type deploymentType = {
  name: string
  model: {
    name: string
    format: string
    version: string
  }
  sku: {
    name: string
    capacity: int
  }
}

type connectionType = {
  name: string
  category: string
  target: string
  authType: string
  audience: string?
  connectorName: string?
  metadata: object?
}

param projectResourceId string
param resourceGroupName string
param location string
param resourceTokenSalt string = ''
param tags object = {}
param deployments deploymentType[] = []
@allowed([
  'none'
  'create'
  'reuse-connect'
  'already-connected'
])
param acrMode string = 'none'
param existingAcrResourceId string = ''
param existingAcrEndpoint string = ''
param existingAcrConnectionName string = ''
param acrPullAssigned bool = false
param projectEndpoint string
param connections connectionType[] = []
@secure()
param connectionCredentials object = {}

var projectIdParts = split(projectResourceId, '/')
var projectSubscriptionId = projectIdParts[2]
var projectResourceGroupName = projectIdParts[4]
var accountName = projectIdParts[8]
var projectName = projectIdParts[10]
var tokenSeed = '${subscription().subscriptionId}${resourceGroupName}${resourceTokenSalt}'
var acrName = 'cr${toLower(uniqueString(tokenSeed))}'
var createAcr = acrMode == 'create'
var reuseAcr = acrMode == 'reuse-connect' || acrMode == 'already-connected'
var createAcrConnection = acrMode == 'create' || acrMode == 'reuse-connect'
var effectiveAcrName = createAcr ? acrName : (reuseAcr ? last(split(existingAcrResourceId, '/')) : '')
var effectiveAcrEndpoint = createAcr
  ? newContainerRegistry!.outputs.endpoint
  : (acrMode == 'reuse-connect' && !acrPullAssigned ? existingContainerRegistry!.outputs.endpoint : existingAcrEndpoint)
var effectiveAcrResourceId = createAcr
  ? newContainerRegistry!.outputs.resourceId
  : (acrMode == 'reuse-connect' && !acrPullAssigned ? existingContainerRegistry!.outputs.resourceId : existingAcrResourceId)

resource adjunctResourceGroup 'Microsoft.Resources/resourceGroups@2021-04-01' = if (createAcr) {
  name: resourceGroupName
  location: location
  tags: tags
}

resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  scope: resourceGroup(projectSubscriptionId, projectResourceGroupName)
  name: accountName

  resource project 'projects' existing = {
    name: projectName
  }
}

module newContainerRegistry 'modules/container-registry.bicep' = if (createAcr) {
  name: 'container-registry'
  scope: resourceGroup(resourceGroupName)
  params: {
    mode: 'create'
    location: location
    tags: tags
    registryName: acrName
    projectPrincipalId: foundryAccount::project.identity.principalId
  }
  dependsOn: [adjunctResourceGroup]
}

module existingContainerRegistry 'modules/container-registry.bicep' = if (acrMode == 'reuse-connect' && !acrPullAssigned) {
  name: 'container-registry'
  scope: resourceGroup(split(existingAcrResourceId, '/')[2], split(existingAcrResourceId, '/')[4])
  params: {
    mode: 'reuse-connect'
    location: location
    tags: tags
    registryName: effectiveAcrName
    projectPrincipalId: foundryAccount::project.identity.principalId
  }
}

module projectResources 'modules/foundry-project.bicep' = {
  name: 'foundry-project-resources'
  scope: resourceGroup(projectSubscriptionId, projectResourceGroupName)
  params: {
    accountName: accountName
    projectName: projectName
    deployments: deployments
    connections: connections
    connectionCredentials: connectionCredentials
    acrName: effectiveAcrName
    acrEndpoint: effectiveAcrEndpoint
    acrResourceId: effectiveAcrResourceId
    createAcrConnection: createAcrConnection
    existingAcrConnectionName: existingAcrConnectionName
  }
}

output AZURE_AI_PROJECT_ID string = projectResourceId
output AZURE_AI_ACCOUNT_NAME string = accountName
output AZURE_AI_PROJECT_NAME string = projectName
output AZURE_OPENAI_ENDPOINT string = 'https://${accountName}.openai.azure.com/'
output FOUNDRY_PROJECT_ENDPOINT string = projectEndpoint
output AZURE_FOUNDRY_RESOURCE_GROUP string = createAcr ? resourceGroupName : ''
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = acrMode == 'none'
  ? ''
  : effectiveAcrEndpoint
output AZURE_CONTAINER_REGISTRY_RESOURCE_ID string = acrMode == 'none'
  ? ''
  : effectiveAcrResourceId
output AZURE_AI_PROJECT_ACR_CONNECTION_NAME string = projectResources.outputs.acrConnectionName
output AZURE_AI_PROJECT_CONNECTION_NAMES string = projectResources.outputs.connectionNames
output AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT string = projectEndpoint
output AZD_FOUNDRY_ACR_MODE string = acrMode
