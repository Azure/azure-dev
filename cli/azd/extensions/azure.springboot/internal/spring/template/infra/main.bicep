targetScope = 'subscription'

param environmentName string
param location string
param serviceName string
param port int
param profiles array
@allowed(['none', 'mysql', 'postgresql'])
param databaseEngine string
param databaseName string = ''
@allowed(['password', 'managedIdentity'])
param databaseAuth string
@secure()
param databasePassword string = ''
param administratorObjectId string = ''
param administratorName string = ''
param bootstrapIp string = ''
param appImage string = 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'
param environmentBindings bool
param jdbcOptions string = ''
param driver string = ''
param embeddedUrl string = ''
param embeddedUrlConfigured bool
param embeddedDriver string = ''

resource group 'Microsoft.Resources/resourceGroups@2024-03-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: { 'azd-env-name': environmentName }
}

module database 'database.bicep' = if (databaseEngine != 'none') {
  name: 'database'
  scope: group
  params: {
    location: location
    engine: databaseEngine
    databaseName: databaseName
    authentication: databaseAuth
    password: databasePassword
    administratorObjectId: administratorObjectId
    administratorName: administratorName
    bootstrapIp: bootstrapIp
  }
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
    postgresServerName: databaseEngine == 'postgresql' ? database!.outputs.serverName : ''
    mysqlServerName: databaseEngine == 'mysql' ? database!.outputs.serverName : ''
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
    port: port
    profiles: profiles
    appImage: appImage
    engine: databaseEngine
    databaseName: databaseName
    host: databaseEngine != 'none' ? database!.outputs.host : ''
    username: databaseAuth == 'managedIdentity' ? 'azd_app' : 'azdadmin'
    password: databasePassword
    authentication: databaseAuth
    environmentBindings: environmentBindings
    jdbcOptions: jdbcOptions
    driver: driver
    embeddedUrl: embeddedUrl
    embeddedUrlConfigured: embeddedUrlConfigured
    embeddedDriver: embeddedDriver
  }
  dependsOn: [access]
}

output AZURE_RESOURCE_GROUP string = group.name
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = hosting.outputs.registryEndpoint
output SPRING_CONTAINER_APP_ENVIRONMENT_ID string = hosting.outputs.environmentId
output SPRING_APPLICATION_ID string = application.outputs.appId
output SPRING_APPLICATION_PRINCIPAL_ID string = bootstrap.outputs.principalId
output SPRING_APPLICATION_ENDPOINT string = application.outputs.endpoint
output SPRING_DATABASE_HOST string = databaseEngine != 'none' ? database!.outputs.host : ''
output SPRING_DATABASE_NAME string = databaseName
output SPRING_DATABASE_SERVER_NAME string = databaseEngine != 'none' ? database!.outputs.serverName : ''
