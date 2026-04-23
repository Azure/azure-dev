// Subscription-scoped template for testing AI model quota preflight checks.
// Creates a resource group and deploys cognitive services with model deployments.
targetScope = 'subscription'

@description('Name of the environment')
param environmentName string

@description('Primary location for all resources')
param location string

@description('GPT model name to deploy')
param gptModelName string = 'gpt-4o'

@description('GPT model version')
param gptModelVersion string = '2024-08-06'

@description('GPT deployment SKU type')
@allowed(['Standard', 'GlobalStandard'])
param gptDeploymentType string = 'GlobalStandard'

@description('GPT deployment capacity (set very high to trigger quota warning)')
param gptDeploymentCapacity int = 99999

@description('Embedding model name')
param embeddingModelName string = 'text-embedding-3-small'

@description('Embedding deployment capacity (set very high to trigger quota warning)')
param embeddingDeploymentCapacity int = 99999

@description('Override location for AI deployments. If empty, uses primary location.')
param aiDeploymentsLocation string = ''

@description('A time to mark on created resource groups, so they can be cleaned up via an automated process.')
param deleteAfterTime string = dateTimeAdd(utcNow('o'), 'PT1H')

var deployLocation = empty(aiDeploymentsLocation) ? location : aiDeploymentsLocation
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg-ai-quota-${resourceToken}'
  location: location
  tags: {
    DeleteAfter: deleteAfterTime
  }
}

module aiResources 'ai-resources.bicep' = {
  name: 'ai-resources'
  scope: rg
  params: {
    resourceToken: resourceToken
    deployLocation: deployLocation
    gptModelName: gptModelName
    gptModelVersion: gptModelVersion
    gptDeploymentType: gptDeploymentType
    gptDeploymentCapacity: gptDeploymentCapacity
    embeddingModelName: embeddingModelName
    embeddingDeploymentCapacity: embeddingDeploymentCapacity
  }
}
