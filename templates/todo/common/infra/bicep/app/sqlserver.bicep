param environmentName string
param location string = resourceGroup().location

param databaseName string = 'ToDo'
param keyVaultName string

@secure()
param sqlAdminPassword string
@secure()
param appUserPassword string

module sqlServer '../../../../../common/infra/bicep/core/database/sqlserver.bicep' = {
  name: 'sqlserver'
  params: {
    environmentName: environmentName
    location: location
    dbName: databaseName
    keyVaultName: keyVaultName
    sqlAdminPassword: sqlAdminPassword
    appUserPassword: appUserPassword
  }
}

output sqlConnectionStringKey string = sqlServer.outputs.sqlConnectionStringKey
output sqlDatabaseName string = databaseName
