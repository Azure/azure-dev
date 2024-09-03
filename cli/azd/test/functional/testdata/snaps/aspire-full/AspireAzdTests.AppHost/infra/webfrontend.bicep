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
@metadata({azd: { defaultValueExpr: 'http://apiservice.internal.{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}' } })
param apiservice_bindings_http_url string
@metadata({azd: { defaultValueExpr: 'https://apiservice.internal.{{ .Env.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN }}' } })
param apiservice_bindings_https_url string
@metadata({azd: { defaultValueExpr: '{{ secretOutput {{ .Env.SERVICE_BINDING_KVF2EDECB5_ENDPOINT }}secrets/connectionString }}' } })
param cosmos_connectionString string
@metadata({azd: { defaultValueExpr: '{{ .Env.STORAGE_BLOBENDPOINT }}' } })
param markdown_connectionString string
@metadata({azd: { defaultValueExpr: '{{ .Env.STORAGE_QUEUEENDPOINT }}' } })
param messages_connectionString string
@metadata({azd: { defaultValueExpr: 'pubsub:6379' } })
param pubsub_connectionString string
@metadata({azd: { defaultValueExpr: '{{ .Env.STORAGE_TABLEENDPOINT }}' } })
param requestlog_connectionString string
@metadata({azd: { defaultValueExpr: '{{ targetPortOrDefault 0 }}' } })
param webfrontend_bindings_http_targetPort string

resource app 'Microsoft.App/containerApps@2024-02-02-preview' = {
  name: 'webfrontend'
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
        external: true
        targetPort: targetPort
        transport: 'http'
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
          name: 'webfrontend'
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
              name: 'ConnectionStrings__cosmos'
              value: '${cosmos_connectionString}'
            }
            {
              name: 'ConnectionStrings__markdown'
              value: '${markdown_connectionString}'
            }
            {
              name: 'ConnectionStrings__messages'
              value: '${messages_connectionString}'
            }
            {
              name: 'ConnectionStrings__pubsub'
              value: '${pubsub_connectionString}'
            }
            {
              name: 'ConnectionStrings__requestlog'
              value: '${requestlog_connectionString}'
            }
            {
              name: 'HTTP_PORTS'
              value: '${webfrontend_bindings_http_targetPort}'
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
            {
              name: 'services__apiservice__http__0'
              value: '${apiservice_bindings_http_url}'
            }
            {
              name: 'services__apiservice__https__0'
              value: '${apiservice_bindings_https_url}'
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
    'azd-service-name': 'webfrontend'
    'aspire-resource-name': 'webfrontend'
  }
}

