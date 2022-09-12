param environmentName string
param location string = resourceGroup().location
param imageName string
param serviceName string
param keyVaultName string = ''
param useKeyVault bool = !(empty(keyVaultName))
param managedIdentity bool = useKeyVault
param env array = []
param targetPort int = 80
param external bool = true

var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))
var tags = { 'azd-env-name': environmentName }
var abbrs = loadJsonContent('../../abbreviations.json')

resource containerAppsEnvironment 'Microsoft.App/managedEnvironments@2022-03-01' existing = {
  name: '${abbrs.appManagedEnvironments}${resourceToken}'
}

// 2022-02-01-preview needed for anonymousPullEnabled
resource containerRegistry 'Microsoft.ContainerRegistry/registries@2022-02-01-preview' existing = {
  name: '${abbrs.containerRegistryRegistries}${resourceToken}'
}

resource app 'Microsoft.App/containerApps@2022-03-01' = {
  name: '${abbrs.appContainerApps}${serviceName}-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': serviceName })
  identity: managedIdentity ? { type: 'SystemAssigned' } : null
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
          name: 'main'
          env: env
        }
      ]
    }
  }
}

module keyVaultAccess '../security/keyvault-access.bicep' = if (useKeyVault) {
  name: 'appservice-keyvault-access-${serviceName}'
  params: {
    principalId: app.identity.principalId
    environmentName: environmentName
    location: location
  }
}

output NAME string = app.name
output URI string = 'https://${app.properties.configuration.ingress.fqdn}'
output IDENTITY_PRINCIPAL_ID string = managedIdentity ? app.identity.principalId : ''
