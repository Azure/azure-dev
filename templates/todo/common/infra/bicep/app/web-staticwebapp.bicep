param name string
param location string = resourceGroup().location
param tags object = {}

param serviceName string = 'web'

module web 'br/azd:bicep/staging/core/host/staticwebapp:v1.0' = {
  name: '${serviceName}-staticwebapp-module'
  params: {
    name: name
    location: location
    tags: union(tags, { 'azd-service-name': serviceName })
  }
}

output SERVICE_WEB_NAME string = web.outputs.name
output SERVICE_WEB_URI string = web.outputs.uri
