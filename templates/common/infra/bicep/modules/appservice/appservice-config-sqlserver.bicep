param resourceName string
param serviceName string
param sqlConnectionStringKey string

module appServiceConfigSqlServerSettings 'appservice-config-union.bicep' = {
  name: 'appservice-config-sqlserver-settings-${serviceName}'
  params: {
    resourceName: resourceName
    configName: 'appsettings'
    currentConfigProperties: list(resourceId('Microsoft.Web/sites/config',  resourceName,  'appsettings'),  '2022-03-01').properties
    additionalConfigProperties:    {
      AZURE_SQL_CONNECTION_STRING_KEY: sqlConnectionStringKey
    }
  }
}
