param location string
param resourceToken string
param tags object

resource storage 'Microsoft.Storage/storageAccounts@2021-09-01' = {
  name: 'st${resourceToken}'
  location: location
  tags: tags
  kind: 'StorageV2'
  sku: {
    name: 'Standard_LRS'
  }
}

output AZURE_STORAGE_ACCOUNT_ID string = storage.id
output AZURE_STORAGE_ACCOUNT_NAME string = storage.name
