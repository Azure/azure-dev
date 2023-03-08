// Module to create a CDN profile with a single endpoint
param cdnProfileName string
param cdnEndpointName string
param location string = resourceGroup().location
param tags object = {}

@description('Origin URL for the CDN endpoint')
param originUrl string

@description('Delivery policy rules')
param deliveryPolicyRules array = []

module cdnProfile 'cdn-profile.bicep' = {
  name: 'cdn-profile'
  params: {
    name: cdnProfileName
    location: location
    tags: tags
  }
}

module cdnEndpoint 'cdn-endpoint.bicep' = {
  name: 'cdn-endpoint'
  params: {
    name: cdnEndpointName
    location: location
    tags: tags
    cdnProfileName: cdnProfile.outputs.name
    originUrl: originUrl
    deliveryPolicyRules: deliveryPolicyRules
  }
}

output uri string = cdnEndpoint.outputs.uri
output profileName string = cdnProfile.outputs.name
output profileId string = cdnProfile.outputs.id
output endpointName string = cdnEndpoint.outputs.name
output endpointId string = cdnEndpoint.outputs.id
