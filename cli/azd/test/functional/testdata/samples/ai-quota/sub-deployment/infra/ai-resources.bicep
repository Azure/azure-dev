// Module for AI resources — deployed into the resource group created by main.bicep.
param resourceToken string
param deployLocation string
param gptModelName string
param gptModelVersion string
param gptDeploymentType string
param gptDeploymentCapacity int
param embeddingModelName string
param embeddingDeploymentCapacity int

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
