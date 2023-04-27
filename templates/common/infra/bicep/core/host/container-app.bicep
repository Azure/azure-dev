param name string
param location string = resourceGroup().location
param tags object = {}

param containerAppsEnvironmentName string
param containerName string = 'main'
param containerRegistryName string

@description('Minimum number of replicas to run')
@minValue(0)
param containerMinReplicas int = 1
@description('Maximum number of replicas to run')
@minValue(1)
param containerMaxReplicas int = 1

param env array = []
param external bool = true
param imageName string
param keyVaultName string = ''
@description('Param to denote whether to use a managed identity or not. If not, the identityPrincipalId output will be empty. DEPRECATED: Use managedIdentityEnabled instead.')
param managedIdentity bool = !empty(keyVaultName)
@description('Param to denote whether to use a managed identity or not. If false, the identityPrincipalId output will be empty.')
param managedIdentityEnabled bool = !empty(keyVaultName)
@description('Name of the managed identity to use.')
param managedIdentityName string = ''

param targetPort int = 80

@description('Enabled Ingress for container app')
param ingressEnabled bool = true
@description('Enable Dapr')
param daprEnabled bool = false
@description('Dapr app ID')
param daprAppId string = containerName
@allowed(['http', 'grpc'])
@description('Protocol used by Dapr to connect to the app, e.g. http or grpc')
param daprAppProtocol string = 'http'

@description('CPU cores allocated to a single container instance, e.g. 0.5')
param containerCpuCoreCount string = '0.5'

@description('Memory allocated to a single container instance, e.g. 1Gi')
param containerMemory string = '1.0Gi'

resource app 'Microsoft.App/containerApps@2022-03-01' = {
  name: name
  location: location
  tags: tags
  identity: (managedIdentityEnabled || managedIdentity) ? !empty(managedIdentityName) ? {
    type: 'UserAssigned'
    userAssignedIdentities:  {
      '${userManagedIdentity.id}' : {}
    }
  } : { type: 'SystemAssigned' } : { type: 'None' }
  properties: {
    managedEnvironmentId: containerAppsEnvironment.id
    configuration: {
      activeRevisionsMode: 'single'
      ingress: ingressEnabled ? {
        external: external
        targetPort: targetPort
        transport: 'auto'
      } : null
      secrets: [
        {
          name: 'registry-password'
          value: containerRegistry.listCredentials().passwords[0].value
        }
      ]
      dapr: daprEnabled ? {
        enabled: true
        appId: daprAppId
        appProtocol: daprAppProtocol
        appPort: ingressEnabled ? targetPort : 0
      } : {enabled: false}
      registries: [
        {
          server: '${containerRegistry.name}.azurecr.io'
          username: containerRegistry.name
          passwordSecretRef: 'registry-password'
        }
      ]
    }
    template: {
      containers: [
        {
          image: imageName
          name: containerName
          env: env
          resources: {
            cpu: json(containerCpuCoreCount)
            memory: containerMemory
          }
        }
      ]
      scale: {
        minReplicas: containerMinReplicas
        maxReplicas: containerMaxReplicas
      }
    }
  }
}

resource containerAppsEnvironment 'Microsoft.App/managedEnvironments@2022-03-01' existing = {
  name: containerAppsEnvironmentName
}

// 2022-02-01-preview needed for anonymousPullEnabled
resource containerRegistry 'Microsoft.ContainerRegistry/registries@2022-02-01-preview' existing = {
  name: containerRegistryName
}

// user assigned managed identity to use throughout
resource userManagedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = if (managedIdentityName != '') {
  name: managedIdentityName
}


output defaultDomain string = containerAppsEnvironment.properties.defaultDomain
// If user managed identity is used, the output app.identity.principalId is not available to be queried
output identityPrincipalId string = managedIdentity && empty(managedIdentityName) ? app.identity.principalId : ''
output imageName string = imageName
output name string = app.name
// If no ingress is enabled, the output uri is empty
output uri string = ingressEnabled ? 'https://${app.properties.configuration.ingress.fqdn}': ''
