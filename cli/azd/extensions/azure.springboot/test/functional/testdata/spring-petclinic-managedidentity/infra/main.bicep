targetScope = 'subscription'

@minLength(1)
@maxLength(64)
param environmentName string

@minLength(1)
param location string

@secure()
@discriminator('type')
param databaseAuthentication {
  type: 'managedidentity'
  @minLength(1)
  administratorName: string
  @minLength(1)
  administratorPrincipalId: string
  administratorPassword: ''
  applicationPassword: ''
} | {
  type: 'password'
  administratorName: '' | 'petclinicadmin'
  administratorPrincipalId: ''
  @secure()
  @minLength(8)
  @maxLength(128)
  administratorPassword: string
  @secure()
  @minLength(8)
  @maxLength(128)
  applicationPassword: string
}

@minLength(1)
@maxLength(63)
param databaseName string = 'petclinic'

@minLength(1)
@maxLength(63)
param databaseUsername string = 'petclinic_app'

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
    databaseAuthentication: databaseAuthentication
    databaseName: databaseName
    databaseUsername: databaseUsername
    imageName: imageName
  }
}

output AZURE_RESOURCE_GROUP string = resourceGroup.name
output AZURE_CONTAINER_REGISTRY_NAME string = resources.outputs.containerRegistryName
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = resources.outputs.containerRegistryEndpoint
output AZURE_CONTAINER_ENVIRONMENT_NAME string = resources.outputs.containerAppsEnvironmentName
output SERVICE_PETCLINIC_IDENTITY_ID string = resources.outputs.identityId
output SERVICE_PETCLINIC_IDENTITY_CLIENT_ID string = resources.outputs.identityClientId
output SERVICE_PETCLINIC_IDENTITY_PRINCIPAL_ID string = resources.outputs.identityPrincipalId
output AZURE_CONTAINER_APP_ID string = resources.outputs.containerAppId
output AZURE_CONTAINER_APP_NAME string = resources.outputs.containerAppName
output AZURE_POSTGRES_SERVER_ID string = resources.outputs.postgresServerId
output AZURE_POSTGRES_SERVER_NAME string = resources.outputs.postgresServerName
output AZURE_POSTGRES_HOST string = resources.outputs.postgresHost
output AZURE_POSTGRES_DATABASE string = resources.outputs.postgresDatabase
output AZURE_POSTGRES_USER string = resources.outputs.postgresUser
output AZURE_POSTGRES_ADMINISTRATOR string = resources.outputs.postgresAdministrator
