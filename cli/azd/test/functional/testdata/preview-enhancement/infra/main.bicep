targetScope = 'resourceGroup'

@description('The location for resources')
param location string = resourceGroup().location

@description('The environment name')
param environmentName string

resource storageAccount 'Microsoft.Storage/storageAccounts@2023-01-01' = {
  name: 'st${uniqueString(resourceGroup().id)}'
  location: location
  sku: {
    name: 'Standard_LRS'
  }
  kind: 'StorageV2'
  properties: {
    minimumTlsVersion: 'TLS1_2'
    supportsHttpsTrafficOnly: true
  }
  tags: {
    environment: environmentName
  }
}

output storageAccountName string = storageAccount.name
