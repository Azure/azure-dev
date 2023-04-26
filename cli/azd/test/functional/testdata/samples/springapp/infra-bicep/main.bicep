targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@description('Primary location for all resources')
param location string

@description('Relative Path of ASA Jar')
param relativePath string

@description('A time to mark on created resource groups, so they can be cleaned up via an automated process.')
param deleteAfterTime string = dateTimeAdd(utcNow('o'), 'PT1H')

var tags = { 'azd-env-name': environmentName, DeleteAfter: deleteAfterTime }

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: tags
}

module springapps 'core/host/springapps.bicep' = {
  name: 'springapps'
  scope: rg
  params: {
    environmentName: environmentName
    location: location
    relativePath: relativePath
  }
}
