targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@description('Primary location for all resources')
param location string

@description('A time to mark on created resource groups, so they can be cleaned up via an automated process.')
param deleteAfterTime string = dateTimeAdd(utcNow('o'), 'PT1H')

@description('Test parameter for int-typed values.')
param intTagValue int

@description('Test parameter for bool-typed values.')
param boolTagValue bool

var tags = {
  'azd-env-name': environmentName
  DeleteAfter: deleteAfterTime
  IntTag: string(intTagValue)
  BoolTag: string(boolTagValue)
}

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

// test cases for all supported types
output STRING string = 'abc'
output BOOL bool = true
output INT int = 1234
output ARRAY array = [true, 'abc', 1234]
output ARRAY_INT array = [1,2,3]
output ARRAY_STRING array = ['elem1', 'elem2', 'elem3']
output OBJECT object = {
  foo : 'bar'
  inner: {
    foo: 'bar'
  }
  array: [true, 'abc', 1234]
}
