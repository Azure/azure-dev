param name string
param location string = resourceGroup().location
param tags object = {}

param containerAppsEnvironmentName string
param containerName string = 'main'
param containerRegistryName string

@description('Minimum number of replicas to run')
param containerMinReplicas int = 1
@description('Maximum number of replicas to run')
param containerMaxReplicas int = 1

param env array = []
param external bool = true
param imageName string
param keyVaultName string = ''
@description('Param to denote whether to use a managed identity or not. If not, the identityPrincipalId output will be empty. DEPRECATED: Use managedIdentityEnabled instead.')
param managedIdentity bool = !empty(keyVaultName)
@description('Param to denote whether to use a managed identity or not. If not, the identityPrincipalId output will be empty.')
param managedIdentityEnabled bool = !empty(keyVaultName)
@description('Name of the managed identity to use.')
param managedIdentityName string = ''
param targetPort int = 80

@description('Enable Dapr')
param daprEnabled bool = false
@description('Dapr app ID')
param daprApp string = containerName
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
  identity: managedIdentityEnabled || managedIdentity ? {
    type: 'SystemAssigned,UserAssigned'
    userAssignedIdentities: !empty(managedIdentityName) ? {
      '${managedIdentityRes.id}' : {}
    } : {}
  } : { type: 'None'}
  dependsOn: [managedIdentityRes]
  properties: {
    managedEnvironmentId: containerAppsEnvironment.id
    configuration: {
      activeRevisionsMode: 'single'
      ingress: {
        external: external
        targetPort: targetPort
        transport: 'auto'
      }
      secrets: [
        {
          name: 'registry-password'
          value: containerRegistry.listCredentials().passwords[0].value
        }
      ]
      dapr: daprEnabled ? {
        enabled: true
        appId: daprApp
        appProtocol: daprAppProtocol
        appPort: targetPort
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
        minReplicas: containerMaxReplicas > 1 ? containerMaxReplicas : 1
        maxReplicas: containerMinReplicas > 1 ? containerMinReplicas : 1
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
resource managedIdentityRes 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = {
  name: managedIdentityName
}


output identityPrincipalId string = managedIdentity ? app.identity.principalId : ''
output userManagedIdentitylId string = managedIdentityEnabled ? managedIdentityRes.id : ''
output imageName string = imageName
output name string = app.name
output uri string = 'https://${app.properties.configuration.ingress.fqdn}'
