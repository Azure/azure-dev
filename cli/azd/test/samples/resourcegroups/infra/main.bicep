targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@description('Primary location for all resources')
param location string

param resourceGroupName string
param includeEnvNameTag string = 'false'
param createMultipleResourceGroups string = 'false'

@description('A time to mark on created resource groups, so they can be cleaned up via an automated process.')
param deleteAfterTime string = dateTimeAdd(utcNow('o'), 'PT1H')

var tags = { 'azd-env-name': includeEnvNameTag == 'true' ? environmentName : '', DeleteAfter: deleteAfterTime }

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: resourceGroupName
  location: location
  tags: tags
}

resource rg2 'Microsoft.Resources/resourceGroups@2021-04-01' = if (createMultipleResourceGroups == 'true') {
  name: '${resourceGroupName}2'
  location: location
  tags: tags
}
