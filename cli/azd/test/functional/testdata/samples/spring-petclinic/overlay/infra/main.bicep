targetScope = 'subscription'

@minLength(1)
@maxLength(64)
param environmentName string

@minLength(1)
param location string

@allowed([
  'h2'
  'postgresql'
])
param databaseType string = 'h2'

@secure()
param databasePassword string = ''

param imageName string = 'mcr.microsoft.com/k8se/quickstart:latest'

var resourceGroupName = 'rg-${environmentName}'
var tags = {
  'azd-env-name': environmentName
}

resource resourceGroup 'Microsoft.Resources/resourceGroups@2024-03-01' = {
  name: resourceGroupName
  location: location
  tags: tags
}

module resources 'resources.bicep' = {
  name: 'spring-petclinic-resources'
  scope: resourceGroup
  params: {
    environmentName: environmentName
    location: location
    databaseType: databaseType
    databasePassword: databasePassword
    imageName: imageName
  }
}

output AZURE_RESOURCE_GROUP string = resourceGroup.name
output AZURE_CONTAINER_REGISTRY_NAME string = resources.outputs.containerRegistryName
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = resources.outputs.containerRegistryEndpoint
output AZURE_CONTAINER_ENVIRONMENT_NAME string = resources.outputs.containerAppsEnvironmentName
output SERVICE_PETCLINIC_IDENTITY_ID string = resources.outputs.identityId
output AZURE_CONTAINER_APP_ID string = resources.outputs.containerAppId
output AZURE_CONTAINER_APP_NAME string = resources.outputs.containerAppName
output AZURE_POSTGRES_SERVER_ID string = resources.outputs.postgresServerId
output AZURE_POSTGRES_SERVER_NAME string = resources.outputs.postgresServerName
output AZURE_POSTGRES_HOST string = resources.outputs.postgresHost
output AZURE_POSTGRES_DATABASE string = resources.outputs.postgresDatabase
output AZURE_POSTGRES_USER string = resources.outputs.postgresUser
