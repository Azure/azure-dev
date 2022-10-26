targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@description('Primary location for all resources')
param location string

@description('A time to mark on created resource groups, so they can be cleaned up via an automated process.')
param deleteAfterTime string = dateTimeAdd(utcNow('o'), 'PT1H')

@description('A simple sentinel that can be used to change behavior between PROD and non-PROD')
param isProd string = 'false'

var tags = { 'azd-env-name': environmentName, DeleteAfter: deleteAfterTime }
var isProdBool = isProd == 'true' ? true : false

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: tags
}

module resources 'resources.bicep' = {
  name: 'resources'
  scope: rg
  params: {
    environmentName: environmentName
    location: location
  }
}

output AZURE_STORAGE_ACCOUNT_ID string = resources.outputs.AZURE_STORAGE_ACCOUNT_ID
output AZURE_STORAGE_ACCOUNT_NAME string = resources.outputs.AZURE_STORAGE_ACCOUNT_NAME
// test support for non string output
output AZURE_STORAGE_IS_PROD bool = isProdBool
