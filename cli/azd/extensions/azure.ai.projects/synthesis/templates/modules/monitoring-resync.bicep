targetScope = 'resourceGroup'

// Re-applies the Application Insights component after its Azure Monitor private link
// scope association exists. A fresh ingestion path can keep rejecting traffic with
// "Component private link id does not match incoming link" until the component is
// updated again, and ARM reports the scoped resource as Succeeded before then.
// Every property must match the original component declaration in monitoring.bicep.
param name string
param location string
param tags object
param workspaceResourceId string
param publicNetworkAccessForIngestion string
param publicNetworkAccessForQuery string

@description('The private link scope resource ID. Hashing it into a tag orders this deployment after the scope.')
param privateLinkScopeResourceId string

resource component 'Microsoft.Insights/components@2020-02-02' = {
  name: name
  location: location
  tags: union(tags, {
    'azd-ampls-link': uniqueString(privateLinkScopeResourceId, name)
  })
  kind: 'web'
  properties: {
    Application_Type: 'web'
    WorkspaceResourceId: workspaceResourceId
    publicNetworkAccessForIngestion: publicNetworkAccessForIngestion
    publicNetworkAccessForQuery: publicNetworkAccessForQuery
  }
}

output id string = component.id
