param eventHubsNamespaceName string
param connectionStringSecretName string
param keyVaultName string

resource eventHubsNamespace 'Microsoft.EventHub/namespaces@2024-01-01' existing = {
  name: eventHubsNamespaceName
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: keyVaultName
}

resource connectionStringSecret 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  name: connectionStringSecretName
  parent: keyVault
  properties: {
    value: listKeys(concat(resourceId('Microsoft.EventHub/namespaces', eventHubsNamespaceName), '/AuthorizationRules/RootManageSharedAccessKey'), eventHubsNamespace.apiVersion).primaryConnectionString
  }
}

output keyVaultUrl string = 'https://${keyVaultName}${environment().suffixes.keyvaultDns}/secrets/${connectionStringSecretName}'
