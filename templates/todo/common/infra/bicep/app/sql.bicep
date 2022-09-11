param environmentName string
param location string = resourceGroup().location
param keyVaultName string
param databaseName string = 'ToDo'

@secure()
param sqlAdminPassword string
@secure()
param appUserPassword string

module sqlServer '../../../../../common/infra/bicep/core/database/sqlserver.bicep' = {
  name: 'todo-sqlserver-resources'
  params: {
    environmentName: environmentName
    location: location
    sqlAdminPassword: sqlAdminPassword
    appUserPassword: appUserPassword
    dbName: databaseName
    keyVaultName: keyVaultName
  }
}

output AZURE_SQL_DATABASE_NAME string = databaseName
output AZURE_SQL_CONNECTION_STRING_KEY string = sqlServer.outputs.AZURE_SQL_CONNECTION_STRING_KEY
