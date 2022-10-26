param name string
param location string = resourceGroup().location
param tags object = {}

param serviceName string = 'web'

module web '../../../../../common/infra/bicep/core/host/staticwebapp.bicep' = {
  name: '${serviceName}-staticwebapp-module'
  params: {
    name: name
    location: location
    tags: tags
  }
}

output WEB_NAME string = web.outputs.name
output WEB_URI string = web.outputs.uri
