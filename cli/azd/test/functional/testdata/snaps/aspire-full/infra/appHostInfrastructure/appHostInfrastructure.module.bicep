@description('The location for the resource(s) to be deployed.')
param location string = resourceGroup().location

param userPrincipalId string

param tags object = { }

resource appHostInfrastructure_mi 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' = {
  name: take('appHostInfrastructure_mi-${uniqueString(resourceGroup().id)}', 128)
  location: location
  tags: tags
}

resource appHostInfrastructure_acr 'Microsoft.ContainerRegistry/registries@2025-04-01' = {
  name: take('appHostInfrastructureacr${uniqueString(resourceGroup().id)}', 50)
  location: location
  sku: {
    name: 'Basic'
  }
  tags: tags
}

resource appHostInfrastructure_acr_appHostInfrastructure_mi_AcrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(appHostInfrastructure_acr.id, appHostInfrastructure_mi.id, subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d'))
  properties: {
    principalId: appHostInfrastructure_mi.properties.principalId
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')
    principalType: 'ServicePrincipal'
  }
  scope: appHostInfrastructure_acr
}

resource appHostInfrastructure_law 'Microsoft.OperationalInsights/workspaces@2025-02-01' = {
  name: take('appHostInfrastructurelaw-${uniqueString(resourceGroup().id)}', 63)
  location: location
  properties: {
    sku: {
      name: 'PerGB2018'
    }
  }
  tags: tags
}

resource appHostInfrastructure 'Microsoft.App/managedEnvironments@2025-01-01' = {
  name: take('apphostinfrastructure${uniqueString(resourceGroup().id)}', 24)
  location: location
  properties: {
    appLogsConfiguration: {
      destination: 'log-analytics'
      logAnalyticsConfiguration: {
        customerId: appHostInfrastructure_law.properties.customerId
        sharedKey: appHostInfrastructure_law.listKeys().primarySharedKey
      }
    }
    workloadProfiles: [
      {
        name: 'consumption'
        workloadProfileType: 'Consumption'
      }
    ]
  }
  tags: tags
}

resource aspireDashboard 'Microsoft.App/managedEnvironments/dotNetComponents@2024-10-02-preview' = {
  name: 'aspire-dashboard'
  properties: {
    componentType: 'AspireDashboard'
  }
  parent: appHostInfrastructure
}

output AZURE_LOG_ANALYTICS_WORKSPACE_NAME string = appHostInfrastructure_law.name

output AZURE_LOG_ANALYTICS_WORKSPACE_ID string = appHostInfrastructure_law.id

output AZURE_CONTAINER_REGISTRY_NAME string = appHostInfrastructure_acr.name

output AZURE_CONTAINER_REGISTRY_ENDPOINT string = appHostInfrastructure_acr.properties.loginServer

output AZURE_CONTAINER_REGISTRY_MANAGED_IDENTITY_ID string = appHostInfrastructure_mi.id

output AZURE_CONTAINER_APPS_ENVIRONMENT_NAME string = appHostInfrastructure.name

output AZURE_CONTAINER_APPS_ENVIRONMENT_ID string = appHostInfrastructure.id

output AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN string = appHostInfrastructure.properties.defaultDomain
