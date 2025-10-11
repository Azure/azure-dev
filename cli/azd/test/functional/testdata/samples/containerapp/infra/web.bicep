param environmentName string
param location string

param containerRegistryEndpoint string
param containerEnvironmentId string
param imageName string
param identityId string

resource web 'Microsoft.App/containerApps@2025-01-01' = {
  name: 'web'
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${identityId}': {}
    }
  }
  properties: {
    environmentId: containerEnvironmentId
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: 8080
        transport: 'http'
        allowInsecure: false
      }
      registries: [
        {
          server: containerRegistryEndpoint
          identity: identityId
        }
      ]
    }
    template: { 
      containers: [
        {
          image: imageName
          name: 'main'
          resources: {
            cpu: json('0.5')
            memory: '1.0Gi'
          }
        }
      ]
      scale: {
        minReplicas: 1
      }
    }
  }
  tags: { 'azd-env-name': environmentName, 'azd-service-name': 'web' }
}
