targetScope = 'subscription'

@minLength(1)
@maxLength(64)
param environmentName string
param location string

@description('Database size: small = 1 vCore / 2 GiB; larger = 2 vCores / 4 GiB and higher cost. Both use 32 GiB storage.')
@allowed(['small', 'larger'])
param databaseSize string

@minLength(1)
param databaseAdministratorObjectId string
@minLength(1)
param databaseAdministratorName string

var serviceName = 'spring-petclinic-rest-app'

resource group 'Microsoft.Resources/resourceGroups@2024-03-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: { 'azd-env-name': environmentName }
}

module hosting 'hosting.bicep' = {
  name: 'hosting'
  scope: group
  params: { location: location }
}

module bootstrap 'bootstrap.bicep' = {
  name: 'bootstrap'
  scope: group
  params: {
    location: location
    serviceName: serviceName
    environmentId: hosting.outputs.environmentId
  }
}

module access 'access.bicep' = {
  name: 'access'
  scope: group
  params: {
    registryName: hosting.outputs.registryName
    principalId: bootstrap.outputs.principalId
  }
}

module database 'database.bicep' = {
  name: 'database'
  scope: group
  params: {
    location: location
    size: databaseSize
    administratorObjectId: databaseAdministratorObjectId
    administratorName: databaseAdministratorName
    outboundIps: bootstrap.outputs.outboundIps
  }
}

module application 'application.bicep' = {
  name: 'application'
  scope: group
  params: {
    location: location
    serviceName: serviceName
    environmentId: hosting.outputs.environmentId
    registryEndpoint: hosting.outputs.registryEndpoint
    databaseHost: database.outputs.host
    appImage: bootstrap.outputs.image
  }
  dependsOn: [access]
}

output AZURE_RESOURCE_GROUP string = group.name
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = hosting.outputs.registryEndpoint
output CONTAINER_APP_ENVIRONMENT_ID string = hosting.outputs.environmentId
output APPLICATION_PRINCIPAL_ID string = bootstrap.outputs.principalId
output DATABASE_HOST string = database.outputs.host
output DATABASE_SERVER_ID string = database.outputs.serverId
output DATABASE_OUTBOUND_IPS string = string(bootstrap.outputs.outboundIps)
output DATABASE_SIZE string = databaseSize
output SERVICE_SPRING_PETCLINIC_REST_APP_ENDPOINT_URL string = application.outputs.SERVICE_SPRING_PETCLINIC_REST_APP_ENDPOINT_URL
