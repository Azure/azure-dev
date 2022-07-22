targetScope = 'subscription'

@minLength(1)
@maxLength(17)
@description('Prefix for all resources, i.e. {basename}storage')
param basename string

@minLength(1)
@description('Primary location for all resources')
param location string

@description('Id of the user or app to assign application roles')
param principalId string = ''

resource rg 'Microsoft.Resources/resourceGroups@2020-06-01' = {
  name: '${basename}rg'
  location: location
}

module resources './resources.bicep' = {
  name: '${rg.name}-resources'
  scope: rg
  params: {
    basename: toLower(basename)
    location: location
    principalId: principalId
  }
}

output COSMOS_CONNECTION_STRING_KEY string = resources.outputs.COSMOS_CONNECTION_STRING_KEY
output COSMOS_DATABASE_NAME string = resources.outputs.COSMOS_DATABASE_NAME
output AZURE_KEY_VAULT_ENDPOINT string = resources.outputs.AZURE_KEY_VAULT_ENDPOINT
output APPINSIGHTS_INSTRUMENTATIONKEY string = resources.outputs.APPINSIGHTS_INSTRUMENTATIONKEY
output APPINSIGHTS_NAME string = resources.outputs.APPINSIGHTS_NAME
output APPINSIGHTS_DASHBOARD_NAME string = resources.outputs.APPINSIGHTS_DASHBOARD_NAME
output REACT_APP_API_BASE_URL string = resources.outputs.API_URI
output REACT_APP_APPINSIGHTS_INSTRUMENTATIONKEY string = resources.outputs.APPINSIGHTS_INSTRUMENTATIONKEY
output LOCATION string = location
