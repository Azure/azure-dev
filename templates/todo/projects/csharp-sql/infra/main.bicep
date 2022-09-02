targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param name string

@minLength(1)
@description('Primary location for all resources')
param location string

@description('User identity to use as resource administrator')
param principalId string = ''

@secure()
@description('SQL Server administrator password')
param sqlAdminPassword string

@secure()
@description('Application user password')
param appUserPassword string

var resourceToken = toLower(uniqueString(subscription().id, name, location))
var tags = { 'azd-env-name': name }
var abbrs = loadJsonContent('../../../../common/infra/bicep/abbreviations.json')

resource rg 'Microsoft.Resources/resourceGroups@2021-04-01' = {
  name: '${abbrs.resourcesResourceGroups}${name}'
  location: location
  tags: tags
}

module resources 'resources.bicep' = {
  name: 'resources'
  scope: rg
  params: {
    location: location
    principalId: principalId
    resourceToken: resourceToken
    tags: tags
    sqlAdminPassword: sqlAdminPassword
    appUserPassword: appUserPassword
  }
}

output APPLICATIONINSIGHTS_CONNECTION_STRING string = resources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output REACT_APP_WEB_BASE_URL string = resources.outputs.WEB_URI
output REACT_APP_API_BASE_URL string = resources.outputs.API_URI
output REACT_APP_APPLICATIONINSIGHTS_CONNECTION_STRING string = resources.outputs.APPLICATIONINSIGHTS_CONNECTION_STRING
output AZURE_LOCATION string = location
output AZURE_KEY_VAULT_ENDPOINT string = resources.outputs.AZURE_KEY_VAULT_ENDPOINT
output AZURE_SQL_CONNECTION_STRING_KEY string = resources.outputs.AZURE_SQL_CONNECTION_STRING_KEY
output KEYVAULT_NAME string = resources.outputs.KEYVAULT_NAME
