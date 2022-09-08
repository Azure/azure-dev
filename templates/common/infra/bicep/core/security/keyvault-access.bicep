param environmentName string
param location string = resourceGroup().location
param principalId string
param permissions object = { secrets: [ 'get', 'list' ] }

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var abbrs = loadJsonContent('../../abbreviations.json')

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: '${abbrs.keyVaultVaults}${resourceToken}'
}

resource keyVaultAccessPolicies 'Microsoft.KeyVault/vaults/accessPolicies@2022-07-01' = {
  parent: keyVault
  name: 'add'
  properties: {
    accessPolicies: [ {
        objectId: principalId
        tenantId: subscription().tenantId
        permissions: permissions
      } ]
  }
}
