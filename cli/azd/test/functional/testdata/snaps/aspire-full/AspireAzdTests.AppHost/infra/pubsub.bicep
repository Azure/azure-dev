@description('')
param location string = resourceGroup().location

@metadata({azd: { defaultValueExpr: '{{ .Env.AZURE_CONTAINER_REGISTRY_MANAGED_IDENTITY_ID }}' } })
param principalId string

@metadata({azd : { defaultValueExpr: '{{ .Env.MANAGED_IDENTITY_CLIENT_ID }}' } })
param principalClientId string

@metadata({azd: { defaultValueExpr: '{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_ID }}' } })
param environmentId string

@metadata({azd: { defaultValueExpr: '{{ .Env.AZURE_CONTAINER_REGISTRY_ENDPOINT }}' } })
param containerRegistryEndpoint string

@metadata({azd: { defaultValueExpr: '{{ .Image }}' } })
param image string

@metadata({azd: { defaultValueExpr: '6379' } })
param targetPort int

resource app 'Microsoft.App/containerApps@2024-02-02-preview' = {
  name: 'pubsub'
  location: location
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${principalId}': {}
    }
  }
  properties: {
    environmentId: environmentId
    configuration: {
      activeRevisionsMode: 'Single'
      runtime: {
        dotnet: {
          autoConfigureDataProtection: true
        }
      }
      ingress: {
        external: false
        targetPort: targetPort
        transport: 'tcp'
        allowInsecure: false
      }
      registries: [
          {
            server: containerRegistryEndpoint
            identity: principalId
          }
      ]
    }
    template: {
      containers: [
        {
          image: image
          name: 'pubsub'
          env: [
            {
              name: 'AZURE_CLIENT_ID'
              value: principalClientId
            }
          ]
        }
      ]
      scale: {
        minReplicas: 1
      }
    }
  }
  tags: {
    'azd-service-name': 'pubsub'
    'aspire-resource-name': 'pubsub'
  }
}

