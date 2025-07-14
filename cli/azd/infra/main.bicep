targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@minLength(1)
@description('Primary location for all resources')
param location string

param appName string = ''
param openAIName string = ''
param applicationInsightsDashboardName string = ''
param applicationInsightsName string = ''
param appServicePlanName string = ''
@secure()
param keyVaultName string = ''
param logAnalyticsName string = ''
param resourceGroupName string = ''
param managedIdentityName string = ''

@description('Id of the user or app to assign application roles')
param principalId string = ''
/*param managedIdentityName string = ''

// Create a User-Assigned Managed Identity
resource userAssignedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2021-09-30' = {
  name: managedIdentityName
  location: location
}
*/

var abbrs = loadJsonContent('./abbreviations.json')
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }

// Organize resources in a resource group
resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: !empty(resourceGroupName) ? resourceGroupName : '${abbrs.resourcesResourceGroups}${environmentName}'
  location: location
  tags: tags
}

// Create an App Service Plan to group applications under the same payment plan and SKU
module appServicePlan './core/host/appserviceplan.bicep' = {
  name: 'appserviceplan'
  scope: rg
  params: {
    name: !empty(appServicePlanName) ? appServicePlanName : '${abbrs.webServerFarms}${resourceToken}'
    location: location
    tags: tags
    sku: {
      name: 'P0v3'
    }
  }
}

// Monitor application with Azure Monitor
module monitoring './core/monitor/monitoring.bicep' = {
  name: 'monitoring'
  scope: rg
  params: {
    location: location
    tags: tags
    logAnalyticsName: !empty(logAnalyticsName) ? logAnalyticsName : '${abbrs.operationalInsightsWorkspaces}${resourceToken}'
    applicationInsightsName: !empty(applicationInsightsName) ? applicationInsightsName : '${abbrs.insightsComponents}${resourceToken}'
    applicationInsightsDashboardName: !empty(applicationInsightsDashboardName) ? applicationInsightsDashboardName : '${abbrs.portalDashboards}${resourceToken}'
  }
}

// Store secrets in a keyvault
module keyVault './core/security/keyvault.bicep' = {
  name: 'keyvault'
  scope: rg
  params: {
    name: !empty(keyVaultName) ? keyVaultName : '${abbrs.keyVaultVaults}${resourceToken}'
    location: location
    tags: tags
    principalId: principalId
  }
}

// Store secrets in a keyvault
module managedidentity './core/security/managedidentity.bicep' = {
  name: 'managedidentity'
  scope: rg
  params: {
    identityName: !empty(managedIdentityName) ? managedIdentityName : '${abbrs.keyVaultVaults}${resourceToken}-mi'
    location: location
    tags: tags
  }
}

// The application database
/*module mySql './core/database/mysql/mysql-db.bicep' = {
  name: 'mysql-db'
  scope: rg
  params: {
    location: location
    tags: tags
    serverName: !empty(mySqlServerName) ? mySqlServerName : '${abbrs.dBforMySQLServers}${resourceToken}'
    serverAdminName: mySqlServerAdminName
    serverAdminPassword: mySqlServerAdminPassword
    databaseName: !empty(mySqlDatabaseName) ? mySqlDatabaseName : 'petclinic'
    keyVaultName: keyVault.outputs.name
  }
}*/

// The application backend
module app './app/app.bicep' = {
  name: 'app'
  scope: rg
  params: {
    name: !empty(appName) ? appName : '${abbrs.webSitesAppService}petclinic-${resourceToken}'
    location: location
    tags: tags
    managedIdentityID: managedidentity.outputs.userAssignedIdentityID
    managedIdentityName: !empty(appName) ? appName : '${abbrs.webSitesAppService}petclinic-${resourceToken}-mi'
    applicationInsightsName: monitoring.outputs.applicationInsightsName
    appServicePlanId: appServicePlan.outputs.id
    keyVaultName: keyVault.outputs.name
    appSettings: {
      APPLICATIONINSIGHTS_CONNECTION_STRING: monitoring.outputs.applicationInsightsConnectionString
      AZURE_KEY_VAULT_ENDPOINT: keyVault.outputs.endpoint
      SPRING_PROFILES_ACTIVE: 'azure,h2'
      OPENAI_DEPLOYMENT_NAME: openAi1.outputs.openaiendpoint
      AZURE_CLIENT_ID: managedidentity.outputs.clientId
      //MYSQL_URL: mySql.outputs.endpoint
      //MYSQL_USER: mySqlServerAdminName
    }
  }
}

