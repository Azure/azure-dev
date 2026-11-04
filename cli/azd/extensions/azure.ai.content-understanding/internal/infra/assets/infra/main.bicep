// Copyright (c) Microsoft Corporation.
// Licensed under the MIT license.

targetScope = 'subscription'

@minLength(1)
@maxLength(64)
@description('Name of the environment. Used to derive resource names including resource group (rg-<name>), Foundry project (proj-<name>), and a unique suffix for the Foundry resource name.')
param environmentName string

@minLength(1)
@allowed([
  'australiaeast'
  'eastus'
  'eastus2'
  'japaneast'
  'northeurope'
  'southcentralus'
  'southeastasia'
  'swedencentral'
  'uksouth'
  'westeurope'
  'westus'
  'westus3'
])
@description('Primary Azure region for the Foundry resource. Must be a supported region for Azure Content Understanding (https://learn.microsoft.com/azure/ai-services/content-understanding/language-region-support)')
param location string

@description('Optional prefix for the Foundry resource name. The final resource name becomes <prefix>-<unique-suffix>. Leave empty to use the default aif- prefix.')
param foundryResourcePrefix string = ''

@description('Optional existing Foundry endpoint. When set, this template skips creating Foundry account/project and deploys models to this existing account.')
param existingFoundryEndpoint string = ''

@description('Resource group of the existing Foundry account when existingFoundryEndpoint is set.')
param existingFoundryResourceGroup string = ''

@description('Object ID of the user or service principal that should receive Cognitive Services User on the Foundry resource. azd injects AZURE_PRINCIPAL_ID automatically.')
param principalId string

@description('Type of principal for the role assignments.')
@allowed([ 'User', 'ServicePrincipal' ])
param principalType string = 'User'

@description('If "true", assign Cognitive Services User to principalId for Entra-authenticated CU operations. Requires Owner, User Access Administrator, or Role Based Access Control Administrator. Set to "false" when you only have Contributor.')
param assignRolesToPrincipal string = 'false'

// Model deployments are loaded from infra/models.json so the file is
// hand-editable and round-trippable through `azd ai cu infra generate`.
var modelDeployments = loadJsonContent('models.json')

var foundryUniqueSuffix = toLower(uniqueString(subscription().id, environmentName, location))
var rgName       = 'rg-${environmentName}'
var useExistingFoundry = !empty(trim(existingFoundryEndpoint))
var existingFoundryAccountName = useExistingFoundry
  ? split(replace(replace(toLower(trim(existingFoundryEndpoint)), 'https://', ''), 'http://', ''), '.')[0]
  : ''
var normalizedResourcePrefix = toLower(trim(foundryResourcePrefix))
var resourceNamePrefix = empty(normalizedResourcePrefix) ? 'aif' : normalizedResourcePrefix
var foundryResourceName  = useExistingFoundry ? existingFoundryAccountName : '${resourceNamePrefix}-${foundryUniqueSuffix}'
var projectName  = 'proj-${environmentName}'

resource rg 'Microsoft.Resources/resourceGroups@2024-03-01' = if (!useExistingFoundry) {
  name: rgName
  location: location
  tags: {
    'azd-env-name': environmentName
  }
}

resource existingRg 'Microsoft.Resources/resourceGroups@2024-03-01' existing = if (useExistingFoundry) {
  name: existingFoundryResourceGroup
}

module foundryNew 'modules/foundry.bicep' = if (!useExistingFoundry) {
  name: 'foundry-new'
  scope: rg
  params: {
    useExistingFoundry: useExistingFoundry
    accountName: foundryResourceName
    projectName: projectName
    location: location
    principalId: principalId
    principalType: principalType
    assignRolesToPrincipal: toLower(assignRolesToPrincipal) == 'true'
    modelDeployments: modelDeployments
  }
}

module foundryExisting 'modules/foundry.bicep' = if (useExistingFoundry) {
  name: 'foundry-existing'
  scope: existingRg
  params: {
    useExistingFoundry: useExistingFoundry
    accountName: foundryResourceName
    projectName: projectName
    location: location
    principalId: principalId
    principalType: principalType
    assignRolesToPrincipal: toLower(assignRolesToPrincipal) == 'true'
    modelDeployments: modelDeployments
  }
}

output AZURE_LOCATION              string = location
output AZURE_RESOURCE_GROUP        string = useExistingFoundry ? existingRg.name : rg.name
output AZURE_TENANT_ID             string = tenant().tenantId
output AZURE_SUBSCRIPTION_ID       string = subscription().subscriptionId

output FOUNDRY_RESOURCE_NAME       string = useExistingFoundry ? foundryExisting!.outputs.accountName : foundryNew!.outputs.accountName
output FOUNDRY_PROJECT_NAME        string = useExistingFoundry ? foundryExisting!.outputs.projectName : foundryNew!.outputs.projectName
output FOUNDRY_ENDPOINT            string = useExistingFoundry ? existingFoundryEndpoint : foundryNew!.outputs.accountEndpoint
output FOUNDRY_PROJECT_ENDPOINT    string = useExistingFoundry ? foundryExisting!.outputs.projectEndpoint : foundryNew!.outputs.projectEndpoint
output CU_ENDPOINT                 string = useExistingFoundry ? existingFoundryEndpoint : foundryNew!.outputs.accountEndpoint
output MODEL_DEPLOYMENTS           array  = useExistingFoundry ? foundryExisting!.outputs.modelDeployments : foundryNew!.outputs.modelDeployments
