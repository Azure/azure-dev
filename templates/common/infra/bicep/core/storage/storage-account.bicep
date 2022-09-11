param environmentName string
param location string = resourceGroup().location

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../abbreviations.json')

resource storage 'Microsoft.Storage/storageAccounts@2021-09-01' = {
  name: '${abbrs.storageStorageAccounts}${resourceToken}'
  location: location
  tags: tags
  kind: 'StorageV2'
  sku: { name: 'Standard_LRS' }
  properties: {
    minimumTlsVersion: 'TLS1_2'
    allowBlobPublicAccess: false
    networkAcls: {
      bypass: 'AzureServices'
      defaultAction: 'Allow'
    }
  }
}

output NAME string = storage.name
