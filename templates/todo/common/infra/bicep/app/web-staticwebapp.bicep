param environmentName string
param location string = resourceGroup().location
param serviceName string = 'web'

module web '../../../../../common/infra/bicep/core/host/staticwebapp.bicep' = {
  name: 'web-staticwebapp-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
  }
}

output NAME string = web.outputs.NAME
output URI string = web.outputs.URI
