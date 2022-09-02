param resourceName string
param serviceName string
param cosmosDatabaseName string = ''
param cosmosConnectionStringKey string = ''
param cosmosEndpoint string = ''

module websiteConfigCosmosSettings 'website-config-union.bicep' = {
  name: 'website-config-cosmos-settings-${serviceName}'
  params: {
    resourceName: resourceName
    configName: 'appsettings'
    currentConfigProperties: list(resourceId('Microsoft.Web/sites/config',  resourceName,  'appsettings'),  '2022-03-01').properties
    additionalConfigProperties:    {
      AZURE_COSMOS_ENDPOINT: cosmosEndpoint
      AZURE_COSMOS_CONNECTION_STRING_KEY: cosmosConnectionStringKey
      AZURE_COSMOS_DATABASE_NAME: cosmosDatabaseName
    }
  }
}
