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

@metadata({azd: { defaultValueExpr: '{{ targetPortOrDefault 8080 }}' } })
param targetPort int
@metadata({azd: { defaultValueExpr: '{{ targetPortOrDefault 0 }}' } })
param apiservice_bindings_http_targetPort string

resource app 'Microsoft.App/containerApps@2024-02-02-preview' = {
  name: 'apiservice'
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
        transport: 'http'
        allowInsecure: true
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
          name: 'apiservice'
          env: [
            {
              name: 'AZURE_CLIENT_ID'
              value: principalClientId
            }
            {
              name: 'ASPNETCORE_FORWARDEDHEADERS_ENABLED'
              value: 'true'
            }
            {
              name: 'HTTP_PORTS'
              value: '${apiservice_bindings_http_targetPort}'
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
    'azd-service-name': 'apiservice'
    'aspire-resource-name': 'apiservice'
  }
}

