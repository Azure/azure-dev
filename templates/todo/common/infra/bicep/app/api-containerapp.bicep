param environmentName string
param location string = resourceGroup().location
param serviceName string = 'api'
param imageName string

module api '../../../../../common/infra/bicep/core/host/container-app.bicep' = {
  name: 'api-containerapp-${serviceName}'
  params: {
    environmentName: environmentName
    location: location
    serviceName: serviceName
    useKeyVault: true
    targetPort: 3100
    imageName: imageName
  }
}

output NAME string = api.outputs.NAME
output URI string = api.outputs.URI
output IDENTITY_PRINCIPAL_ID string = api.outputs.IDENTITY_PRINCIPAL_ID
