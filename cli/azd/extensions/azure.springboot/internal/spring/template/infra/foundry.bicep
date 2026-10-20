param location string
param deploymentName string
param modelName string
param modelVersion string
param modelSku string
param modelCapacity int
param outboundIps array
@allowed(['apiKey', 'managedIdentity'])
param authentication string
param applicationPrincipalId string

var accountName = 'aifoundry${uniqueString(resourceGroup().id)}'
var openAIUserRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '5e0bd9bd-7b93-4f28-af87-19fc36ad61bd'
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

resource deployment 'Microsoft.CognitiveServices/accounts/deployments@2025-06-01' = {
  parent: account
  name: deploymentName
  sku: {
    name: modelSku
    capacity: modelCapacity
  }
  properties: {
    model: {
      format: 'OpenAI'
      name: modelName
      version: modelVersion
    }
    versionUpgradeOption: 'NoAutoUpgrade'
  }
}

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
