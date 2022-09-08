param environmentName string
param location string = resourceGroup().location
param principalId string = ''

@secure()
param sqlAdminPassword string
@secure()
param appUserPassword string


module appServicePlan '../../../../../../common/infra/bicep/core/host/appserviceplan-sites.bicep' = {
  name: 'appserviceplan-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module web '../../../../../common/infra/core/application/web-node.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    applicationInsights
    appServicePlan
  ]
}

module api '../../../../../common/infra/core/application/api-dotnet.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
  }
  dependsOn: [
    applicationInsights
    keyVault
    appServicePlan
  ]
}

module apiSqlServerConfig '../../../../../../common/infra/bicep/core/host/appservice-config-sqlserver.bicep' = {
  name: 'api-sqlserver-config-resources'
  params: {
    appServiceName: api.outputs.NAME
    sqlConnectionStringKey: sqlServer.outputs.AZURE_SQL_CONNECTION_STRING_KEY
  }
}

module keyVault '../../../../../../common/infra/bicep/core/security/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

module sqlServer '../../../../../../common/infra/bicep/core/database/sqlserver.bicep' = {
  name: 'sqlserver-resources'
  params: {
    environmentName: environmentName
    location: location
    sqlAdminPassword: sqlAdminPassword
    appUserPassword: appUserPassword
    dbName: 'ToDo'
  }
  dependsOn: [
    keyVault
  ]
}

module logAnalytics '../../../../../../common/infra/bicep/core/monitor/loganalytics.bicep' = {
  name: 'loganalytics-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

module applicationInsights '../../../../../../common/infra/bicep/core/monitor/applicationinsights.bicep' = {
  name: 'applicationinsights-resources'
  params: {
    environmentName: environmentName
    location: location
    workspaceId: logAnalytics.outputs.AZURE_LOG_ANALYTICS_WORKSPACE_ID
  }
}


output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPLICATIONINSIGHTS_CONNECTION_STRING string = applicationInsights.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output WEB_URI string = web.outputs.URI
output API_URI string = api.outputs.URI
output AZURE_SQL_CONNECTION_STRING_KEY string = sqlServer.outputs.AZURE_SQL_CONNECTION_STRING_KEY
output KEYVAULT_NAME string = keyVault.name
