param name string
param location string

resource storage 'Microsoft.Storage/storageAccounts@2021-09-01' = {
  kind: 'StorageV2'
  location: location
  name: '${replace(name, '-', '')}store'
  sku: {
    name: 'Standard_LRS'
  }
}

output AZURE_STORAGE_ACCOUNT_ID string = storage.id
output AZURE_STORAGE_ACCOUNT_NAME string = storage.name
