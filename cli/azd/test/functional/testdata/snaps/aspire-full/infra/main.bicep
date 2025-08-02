targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the environment that can be used as part of naming resource convention, the name of the resource group for your application will use this name, prefixed with rg-')
param environmentName string

@minLength(1)
@description('The location used for all deployed resources')
param location string

@description('Id of the user or app to assign application roles')
param principalId string = ''

param goversion string = '1.22'
@metadata({azd: {
  type: 'generate'
  config: {length:22,noSpecial:true}
  }
})
@secure()
param pubsub_password string

var tags = {
  'azd-env-name': environmentName
}

resource rg 'Microsoft.Resources/resourceGroups@2022-09-01' = {
  name: 'rg-${environmentName}'
  location: location
  tags: tags
}

module env 'env/env.module.bicep' = {
  name: 'env'
  scope: rg
  params: {
    location: location
    userPrincipalId: principalId
  }
}
module storage 'storage/storage.module.bicep' = {
  name: 'storage'
  scope: rg
  params: {
    location: location
  }
}
module webfrontend_identity 'webfrontend-identity/webfrontend-identity.module.bicep' = {
  name: 'webfrontend-identity'
  scope: rg
  params: {
    location: location
  }
}
module webfrontend_roles_storage 'webfrontend-roles-storage/webfrontend-roles-storage.module.bicep' = {
  name: 'webfrontend-roles-storage'
  scope: rg
  params: {
    location: location
    principalId: webfrontend_identity.outputs.principalId
    storage_outputs_name: storage.outputs.name
  }
}
output AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN string = env.outputs.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN
output AZURE_CONTAINER_REGISTRY_ENDPOINT string = env.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT
output ENV_AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN string = env.outputs.AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN
output ENV_AZURE_CONTAINER_APPS_ENVIRONMENT_ID string = env.outputs.AZURE_CONTAINER_APPS_ENVIRONMENT_ID
output ENV_AZURE_CONTAINER_REGISTRY_ENDPOINT string = env.outputs.AZURE_CONTAINER_REGISTRY_ENDPOINT
output ENV_AZURE_CONTAINER_REGISTRY_MANAGED_IDENTITY_ID string = env.outputs.AZURE_CONTAINER_REGISTRY_MANAGED_IDENTITY_ID
output STORAGE_BLOBENDPOINT string = storage.outputs.blobEndpoint
output STORAGE_QUEUEENDPOINT string = storage.outputs.queueEndpoint
output STORAGE_TABLEENDPOINT string = storage.outputs.tableEndpoint
output WEBFRONTEND_IDENTITY_CLIENTID string = webfrontend_identity.outputs.clientId
output WEBFRONTEND_IDENTITY_ID string = webfrontend_identity.outputs.id
output AZURE_GOVERSION string = goversion

