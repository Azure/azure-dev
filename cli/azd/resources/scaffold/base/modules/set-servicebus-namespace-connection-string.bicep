param serviceBusNamespaceName string
param connectionStringSecretName string
param keyVaultName string

resource serviceBusNamespace 'Microsoft.ServiceBus/namespaces@2022-10-01-preview' existing = {
  name: serviceBusNamespaceName
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: keyVaultName
}

resource serviceBusConnectionStringSecret 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  name: connectionStringSecretName
  parent: keyVault
  properties: {
    value: listKeys(concat(resourceId('Microsoft.ServiceBus/namespaces', serviceBusNamespaceName), '/AuthorizationRules/RootManageSharedAccessKey'), serviceBusNamespace.apiVersion).primaryConnectionString
  }
}
