param appServiceName string
param cosmosConnectionStringKey string = ''
param cosmosDatabaseName string = ''
param cosmosEndpoint string = ''

module appServiceConfigCosmosSettings 'appservice-config-union.bicep' = {
  name: 'appservice-config-cosmos-settings-${appServiceName}'
  params: {
    appServiceName: appServiceName
    configName: 'appsettings'
    currentConfigProperties: list(resourceId('Microsoft.Web/sites/config', appServiceName, 'appsettings'), '2022-03-01').properties
    additionalConfigProperties: {
      cosmosConnectionStringKey: cosmosConnectionStringKey
      cosmosDatabaseName: cosmosDatabaseName
      cosmosEndpoint: cosmosEndpoint
    }
  }
}
