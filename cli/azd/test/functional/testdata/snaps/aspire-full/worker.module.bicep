@description('The location for the resource(s) to be deployed.')
param location string = resourceGroup().location

param apphostinfrastructure_outputs_azure_container_apps_environment_default_domain string

param apphostinfrastructure_outputs_azure_container_apps_environment_id string

param apphostinfrastructure_outputs_azure_container_registry_endpoint string

param apphostinfrastructure_outputs_azure_container_registry_managed_identity_id string

param worker_containerimage string

resource worker 'Microsoft.App/containerApps@2025-02-02-preview' = {
  name: 'worker'
  location: location
  properties: {
    configuration: {
      activeRevisionsMode: 'Single'
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
          image: worker_containerimage
          name: 'worker'
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
