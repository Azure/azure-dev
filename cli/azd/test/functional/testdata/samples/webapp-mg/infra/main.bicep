targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@description('Primary location for all resources')
param location string

@description('A time to mark on created resource groups, so they can be cleaned up via an automated process.')
param deleteAfterTime string = dateTimeAdd(utcNow('o'), 'PT1H')

var tags = { 'azd-env-name': environmentName, DeleteAfter: deleteAfterTime }

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: tags
}

resource rg2 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg2-${environmentName}'
  location: location
  tags: tags
}

module resources 'resources.bicep' = {
  name: 'resources'
  scope: rg
  params: {
    environmentName: environmentName
    location: location
    serviceName: 'web'
  }
}

module resources2 'resources.bicep' = {
  name: 'resources2'
  scope: rg2
  params: {
    environmentName: environmentName
    location: location
    serviceName: 'web2'
  }
}

output WEBSITE_URL string = resources.outputs.WEBSITE_URL
output WEBSITE_URL_2 string = resources2.outputs.WEBSITE_URL

output SERVICE_WEB_RESOURCE_GROUP string = rg.name
output SERVICE_WEB2_RESOURCE_GROUP string = rg2.name
