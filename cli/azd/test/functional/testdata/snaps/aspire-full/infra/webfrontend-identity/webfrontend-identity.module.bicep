@description('The location for the resource(s) to be deployed.')
param location string = resourceGroup().location

resource webfrontend_identity 'Microsoft.ManagedIdentity/userAssignedIdentities@2024-11-30' = {
  name: take('webfrontend_identity-${uniqueString(resourceGroup().id)}', 128)
  location: location
}

output id string = webfrontend_identity.id

output clientId string = webfrontend_identity.properties.clientId

output principalId string = webfrontend_identity.properties.principalId

output principalName string = webfrontend_identity.name

output name string = webfrontend_identity.name
