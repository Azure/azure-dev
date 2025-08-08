@description('The location for the resource(s) to be deployed.')
param location string = resourceGroup().location

param env_outputs_azure_container_apps_environment_default_domain string

param env_outputs_azure_container_apps_environment_id string

param env_outputs_azure_container_registry_endpoint string

param env_outputs_azure_container_registry_managed_identity_id string

param webfrontend_containerimage string

param webfrontend_identity_outputs_id string

param webfrontend_containerport string

@secure()
param pubsub_password_value string

param storage_outputs_tableendpoint string

param storage_outputs_blobendpoint string

param storage_outputs_queueendpoint string

param goversion_value string

param webfrontend_identity_outputs_clientid string

resource webfrontend 'Microsoft.App/containerApps@2025-02-02-preview' = {
  name: 'webfrontend'
  location: location
  properties: {
    configuration: {
      secrets: [
        {
          name: 'connectionstrings--pubsub'
          value: 'pubsub:6379,password=${pubsub_password_value}'
        }
      ]
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: int(webfrontend_containerport)
        transport: 'http'
      }
      registries: [
        {
          server: env_outputs_azure_container_registry_endpoint
          identity: env_outputs_azure_container_registry_managed_identity_id
        }
      ]
      runtime: {
        dotnet: {
          autoConfigureDataProtection: true
        }
      }
    }
    environmentId: env_outputs_azure_container_apps_environment_id
    template: {
      containers: [
        {
          image: webfrontend_containerimage
          name: 'webfrontend'
          env: [
            {
              name: 'OTEL_DOTNET_EXPERIMENTAL_OTLP_EMIT_EXCEPTION_LOG_ATTRIBUTES'
              value: 'true'
            }
            {
              name: 'OTEL_DOTNET_EXPERIMENTAL_OTLP_EMIT_EVENT_LOG_ATTRIBUTES'
              value: 'true'
            }
            {
              name: 'OTEL_DOTNET_EXPERIMENTAL_OTLP_RETRY'
              value: 'in_memory'
            }
            {
              name: 'ASPNETCORE_FORWARDEDHEADERS_ENABLED'
              value: 'true'
            }
            {
              name: 'HTTP_PORTS'
              value: webfrontend_containerport
            }
            {
              name: 'ConnectionStrings__pubsub'
              secretRef: 'connectionstrings--pubsub'
            }
            {
              name: 'ConnectionStrings__requestlog'
              value: storage_outputs_tableendpoint
            }
            {
              name: 'ConnectionStrings__markdown'
              value: storage_outputs_blobendpoint
            }
            {
              name: 'ConnectionStrings__messages'
              value: storage_outputs_queueendpoint
            }
            {
              name: 'services__apiservice__http__0'
              value: 'http://apiservice.internal.${env_outputs_azure_container_apps_environment_default_domain}'
            }
            {
              name: 'services__apiservice__https__0'
              value: 'https://apiservice.internal.${env_outputs_azure_container_apps_environment_default_domain}'
            }
            {
              name: 'GOVERSION'
              value: goversion_value
            }
            {
              name: 'AZURE_CLIENT_ID'
              value: webfrontend_identity_outputs_clientid
            }
          ]
        }
      ]
      scale: {
        minReplicas: 1
      }
    }
  }
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${webfrontend_identity_outputs_id}': { }
      '${env_outputs_azure_container_registry_managed_identity_id}': { }
    }
  }
}
