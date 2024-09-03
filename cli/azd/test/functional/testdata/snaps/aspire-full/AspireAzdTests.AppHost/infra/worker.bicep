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

@metadata({azd: { defaultValueExpr: '' } })
param targetPort int

resource app 'Microsoft.App/containerApps@2024-02-02-preview' = {
  name: 'worker'
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
          name: 'worker'
          env: [
            {
              name: 'AZURE_CLIENT_ID'
              value: principalClientId
            }
            {
              name: 'OTEL_DOTNET_EXPERIMENTAL_OTLP_EMIT_EVENT_LOG_ATTRIBUTES'
              value: 'true'
            }
            {
              name: 'OTEL_DOTNET_EXPERIMENTAL_OTLP_EMIT_EXCEPTION_LOG_ATTRIBUTES'
              value: 'true'
            }
            {
              name: 'OTEL_DOTNET_EXPERIMENTAL_OTLP_RETRY'
              value: 'in_memory'
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
    'azd-service-name': 'worker'
    'aspire-resource-name': 'worker'
  }
}

