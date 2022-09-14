param environmentName string
param location string = resourceGroup().location
param principalId string = ''

@secure()
param sqlAdminPassword string

@secure()
param appUserPassword string

// The application frontend
module web '../../../../../common/infra/bicep/app/web-appservice.bicep' = {
  name: 'web-resources'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    appServicePlanId: appServicePlan.outputs.appServicePlanId
  }
}

// The application backend
module api '../../../../../common/infra/bicep/app/api-appservice-dotnet.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    appServicePlanId: appServicePlan.outputs.appServicePlanId
    keyVaultName: keyVault.outputs.keyVaultName
    allowedOrigins: [ web.outputs.uri ]
  }
}
// The application database
module sqlServer '../../../../../common/infra/bicep/app/sql.bicep' = {
  name: 'sql-resources'
  params: {
    environmentName: environmentName
    location: location
    sqlAdminPassword: sqlAdminPassword
    appUserPassword: appUserPassword
    keyVaultName: keyVault.outputs.keyVaultName
  }
}

// Configure api to use sql
module apiSqlServerConfig '../../../../../../common/infra/bicep/core/host/appservice-config-sqlserver.bicep' = {
  name: 'api-sqlserver-config-resources'
  params: {
    appServiceName: api.outputs.name
    sqlConnectionStringKey: sqlServer.outputs.sqlConnectionStringKey
  }
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan '../../../../../../common/infra/bicep/core/host/appserviceplan-sites.bicep' = {
  name: 'appserviceplan-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

// Store secrets in a keyvault
module keyVault '../../../../../../common/infra/bicep/core/security/keyvault.bicep' = {
  name: 'keyvault-resources'
  params: {
    environmentName: environmentName
    location: location
    principalId: principalId
  }
}

// Monitor application with Azure Monitor
module monitoring '../../../../../../common/infra/bicep/core/monitor/monitoring.bicep' = {
  name: 'monitoring-resources'
  params: {
    environmentName: environmentName
    location: location
  }
}

output apiUri string = api.outputs.uri
output applicationInsightsConnectionString string = monitoring.outputs.applicationInsightsConnectionString
output keyVaultEndpoint string = keyVault.outputs.keyVaultEndpoint
output keyVaultName string = keyVault.name
output sqlConnectionStringKey string = sqlServer.outputs.sqlConnectionStringKey
output webUri string = web.outputs.uri
