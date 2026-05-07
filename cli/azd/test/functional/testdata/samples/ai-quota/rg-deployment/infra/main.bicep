// Minimal resource-group scoped template for testing AI model quota preflight checks.
// All model parameters are configurable to exercise different quota validation scenarios.
targetScope = 'resourceGroup'

@description('Name of the environment')
param environmentName string

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

@description('Override location for AI deployments. If empty, uses resourceGroup().location.')
param aiDeploymentsLocation string = ''

var deployLocation = empty(aiDeploymentsLocation) ? resourceGroup().location : aiDeploymentsLocation
var resourceToken = toLower(uniqueString(resourceGroup().id, environmentName))

resource aiServices 'Microsoft.CognitiveServices/accounts@2024-10-01' = {
  name: 'ai-${resourceToken}'
  location: deployLocation
  sku: {
    name: 'S0'
  }
  kind: 'AIServices'
  properties: {
    customSubDomainName: 'ai-${resourceToken}'
    publicNetworkAccess: 'Enabled'
  }
}

resource gptDeployment 'Microsoft.CognitiveServices/accounts/deployments@2024-10-01' = {
  parent: aiServices
  name: gptModelName
  properties: {
    model: {
      format: 'OpenAI'
      name: gptModelName
      version: gptModelVersion
    }
  }
  sku: {
    name: gptDeploymentType
    capacity: gptDeploymentCapacity
  }
}

resource embeddingDeployment 'Microsoft.CognitiveServices/accounts/deployments@2024-10-01' = {
  parent: aiServices
  name: embeddingModelName
  properties: {
    model: {
      format: 'OpenAI'
      name: embeddingModelName
    }
  }
  sku: {
    name: 'GlobalStandard'
    capacity: embeddingDeploymentCapacity
  }
  dependsOn: [gptDeployment]
}
