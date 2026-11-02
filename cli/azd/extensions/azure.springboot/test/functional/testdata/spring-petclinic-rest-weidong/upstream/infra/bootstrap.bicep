param location string
param serviceName string
param environmentId string
param identityId string

// Discover the environment's outbound IPs without resetting an existing application.
@onlyIfNotExists()
resource app 'Microsoft.App/containerApps@2024-03-01' = {
  name: serviceName
  location: location
  tags: { 'azd-service-name': serviceName }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: { '${identityId}': {} }
  }
  properties: {
    managedEnvironmentId: environmentId
    configuration: { activeRevisionsMode: 'Single' }
    template: {
      containers: [
        {
          name: 'app'
          image: 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'
          resources: { cpu: json('0.5'), memory: '1Gi' }
        }
      ]
      scale: { minReplicas: 1, maxReplicas: 1 }
    }
  }
}

output outboundIps array = app.properties.outboundIpAddresses
output image string = app.properties.template.containers[0].image
