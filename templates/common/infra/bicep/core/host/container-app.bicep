param name string
param location string = resourceGroup().location
param tags object = {}

param containerAppsEnvironmentName string
param containerName string = 'main'
param containerRegistryName string
param secrets array = []
param env array = []
param external bool = true
param imageName string
param targetPort int = 80

@description('Managed identity type')
@allowed([ 'SystemAssigned', 'UserAssigned' ])
param managedIdentityType string = 'SystemAssigned'

@description('User assigned identity name')
param userAssignedIdentityName string = ''

@description('CPU cores allocated to a single container instance, e.g. 0.5')
param containerCpuCoreCount string = '0.5'

@description('Memory allocated to a single container instance, e.g. 1Gi')
param containerMemory string = '1.0Gi'

resource app 'Microsoft.App/containerApps@2022-03-01' = {
  name: name
  location: location
  tags: tags
  identity: {
    type: managedIdentityType == 'SystemAssigned' ? 'SystemAssigned' : 'UserAssigned'
    userAssignedIdentities: managedIdentityType == 'UserAssigned' ? { '${userIdentity.id}': {} } : null
  }
  properties: {
    managedEnvironmentId: containerAppsEnvironment.id
    configuration: {
      activeRevisionsMode: 'single'
      ingress: {
        external: external
        targetPort: targetPort
        transport: 'auto'
      }
      secrets: secrets
      registries: [
        {
          server: '${containerRegistry.name}.azurecr.io'
          identity: managedIdentityType == 'SystemAssigned' ? 'system' : userIdentity.id
        }
      ]
    }
    template: {
      containers: [
        {
          image: !empty(imageName) ? imageName : 'mcr.microsoft.com/azuredocs/containerapps-helloworld:latest'
          name: containerName
          env: env
          resources: {
            cpu: json(containerCpuCoreCount)
            memory: containerMemory
          }
        }
      ]
    }
  }
}

resource userIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' existing = if (managedIdentityType == 'UserAssigned') {
  name: userAssignedIdentityName
}

resource containerAppsEnvironment 'Microsoft.App/managedEnvironments@2022-03-01' existing = {
  name: containerAppsEnvironmentName
}

// 2022-02-01-preview needed for anonymousPullEnabled
resource containerRegistry 'Microsoft.ContainerRegistry/registries@2022-02-01-preview' existing = {
  name: containerRegistryName
}

module containerRegistryAccess '../security/registry-access.bicep' = {
  name: 'app-container-registry-access'
  params: {
    containerRegistryName: containerRegistryName
    principalId: managedIdentityType == 'SystemAssigned' ? app.identity.principalId : userIdentity.properties.principalId
  }
}

output defaultDomain string = containerAppsEnvironment.properties.defaultDomain
output identityPrincipalId string = managedIdentityType == 'SystemAssigned' ? app.identity.principalId : userIdentity.properties.principalId
output imageName string = imageName
output name string = app.name
output uri string = 'https://${app.properties.configuration.ingress.fqdn}'
