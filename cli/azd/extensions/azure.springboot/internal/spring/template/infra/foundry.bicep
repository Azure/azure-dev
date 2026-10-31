param location string
param chatEnabled bool
param chatDeploymentName string
param chatModelName string
param chatModelVersion string
param chatModelSku string
param chatModelCapacity int
param embeddingEnabled bool
param embeddingDeploymentName string
param embeddingModelName string
param embeddingModelVersion string
param embeddingModelSku string
param embeddingModelCapacity int
param outboundIps array
@allowed(['apiKey', 'managedIdentity'])
param authentication string
param applicationPrincipalId string

var accountName = 'aifoundry${uniqueString(resourceGroup().id)}'
var openAIUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '5e0bd9bd-7b93-4f28-af87-19fc36ad61bd'
)
var deployments = concat(
  chatEnabled
    ? [
        {
          name: chatDeploymentName
          modelName: chatModelName
          modelVersion: chatModelVersion
          sku: chatModelSku
          capacity: chatModelCapacity
        }
      ]
    : [],
  embeddingEnabled
    ? [
        {
          name: embeddingDeploymentName
          modelName: embeddingModelName
          modelVersion: embeddingModelVersion
          sku: embeddingModelSku
          capacity: embeddingModelCapacity
        }
      ]
    : []
)

resource account 'Microsoft.CognitiveServices/accounts@2025-06-01' = {
  name: accountName
  location: location
  kind: 'AIServices'
  identity: { type: 'SystemAssigned' }
  sku: { name: 'S0' }
  properties: {
    allowProjectManagement: true
    customSubDomainName: accountName
    disableLocalAuth: authentication == 'managedIdentity'
    publicNetworkAccess: 'Enabled'
    networkAcls: {
      defaultAction: 'Deny'
      ipRules: [for ip in union(outboundIps, []): { value: ip }]
      virtualNetworkRules: []
    }
  }
}

@batchSize(1)
resource modelDeployments 'Microsoft.CognitiveServices/accounts/deployments@2025-06-01' = [
  for deployment in deployments: {
    parent: account
    name: deployment.name
    sku: {
      name: deployment.sku
      capacity: deployment.capacity
    }
    properties: {
      model: {
        format: 'OpenAI'
        name: deployment.modelName
        version: deployment.modelVersion
      }
      versionUpgradeOption: 'OnceCurrentVersionExpired'
    }
  }
]

resource openAIUser 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (authentication == 'managedIdentity') {
  name: guid(account.id, applicationPrincipalId, openAIUserRoleId)
  scope: account
  properties: {
    principalId: applicationPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: openAIUserRoleId
  }
}

output accountName string = account.name
