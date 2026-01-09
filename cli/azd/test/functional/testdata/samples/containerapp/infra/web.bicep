@minLength(1)
@maxLength(64)
@description('Name of the the environment which is used to generate a short unique hash used in all resources.')
param environmentName string

@minLength(1)
@description('Primary location for all resources')
param location string

param containerRegistryName string
param containerAppsEnvironmentName string

param imageName string

param identityId string

resource containerRegistry 'Microsoft.ContainerRegistry/registries@2023-01-01-preview' existing = {
  name: containerRegistryName
}

resource containerAppsEnvironment 'Microsoft.App/managedEnvironments@2022-03-01' existing = {
  name: containerAppsEnvironmentName
}

module web 'br/public:avm/res/app/container-app:0.8.0' = {
  name: 'web'
  params: {
    name: 'web'
    ingressTargetPort: 8080
    scaleMinReplicas: 1
    scaleMaxReplicas: 10
    secrets: {
      secureList:  [
      ]
    }
    containers: [
      {
        image: imageName
        name: 'main'
        resources: {
          cpu: json('0.5')
          memory: '1.0Gi'
        }
      }
    ]
    managedIdentities:{
      systemAssigned: false
      userAssignedResourceIds: [identityId]
    }
    registries:[
      {
        server: containerRegistry.properties.loginServer
        identity: identityId
      }
    ]
    environmentResourceId: containerAppsEnvironment.id
    location: location
    tags: { 'azd-env-name': environmentName, 'azd-service-name': 'web' }
  }
}

output WEBSITE_URL string = 'https://${web.outputs.fqdn}'
