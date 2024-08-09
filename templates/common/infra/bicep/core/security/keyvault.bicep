metadata description = 'Creates an Azure Key Vault.'
param name string
param location string = resourceGroup().location
param tags object = {}

// Allow public network access to Key Vault
param allowPublicNetworkAccess bool = true

// Allow all Azure services to bypass Key Vault network rules
param allowAzureServicesAccess bool = true

param networkAcls object = {
  bypass: allowAzureServicesAccess ? 'AzureServices' : 'None'
  defaultAction: allowPublicNetworkAccess ? 'Allow' : 'Deny'
  ipRules: []
  virtualNetworkRules: []
}
param principalId string = ''

@description('Allow the key vault to be used during resource creation.')
param enabledForDeployment bool = false
@description('Allow the key vault to be used for template deployment.')
param enabledForTemplateDeployment bool = false

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' = {
  name: name
  location: location
  tags: tags
  properties: {
    tenantId: subscription().tenantId
    sku: { family: 'A', name: 'standard' }
    networkAcls: networkAcls
    accessPolicies: !empty(principalId) ? [
      {
        objectId: principalId
        permissions: { secrets: [ 'get', 'list' ] }
        tenantId: subscription().tenantId
      }
    ] : []
    enabledForDeployment: enabledForDeployment
    enabledForTemplateDeployment: enabledForTemplateDeployment
  }
}

output endpoint string = keyVault.properties.vaultUri
output id string = keyVault.id
output name string = keyVault.name
