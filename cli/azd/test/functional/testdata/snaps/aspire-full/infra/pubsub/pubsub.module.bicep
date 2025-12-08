@description('The location for the resource(s) to be deployed.')
param location string = resourceGroup().location

param env_outputs_azure_container_apps_environment_default_domain string

param env_outputs_azure_container_apps_environment_id string

@secure()
param pubsub_password_value string

resource pubsub 'Microsoft.App/containerApps@2025-01-01' = {
  name: 'pubsub'
  location: location
  properties: {
    configuration: {
      secrets: [
        {
          name: 'redis-password'
          value: pubsub_password_value
        }
      ]
      activeRevisionsMode: 'Single'
      ingress: {
        external: false
        targetPort: 6379
        transport: 'tcp'
      }
    }
    environmentId: env_outputs_azure_container_apps_environment_id
    template: {
      containers: [
        {
          image: 'docker.io/library/redis:7.4'
          name: 'pubsub'
          command: [
            '/bin/sh'
          ]
          args: [
            '-c'
            'redis-server --requirepass \$REDIS_PASSWORD'
          ]
          env: [
            {
              name: 'REDIS_PASSWORD'
              secretRef: 'redis-password'
            }
          ]
        }
      ]
      scale: {
        minReplicas: 1
      }
    }
  }
}