module roleAssignments './core/security/roleassignments.bicep' = {
  scope: rg
  name: 'role-assignments-mi'
  params: {
    managedIdentityID: managedidentity.outputs.userAssignedIdentityID
    managedIdentityPrincipalID: managedidentity.outputs.principalId
  }
}

// Give the API access to KeyVault
/*module appKeyVaultAccess './core/security/keyvault-access.bicep' = {
  name: 'app-keyvault-access'
  scope: rg
  params: {
    keyVaultName: keyVault.outputs.name
    principalId: app.outputs.APP_IDENTITY_PRINCIPAL_ID
  }
}*/


@description('Location for the OpenAI resource group')
@allowed(['australiaeast', 'canadaeast', 'eastus', 'eastus2', 'francecentral', 'japaneast', 'northcentralus', 'swedencentral', 'switzerlandnorth', 'uksouth', 'westeurope'])
@metadata({
  azd: {
    type: 'location'
  }
})
param openAiLocation string // Set in main.parameters.json

// FIRST: creating Azure Cognitive Services account for OpenAI
module openAi1 'core/openai/openai.bicep' = {
  name: 'openai1'
  scope: rg
  params: {
    name: !empty(openAIName) ? openAIName : '${abbrs.webSitesAppService}petclinic-${resourceToken}-oai'
    location: openAiLocation
    tags: tags
    sku: {
      name: 'S0'
    }
    userAssignedIdentityID: managedidentity.outputs.userAssignedIdentityID
    disableLocalAuth: true
    deployments: [
      {
        name: 'gpt-4o-model'
        raiPolicyName: 'Microsoft.Default'
        model: {
          format: 'OpenAI'
          name: 'gpt-4o'
        }
        sku: {
          name: 'Standard'
          capacity: 2
        }
      }
    ]
  }
}


// Roles

// Assign the role (to Cog service account 1), a role entry is added to Cognitive Services account with the following args: 
// - roleDefinitionId (Cognitive Service User), 
// - principalId (app service instance) 
// - scope (Cognitive Services account)
/*module openAi1RoleAppService 'core/security/role.bicep' = {
  scope: rg
  name: 'openai1-role-appservice'
  params: {
    principalId: managedidentity.outputs.principalId
    // Cognitive Services OpenAI User
    roleDefinitionId: '5e0bd9bd-7b93-4f28-af87-19fc36ad61bd'
    principalType: 'ServicePrincipal'
  }
}*/



output AZURE_RESOURCE_GROUP string = rg.name
output DEPLOYMENT_ID string = 'gpt-4o-model'
output OPENAI_DEPLOYMENT_NAME string = openAi1.outputs.openaiendpoint
// Data outputs
//output MYSQL_URL string = mySql.outputs.endpoint
//output MYSQL_USER string = mySqlServerAdminName
output WEBSITES_PORT int = 8080

// App outputs
output APPLICATIONINSIGHTS_CONNECTION_STRING string = monitoring.outputs.applicationInsightsConnectionString
output AZURE_KEY_VAULT_ENDPOINT string = keyVault.outputs.endpoint
output AZURE_KEY_VAULT_NAME string = keyVault.outputs.name
output AZURE_LOCATION string = location
output AZURE_TENANT_ID string = tenant().tenantId
output SPRING_PROFILES_ACTIVE string = 'azure,h2'
output AZURE_CLIENT_ID string = managedidentity.outputs.clientId
