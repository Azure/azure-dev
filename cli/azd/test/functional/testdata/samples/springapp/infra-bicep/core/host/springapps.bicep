param environmentName string
param relativePath string
param location string = resourceGroup().location
var tags = { 'azd-env-name': environmentName }
var resourceToken = toLower(uniqueString(subscription().id, environmentName, location))


resource asaInstance 'Microsoft.AppPlatform/Spring@2022-12-01' = {
  name: 'asa-${resourceToken}'
  location: location
  tags: union(tags, { 'azd-service-name': 'helloworld-web' })
}

resource asaApp 'Microsoft.AppPlatform/Spring/apps@2022-12-01' = {
  name: 'helloworld-web'
  location: location
  parent: asaInstance
  identity: {
      type: 'SystemAssigned'
    }
  properties: {
    public: true
  }
}

resource asaDeployment 'Microsoft.AppPlatform/Spring/apps/deployments@2022-12-01' = {
  name: 'default'
  parent: asaApp
  properties: {
    deploymentSettings: {
      resourceRequests: {
        cpu: '1'
        memory: '2Gi'
      }
    }
    source: {
      type: 'Jar'
      runtimeVersion: 'Java_11'
      relativePath: relativePath
    }
  }
}

