@description('The location for the resource(s) to be deployed.')
param location string = resourceGroup().location

param apphostinfrastructure_outputs_azure_container_apps_environment_default_domain string

param apphostinfrastructure_outputs_azure_container_apps_environment_id string

param apphostinfrastructure_outputs_azure_container_registry_endpoint string

param apphostinfrastructure_outputs_azure_container_registry_managed_identity_id string

param webfrontend_containerimage string

param webfrontend_containerport string

param goversion_value string

resource webfrontend 'Microsoft.App/containerApps@2025-02-02-preview' = {
  name: 'webfrontend'
  location: location
  properties: {
    configuration: {
      activeRevisionsMode: 'Single'
      ingress: {
        external: true
        targetPort: int(webfrontend_containerport)
        transport: 'http'
      }
      registries: [
        {
          server: apphostinfrastructure_outputs_azure_container_registry_endpoint
          identity: apphostinfrastructure_outputs_azure_container_registry_managed_identity_id
        }
      ]
      runtime: {
        dotnet: {
          autoConfigureDataProtection: true
        }
      }
    }
    environmentId: apphostinfrastructure_outputs_azure_container_apps_environment_id
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
              name: 'services__apiservice__http__0'
              value: 'http://apiservice.internal.${apphostinfrastructure_outputs_azure_container_apps_environment_default_domain}'
            }
            {
              name: 'services__apiservice__https__0'
              value: 'https://apiservice.internal.${apphostinfrastructure_outputs_azure_container_apps_environment_default_domain}'
            }
            {
              name: 'GOVERSION'
              value: goversion_value
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
      '${apphostinfrastructure_outputs_azure_container_registry_managed_identity_id}': { }
    }
  }
}
