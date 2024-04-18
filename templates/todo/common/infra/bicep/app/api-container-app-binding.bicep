param name string
param location string = resourceGroup().location
param tags object = {}

param identityName string
param appConfigName string
param containerAppsEnvironmentName string
param containerRegistryName string
param containerRegistryHostSuffix string
param keyVaultName string
param serviceName string = 'api'
param corsAcaUrl string
param exists bool

resource apiIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: identityName
}

resource appConfiguration 'Microsoft.AppConfiguration/configurationStores@2023-03-01' existing = {
  name: appConfigName
}

// Give the API access to KeyVault
module apiKeyVaultAccess '../../../../../common/infra/bicep/core/security/keyvault-access.bicep' = {
  name: 'api-keyvault-access'
  params: {
    keyVaultName: keyVaultName
    principalId: apiIdentity.properties.principalId
  }
}

// Give the API access to App Configuration. Role assignment after both created otherwise mutual dependency.
module appConfigurationAccess '../../../../../common/infra/bicep/core/security/configstore-access.bicep' = {
  name: 'app-configuration-access'
  params: {
    configStoreName: appConfigName
    principalId: apiIdentity.properties.principalId
  }
}

module app '../../../../../common/infra/bicep/core/host/container-app-upsert.bicep' = {
  name: '${serviceName}-container-app'
  dependsOn: [ apiKeyVaultAccess ]
  params: {
    name: name
    location: location
    tags: union(tags, { 'azd-service-name': serviceName })
    identityType: 'UserAssigned'
    identityName: apiIdentity.name
    exists: exists
    containerAppsEnvironmentName: containerAppsEnvironmentName
    containerRegistryName: containerRegistryName
    containerRegistryHostSuffix: containerRegistryHostSuffix
    containerCpuCoreCount: '1.0'
    containerMemory: '2.0Gi'
    env: [
      {
        name: 'AZURE_CLIENT_ID'
        value: apiIdentity.properties.clientId
      }
      {
        name: 'AZURE_APPCONFIGURATION_ENDPOINT'
        value: appConfiguration.properties.endpoint
      }
      {
        name: 'NODE_ENV'
        value: 'production'
      }
      {
        name: 'API_ALLOW_ORIGINS'
        value: corsAcaUrl
      }
    ]
    targetPort: 3100
  }
}

output SERVICE_API_IDENTITY_PRINCIPAL_ID string = apiIdentity.properties.principalId
output SERVICE_API_NAME string = app.outputs.name
output SERVICE_API_URI string = app.outputs.uri
output SERVICE_API_IMAGE_NAME string = app.outputs.imageName
