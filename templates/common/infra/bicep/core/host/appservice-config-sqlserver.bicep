param appServiceName string
param sqlConnectionStringKey string

module appServiceConfigSqlServerSettings 'appservice-config-union.bicep' = {
  name: 'appservice-config-sqlserver-settings-${appServiceName}'
  params: {
    appServiceName: appServiceName
    configName: 'appsettings'
    currentConfigProperties: list(resourceId('Microsoft.Web/sites/config', appServiceName, 'appsettings'), '2022-03-01').properties
    additionalConfigProperties: {
      sqlConnectionStringKey: sqlConnectionStringKey
    }
  }
}
