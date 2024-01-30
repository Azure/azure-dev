param environmentName string
param location string = resourceGroup().location
param principalId string

var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))

resource storage 'Microsoft.Storage/storageAccounts@2022-05-01' = {
  name: 'st${resourceToken}'
  location: location
  tags: tags
  kind: 'StorageV2'
  sku: {
    name: 'Standard_LRS'
  }
}

var storageBlobDataContributorRole = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'ba92f5b4-2d11-453d-a403-e96b0029c9fe')

resource role 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(storage.id, storageBlobDataContributorRole)
  scope: storage
  properties: {
    principalId: principalId
    roleDefinitionId: storageBlobDataContributorRole
  }
}

output AZURE_STORAGE_ACCOUNT_NAME string = storage.name
