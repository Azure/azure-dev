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
    applicationInsightsName: monitoring.outputs.APPLICATIONINSIGHTS_NAME
    appServicePlanId: appServicePlan.outputs.AZURE_APP_SERVICE_PLAN_ID
  }
}

// The application backend
module api '../../../../../common/infra/bicep/app/api-appservice-dotnet.bicep' = {
  name: 'api-resources'
  params: {
    environmentName: environmentName
    location: location
    applicationInsightsName: monitoring.outputs.APPLICATIONINSIGHTS_NAME
    appServicePlanId: appServicePlan.outputs.AZURE_APP_SERVICE_PLAN_ID
    keyVaultName: keyVault.outputs.AZURE_KEY_VAULT_NAME
    allowedOrigins: [web.outputs.URI]
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
    keyVaultName: keyVault.outputs.AZURE_KEY_VAULT_NAME
  }
}

// Configure api to use sql
module apiSqlServerConfig '../../../../../../common/infra/bicep/core/host/appservice-config-sqlserver.bicep' = {
  name: 'api-sqlserver-config-resources'
  params: {
    appServiceName: api.outputs.NAME
    sqlConnectionStringKey: sqlServer.outputs.AZURE_SQL_CONNECTION_STRING_KEY
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

output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output WEB_URI string = web.outputs.URI
output API_URI string = api.outputs.URI
output AZURE_SQL_CONNECTION_STRING_KEY string = sqlServer.outputs.AZURE_SQL_CONNECTION_STRING_KEY
output KEYVAULT_NAME string = keyVault.name
