@minLength(2)
@description('The location to use for the deployment. defaults to Resource Groups location.')
param location string = resourceGroup().location

@minLength(3)
@maxLength(20)
@description('Used to name all resources')
param resourceName string

/*
Resource sections
1. Networking
2. DNS
3. Key Vault
4. Container Registry
5. Firewall
6. Application Gateway
7. AKS
8. Monitoring / Log Analytics
*/


/*.__   __.  _______ .___________.____    __    ____  ______   .______       __  ___  __  .__   __.   _______
|  \ |  | |   ____||           |\   \  /  \  /   / /  __  \  |   _  \     |  |/  / |  | |  \ |  |  /  _____|
|   \|  | |  |__   `---|  |----` \   \/    \/   / |  |  |  | |  |_)  |    |  '  /  |  | |   \|  | |  |  __
|  . `  | |   __|      |  |       \            /  |  |  |  | |      /     |    <   |  | |  . `  | |  | |_ |
|  |\   | |  |____     |  |        \    /\    /   |  `--'  | |  |\  \----.|  .  \  |  | |  |\   | |  |__| |
|__| \__| |_______|    |__|         \__/  \__/     \______/  | _| `._____||__|\__\ |__| |__| \__|  \______| */
//Networking can either be one of: custom / byo / default

@description('Are you providing your own vNet CIDR blocks')
param custom_vnet bool = false

@description('Full resource id path of an existing subnet to use for AKS')
param byoAKSSubnetId string = ''

@description('Full resource id path of an existing subnet to use for Application Gateway')
param byoAGWSubnetId string = ''

//--- Custom, BYO networking and PrivateApiZones requires BYO AKS User Identity
var createAksUai = custom_vnet || !empty(byoAKSSubnetId) || !empty(dnsApiPrivateZoneId)
resource aksUai 'Microsoft.ManagedIdentity/userAssignedIdentities@2018-11-30' = if (createAksUai) {
  name: 'id-aks-${resourceName}'
  location: location
}

//----------------------------------------------------- BYO Vnet
var existingAksVnetRG = !empty(byoAKSSubnetId) ? (length(split(byoAKSSubnetId, '/')) > 4 ? split(byoAKSSubnetId, '/')[4] : '') : ''

module aksnetcontrib './aksnetcontrib.bicep' = if (!empty(byoAKSSubnetId) && createAksUai) {
  name: 'addAksNetContributor'
  scope: resourceGroup(existingAksVnetRG)
  params: {
    byoAKSSubnetId: byoAKSSubnetId
    user_identity_principalId: createAksUai ? aksUai.properties.principalId : ''
    user_identity_name: aksUai.name
    user_identity_rg: resourceGroup().name
    rbacAssignmentScope: uaiNetworkScopeRbac
  }
}

//------------------------------------------------------ Create custom vnet
@minLength(9)
@maxLength(18)
@description('The address range for the custom vnet')
param vnetAddressPrefix string = '10.240.0.0/16'

@minLength(9)
@maxLength(18)
@description('The address range for AKS in your custom vnet')
param vnetAksSubnetAddressPrefix string = '10.240.0.0/22'

@minLength(9)
@maxLength(18)
@description('The address range for the App Gateway in your custom vnet')
param vnetAppGatewaySubnetAddressPrefix string = '10.240.5.0/24'

@minLength(9)
@maxLength(18)
@description('The address range for the ACR in your custom vnet')
param acrAgentPoolSubnetAddressPrefix string = '10.240.4.64/26'

@minLength(9)
@maxLength(18)
@description('The address range for Azure Bastion in your custom vnet')
param bastionSubnetAddressPrefix string = '10.240.4.128/26'

@minLength(9)
@maxLength(18)
@description('The address range for private link in your custom vnet')
param privateLinkSubnetAddressPrefix string = '10.240.4.192/26'

@minLength(9)
@maxLength(18)
@description('The address range for Azure Firewall in your custom vnet')
param vnetFirewallSubnetAddressPrefix string = '10.240.50.0/24'

@description('Enable support for private links')
param privateLinks bool = false

@description('Enable support for ACR private pool')
param acrPrivatePool bool = false

@description('Deploy Azure Bastion to your vnet. (works with Custom Networking only, not BYO)')
param bastion bool = false

@description('Deploy NSGs to your vnet subnets. (works with Custom Networking only, not BYO)')
param CreateNetworkSecurityGroups bool = false

@description('Configure Flow Logs for Network Security Groups in the NetworkWatcherRG resource group. Requires Contributor RBAC on NetworkWatcherRG and Reader on Subscription.')
param CreateNetworkSecurityGroupFlowLogs bool = false

module network './network.bicep' = if (custom_vnet) {
  name: 'network'
  params: {
    resourceName: resourceName
    location: location
    vnetAddressPrefix: vnetAddressPrefix
    aksPrincipleId: createAksUai ? aksUai.properties.principalId : ''
    vnetAksSubnetAddressPrefix: vnetAksSubnetAddressPrefix
    ingressApplicationGateway: ingressApplicationGateway
    vnetAppGatewaySubnetAddressPrefix: vnetAppGatewaySubnetAddressPrefix
    azureFirewalls: azureFirewalls
    vnetFirewallSubnetAddressPrefix: vnetFirewallSubnetAddressPrefix
    privateLinks: privateLinks
    privateLinkSubnetAddressPrefix: privateLinkSubnetAddressPrefix
    privateLinkAcrId: privateLinks && !empty(registries_sku) ? acr.id : ''
    privateLinkAkvId: privateLinks && keyVaultCreate ? kv.outputs.keyVaultId : ''
    acrPrivatePool: acrPrivatePool
    acrAgentPoolSubnetAddressPrefix: acrAgentPoolSubnetAddressPrefix
    bastion: bastion
    bastionSubnetAddressPrefix: bastionSubnetAddressPrefix
    availabilityZones: availabilityZones
    workspaceName: createLaw ? aks_law.name : ''
    workspaceResourceGroupName:  createLaw ? resourceGroup().name : ''
    networkSecurityGroups: CreateNetworkSecurityGroups
    CreateNsgFlowLogs: CreateNetworkSecurityGroups && CreateNetworkSecurityGroupFlowLogs
    ingressApplicationGatewayPublic: empty(privateIpApplicationGateway)
    natGateway: createNatGateway
    natGatewayIdleTimeoutMins: natGwIdleTimeout
    natGatewayPublicIps: natGwIpCount
  }
}
output CustomVnetId string = custom_vnet ? network.outputs.vnetId : ''
output CustomVnetPrivateLinkSubnetId string = custom_vnet ? network.outputs.privateLinkSubnetId : ''

var aksSubnetId = custom_vnet ? network.outputs.aksSubnetId : byoAKSSubnetId
var appGwSubnetId = ingressApplicationGateway ? (custom_vnet ? network.outputs.appGwSubnetId : byoAGWSubnetId) : ''


/*______  .__   __.      _______.    ________    ______   .__   __.  _______      _______.
|       \ |  \ |  |     /       |   |       /   /  __  \  |  \ |  | |   ____|    /       |
|  .--.  ||   \|  |    |   (----`   `---/  /   |  |  |  | |   \|  | |  |__      |   (----`
|  |  |  ||  . `  |     \   \          /  /    |  |  |  | |  . `  | |   __|      \   \
|  '--'  ||  |\   | .----)   |        /  /----.|  `--'  | |  |\   | |  |____ .----)   |
|_______/ |__| \__| |_______/        /________| \______/  |__| \__| |_______||_______/    */

@description('The full Azure resource ID of the DNS zone to use for the AKS cluster')
param dnsZoneId string = ''
var isDnsZonePrivate = !empty(dnsZoneId) ? split(dnsZoneId, '/')[7] == 'privateDnsZones' : false

module dnsZone './dnsZoneRbac.bicep' = if (!empty(dnsZoneId)) {
  name: 'addDnsContributor'
  params: {
    dnsZoneId: dnsZoneId
    vnetId: isDnsZonePrivate ? (!empty(byoAKSSubnetId) ? split(byoAKSSubnetId, '/subnets')[0] : (custom_vnet ? network.outputs.vnetId : '')) : ''
    principalId: any(aks.properties.identityProfile.kubeletidentity).objectId
  }
}


/*__  __  _______ ____    ____    ____    ____  ___      __    __   __      .___________.
|  |/  / |   ____|\   \  /   /    \   \  /   / /   \    |  |  |  | |  |     |           |
|  '  /  |  |__    \   \/   /      \   \/   / /  ^  \   |  |  |  | |  |     `---|  |----`
|    <   |   __|    \_    _/        \      / /  /_\  \  |  |  |  | |  |         |  |
|  .  \  |  |____     |  |           \    / /  _____  \ |  `--'  | |  `----.    |  |
|__|\__\ |_______|    |__|            \__/ /__/     \__\ \______/  |_______|    |__|     */

@description('Creates a KeyVault')
param keyVaultCreate bool = false

@description('If soft delete protection is enabled')
param keyVaultSoftDelete bool = true

@description('If purge protection is enabled')
param keyVaultPurgeProtection bool = true

@description('Add IP to KV firewall allow-list')
param keyVaultIPAllowlist array = []

@description('Installs the AKS KV CSI provider')
param keyVaultAksCSI bool = false

@description('Rotation poll interval for the AKS KV CSI provider')
param keyVaultAksCSIPollInterval string = '2m'

module kv 'keyvault.bicep' = if(keyVaultCreate) {
  name: 'keyvault'
  params: {
    resourceName: resourceName
    keyVaultPurgeProtection: keyVaultPurgeProtection
    keyVaultSoftDelete: keyVaultSoftDelete
    keyVaultIPAllowlist: keyVaultIPAllowlist
    location: location
    privateLinks: privateLinks
  }
}

@description('The principal ID of the user or service principal that requires access to the Key Vault. Set automatedDeployment to toggle between user and service prinicpal')
param keyVaultOfficerRolePrincipalId string = ''
var keyVaultOfficerRolePrincipalIds = [
  keyVaultOfficerRolePrincipalId
]

@description('Parsing an array with union ensures that duplicates are removed, which is great when dealing with highly conditional elements')
var rbacSecretUserSps = union([deployAppGw && appgwKVIntegration ? appGwIdentity.properties.principalId : ''],[keyVaultAksCSI ? aks.properties.addonProfiles.azureKeyvaultSecretsProvider.identity.objectId : ''])

@description('A seperate module is used for RBAC to avoid delaying the KeyVault creation and causing a circular reference.')
module kvRbac 'keyvaultrbac.bicep' = if (keyVaultCreate) {
  name: 'KeyVaultRbac'
  params: {
    keyVaultName: keyVaultCreate ? kv.outputs.keyVaultName : ''

    //service principals
    rbacSecretUserSps: rbacSecretUserSps
    rbacSecretOfficerSps: !empty(keyVaultOfficerRolePrincipalId) && automatedDeployment ? keyVaultOfficerRolePrincipalIds : []
    rbacCertOfficerSps: !empty(keyVaultOfficerRolePrincipalId) && automatedDeployment ? keyVaultOfficerRolePrincipalIds : []

    //users
    rbacSecretOfficerUsers: !empty(keyVaultOfficerRolePrincipalId) && !automatedDeployment ? keyVaultOfficerRolePrincipalIds : []
    rbacCertOfficerUsers: !empty(keyVaultOfficerRolePrincipalId) && !automatedDeployment ? keyVaultOfficerRolePrincipalIds : []
  }
}

output keyVaultName string = keyVaultCreate ? kv.outputs.keyVaultName : ''
output keyVaultId string = keyVaultCreate ? kv.outputs.keyVaultId : ''


/*   ___           ______     .______
    /   \         /      |    |   _  \
   /  ^  \       |  ,----'    |  |_)  |
  /  /_\  \      |  |         |      /
 /  _____  \   __|  `----. __ |  |\  \----. __
/__/     \__\ (__)\______|(__)| _| `._____|(__)*/

@allowed([
  ''
  'Basic'
  'Standard'
  'Premium'
])
@description('The SKU to use for the Container Registry')
param registries_sku string = ''

@description('Enable the ACR Content Trust Policy, SKU must be set to Premium')
param enableACRTrustPolicy bool = false
var acrContentTrustEnabled = enableACRTrustPolicy && registries_sku == 'Premium' ? 'enabled' : 'disabled'

//param enableACRZoneRedundancy bool = true
var acrZoneRedundancyEnabled = !empty(availabilityZones) && registries_sku == 'Premium' ? 'Enabled' : 'Disabled'

@description('Enable removing of untagged manifests from ACR')
param acrUntaggedRetentionPolicyEnabled bool = false

@description('The number of days to retain untagged manifests for')
param acrUntaggedRetentionPolicy int = 30

var acrName = 'cr${replace(resourceName, '-', '')}${uniqueString(resourceGroup().id, resourceName)}'

resource acr 'Microsoft.ContainerRegistry/registries@2021-06-01-preview' = if (!empty(registries_sku)) {
  name: acrName
  location: location
  sku: {
    #disable-next-line BCP036 //Disabling validation of this parameter to cope with empty string to indicate no ACR required.
    name: registries_sku
  }
  properties: {
    policies: {
      trustPolicy: enableACRTrustPolicy ? {
        status: acrContentTrustEnabled
        type: 'Notary'
      } : {}
      retentionPolicy: acrUntaggedRetentionPolicyEnabled ? {
        status: 'enabled'
        days: acrUntaggedRetentionPolicy
      } : json('null')
    }
    publicNetworkAccess: privateLinks /* && empty(acrIPWhitelist)*/ ? 'Disabled' : 'Enabled'
    zoneRedundancy: acrZoneRedundancyEnabled
    /*
    networkRuleSet: {
      defaultAction: 'Deny'

      ipRules: empty(acrIPWhitelist) ? [] : [
          {
            action: 'Allow'
            value: acrIPWhitelist
        }
      ]
      virtualNetworkRules: []
    }
    */
  }
}
output containerRegistryName string = !empty(registries_sku) ? acr.name : ''
output containerRegistryId string = !empty(registries_sku) ? acr.id : ''


resource acrDiags 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (createLaw && !empty(registries_sku)) {
  name: 'acrDiags'
  scope: acr
  properties: {
    workspaceId:aks_law.id
    logs: [
      {
        category: 'ContainerRegistryRepositoryEvents'
        enabled: true
      }
      {
        category: 'ContainerRegistryLoginEvents'
        enabled: true
      }
    ]
    metrics: [
      {
        category: 'AllMetrics'
        enabled: true
        timeGrain: 'PT1M'
      }
    ]
  }
}

//resource acrPool 'Microsoft.ContainerRegistry/registries/agentPools@2019-06-01-preview' = if (custom_vnet && (!empty(registries_sku)) && privateLinks && acrPrivatePool) {
module acrPool 'acragentpool.bicep' = if (custom_vnet && (!empty(registries_sku)) && privateLinks && acrPrivatePool) {
  name: 'acrprivatepool'
  params: {
    acrName: acr.name
    acrPoolSubnetId: custom_vnet ? network.outputs.acrPoolSubnetId : ''
    location: location
  }
}

var AcrPullRole = resourceId('Microsoft.Authorization/roleDefinitions', '7f951dda-4ed3-4680-a7ca-43fe172d538d')
var KubeletObjectId = any(aks.properties.identityProfile.kubeletidentity).objectId

resource aks_acr_pull 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(registries_sku)) {
  scope: acr // Use when specifying a scope that is different than the deployment scope
  name: guid(aks.id, 'Acr' , AcrPullRole)
  properties: {
    roleDefinitionId: AcrPullRole
    principalType: 'ServicePrincipal'
    principalId: KubeletObjectId
  }
}

var AcrPushRole = resourceId('Microsoft.Authorization/roleDefinitions', '8311e382-0749-4cb8-b61a-304f252e45ec')

@description('The principal ID of the service principal to assign the push role to the ACR')
param acrPushRolePrincipalId string = ''

resource aks_acr_push 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!empty(registries_sku) && !empty(acrPushRolePrincipalId)) {
  scope: acr // Use when specifying a scope that is different than the deployment scope
  name: guid(aks.id, 'Acr' , AcrPushRole)
  properties: {
    roleDefinitionId: AcrPushRole
    principalType: automatedDeployment ? 'ServicePrincipal' : 'User'
    principalId: acrPushRolePrincipalId
  }
}


param imageNames array = []

module acrImport 'br/public:deployment-scripts/import-acr:2.0.1' = if (!empty(registries_sku) && !empty(imageNames)) {
  name: 'testAcrImportMulti'
  params: {
    acrName: acr.name
    location: location
    images: imageNames
  }
}




/*______  __  .______       _______ ____    __    ____  ___       __       __
|   ____||  | |   _  \     |   ____|\   \  /  \  /   / /   \     |  |     |  |
|  |__   |  | |  |_)  |    |  |__    \   \/    \/   / /  ^  \    |  |     |  |
|   __|  |  | |      /     |   __|    \            / /  /_\  \   |  |     |  |
|  |     |  | |  |\  \----.|  |____    \    /\    / /  _____  \  |  `----.|  `----.
|__|     |__| | _| `._____||_______|    \__/  \__/ /__/     \__\ |_______||_______|*/

@description('Create an Azure Firewall')
param azureFirewalls bool = false

@description('Add application rules to the firewall for certificate management.')
param certManagerFW bool = false

// @allowed([
//   'AllowAllIn'
//   'AllowAcrSubnetIn'
//   ''
// ])
// @description('Allow Http traffic (80/443) into AKS from specific sources')
// param inboundHttpFW string = ''

module firewall './firewall.bicep' = if (azureFirewalls && custom_vnet) {
  name: 'firewall'
  params: {
    resourceName: resourceName
    location: location
    workspaceDiagsId: createLaw ? aks_law.id : ''
    fwSubnetId: azureFirewalls && custom_vnet ? network.outputs.fwSubnetId : ''
    vnetAksSubnetAddressPrefix: vnetAksSubnetAddressPrefix
    certManagerFW: certManagerFW
    appDnsZoneName: !empty(dnsZoneId) ? split(dnsZoneId, '/')[8] : ''
    acrPrivatePool: acrPrivatePool
    acrAgentPoolSubnetAddressPrefix: acrAgentPoolSubnetAddressPrefix
    // inboundHttpFW: inboundHttpFW
    availabilityZones: availabilityZones
  }
}

/*   ___      .______   .______          _______ ____    __    ____
    /   \     |   _  \  |   _  \        /  _____|\   \  /  \  /   /
   /  ^  \    |  |_)  | |  |_)  |      |  |  __   \   \/    \/   /
  /  /_\  \   |   ___/  |   ___/       |  | |_ |   \            /
 /  _____  \  |  |      |  |     __    |  |__| |    \    /\    / __
/__/     \__\ | _|      | _|    (__)    \______|     \__/  \__/ (__)*/

@description('Create an Application Gateway')
param ingressApplicationGateway bool = false

@description('The number of applciation gateway instances')
param appGWcount int = 2

@description('The maximum number of application gateway instances')
param appGWmaxCount int = 0

@maxLength(15)
@description('A known private ip in the Application Gateway subnet range to be allocated for internal traffic')
param privateIpApplicationGateway string = ''

@description('Enable key vault integration for application gateway')
param appgwKVIntegration bool = false

@allowed([
  'Standard_v2'
  'WAF_v2'
])
@description('The SKU for AppGw')
param appGWsku string = 'WAF_v2'

@description('Enable the WAF Firewall, valid for WAF_v2 SKUs')
param appGWenableFirewall bool = true

var deployAppGw = ingressApplicationGateway && (custom_vnet || !empty(byoAGWSubnetId))
var appGWenableWafFirewall = appGWsku=='Standard_v2' ? false : appGWenableFirewall

// If integrating App Gateway with KeyVault, create a Identity App Gateway will use to access keyvault
// 'identity' is always created (adding: "|| deployAppGw") until this is fixed:
// https://github.com/Azure/bicep/issues/387#issuecomment-885671296
resource appGwIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2018-11-30' = if (deployAppGw) {
  name: 'id-appgw-${resourceName}'
  location: location
}

var appgwName = 'agw-${resourceName}'
var appgwResourceId = deployAppGw ? resourceId('Microsoft.Network/applicationGateways', '${appgwName}') : ''

resource appgwpip 'Microsoft.Network/publicIPAddresses@2021-02-01' = if (deployAppGw) {
  name: 'pip-agw-${resourceName}'
  location: location
  sku: {
    name: 'Standard'
  }
  zones: !empty(availabilityZones) ? availabilityZones : []
  properties: {
    publicIPAllocationMethod: 'Static'
  }
}

var frontendPublicIpConfig = {
  properties: {
    publicIPAddress: {
      id: appgwpip.id
    }
  }
  name: 'appGatewayFrontendIP'
}

var frontendPrivateIpConfig = {
  properties: {
    privateIPAllocationMethod: 'Static'
    privateIPAddress: privateIpApplicationGateway
    subnet: {
      id: appGwSubnetId
    }
  }
  name: 'appGatewayPrivateIP'
}

@allowed([
  'Prevention'
  'Detection'
])
param appGwFirewallMode string = 'Prevention'

var appGwFirewallConfigOwasp = {
  enabled: appGWenableWafFirewall
  firewallMode: appGwFirewallMode
  ruleSetType: 'OWASP'
  ruleSetVersion: '3.2'
  requestBodyCheck: true
  maxRequestBodySizeInKb: 128
  disabledRuleGroups: []
}

var appGWskuObj = union({
  name: appGWsku
  tier: appGWsku
}, appGWmaxCount == 0 ? {
  capacity: appGWcount
} : {})

// ugh, need to create a variable with the app gateway properies, because of the conditional key 'autoscaleConfiguration'
var appgwProperties = union({
  sku: appGWskuObj
  sslPolicy: {
    policyType: 'Predefined'
    policyName: 'AppGwSslPolicy20170401S'
  }
  webApplicationFirewallConfiguration: appGWenableWafFirewall ? appGwFirewallConfigOwasp : json('null')
  gatewayIPConfigurations: [
    {
      name: 'besubnet'
      properties: {
        subnet: {
          id: appGwSubnetId
        }
      }
    }
  ]
  frontendIPConfigurations: empty(privateIpApplicationGateway) ? array(frontendPublicIpConfig) : concat(array(frontendPublicIpConfig), array(frontendPrivateIpConfig))
  frontendPorts: [
    {
      name: 'appGatewayFrontendPort'
      properties: {
        port: 80
      }
    }
  ]
  backendAddressPools: [
    {
      name: 'defaultaddresspool'
    }
  ]
  backendHttpSettingsCollection: [
    {
      name: 'defaulthttpsetting'
      properties: {
        port: 80
        protocol: 'Http'
        cookieBasedAffinity: 'Disabled'
        requestTimeout: 30
        pickHostNameFromBackendAddress: true
      }
    }
  ]
  httpListeners: [
    {
      name: 'hlisten'
      properties: {
        frontendIPConfiguration: {
          id: empty(privateIpApplicationGateway) ? '${appgwResourceId}/frontendIPConfigurations/appGatewayFrontendIP' : '${appgwResourceId}/frontendIPConfigurations/appGatewayPrivateIP'
        }
        frontendPort: {
          id: '${appgwResourceId}/frontendPorts/appGatewayFrontendPort'
        }
        protocol: 'Http'
      }
    }
  ]
  requestRoutingRules: [
    {
      name: 'appGwRoutingRuleName'
      properties: {
        ruleType: 'Basic'
        httpListener: {
          id: '${appgwResourceId}/httpListeners/hlisten'
        }
        backendAddressPool: {
          id: '${appgwResourceId}/backendAddressPools/defaultaddresspool'
        }
        backendHttpSettings: {
          id: '${appgwResourceId}/backendHttpSettingsCollection/defaulthttpsetting'
        }
      }
    }
  ]
}, appGWmaxCount > 0 ? {
  autoscaleConfiguration: {
    minCapacity: appGWcount
    maxCapacity: appGWmaxCount
  }
} : {})

// 'identity' is always set until this is fixed: https://github.com/Azure/bicep/issues/387#issuecomment-885671296
resource appgw 'Microsoft.Network/applicationGateways@2021-02-01' = if (deployAppGw) {
  name: appgwName
  location: location
  zones: !empty(availabilityZones) ? availabilityZones : []
  identity: {
    type: 'UserAssigned'
    userAssignedIdentities: {
      '${appGwIdentity.id}': {}
    }
  }
  properties: appgwProperties
}

var contributor = resourceId('Microsoft.Authorization/roleDefinitions', 'b24988ac-6180-42a0-ab88-20f7382dd24c')
// https://docs.microsoft.com/en-us/azure/role-based-access-control/role-assignments-template#new-service-principal
// AGIC's identity requires "Contributor" permission over Application Gateway.
resource appGwAGICContrib 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (ingressApplicationGateway && deployAppGw) {
  scope: appgw
  name: guid(aks.id, 'Agic', contributor)
  properties: {
    roleDefinitionId: contributor
    principalType: 'ServicePrincipal'
    principalId: aks.properties.addonProfiles.ingressApplicationGateway.identity.objectId
  }
}

// AGIC's identity requires "Reader" permission over Application Gateway's resource group.
var reader = resourceId('Microsoft.Authorization/roleDefinitions', 'acdd72a7-3385-48ef-bd42-f606fba81ae7')
resource appGwAGICRGReader 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (ingressApplicationGateway && deployAppGw) {
  scope: resourceGroup()
  name: guid(aks.id, 'Agic', reader)
  properties: {
    roleDefinitionId: reader
    principalType: 'ServicePrincipal'
    principalId: aks.properties.addonProfiles.ingressApplicationGateway.identity.objectId
  }
}

// AGIC's identity requires "Managed Identity Operator" permission over the user assigned identity of Application Gateway.
var managedIdentityOperator = resourceId('Microsoft.Authorization/roleDefinitions', 'f1a07417-d97a-45cb-824c-7a7467783830')
resource appGwAGICMIOp 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (ingressApplicationGateway &&  deployAppGw) {
  scope: appGwIdentity
  name: guid(aks.id, 'Agic', managedIdentityOperator)
  properties: {
    roleDefinitionId: managedIdentityOperator
    principalType: 'ServicePrincipal'
    principalId: aks.properties.addonProfiles.ingressApplicationGateway.identity.objectId
  }
}

// AppGW Diagnostics
resource appgw_Diag 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (createLaw && deployAppGw) {
  scope: appgw
  name: 'appgwDiag'
  properties: {
    workspaceId: aks_law.id
    logs: [
      {
        category: 'ApplicationGatewayAccessLog'
        enabled: true
      }
      {
        category: 'ApplicationGatewayPerformanceLog'
        enabled: true
      }
      {
        category: 'ApplicationGatewayFirewallLog'
        enabled: true
      }
    ]
  }
}

// =================================================

// Prevent error: AGIC Identity needs atleast has 'Contributor' access to Application Gateway and 'Reader' access to Application Gateway's Resource Group

output ApplicationGatewayName string = deployAppGw ? appgw.name : ''

/*_  ___  __    __  .______    _______ .______      .__   __.  _______ .___________. _______      _______.
|  |/  / |  |  |  | |   _  \  |   ____||   _  \     |  \ |  | |   ____||           ||   ____|    /       |
|  '  /  |  |  |  | |  |_)  | |  |__   |  |_)  |    |   \|  | |  |__   `---|  |----`|  |__      |   (----`
|    <   |  |  |  | |   _  <  |   __|  |      /     |  . `  | |   __|      |  |     |   __|      \   \
|  .  \  |  `--'  | |  |_)  | |  |____ |  |\  \----.|  |\   | |  |____     |  |     |  |____ .----)   |
|__|\__\  \______/  |______/  |_______|| _| `._____||__| \__| |_______|    |__|     |_______||_______/ */

@description('DNS prefix. Defaults to {resourceName}-dns')
param dnsPrefix string = '${resourceName}-dns'

@description('Kubernetes Version')
param kubernetesVersion string = '1.23.8'

@description('Enable Azure AD integration on AKS')
param enable_aad bool = false

@description('The ID of the Azure AD tenant')
param aad_tenant_id string = ''

@description('Create, and use a new Log Analytics workspace for AKS logs')
param omsagent bool = false

@description('Enable RBAC using AAD')
param enableAzureRBAC bool = false

@description('Enables Kubernetes Event-driven Autoscaling (KEDA)')
param kedaAddon bool = false

@description('Enables Open Service Mesh')
param openServiceMeshAddon bool = false

@description('Enables the Blob CSI extension')
param blobCSIAddon bool = false

@allowed([
  'none'
  'patch'
  'stable'
  'rapid'
  'node-image'
])
@description('AKS upgrade channel')
param upgradeChannel string = 'none'

@allowed([
  'Ephemeral'
  'Managed'
])
@description('OS disk type')
param osDiskType string = 'Ephemeral'

@description('VM SKU')
param agentVMSize string = 'Standard_DS3_v2'

@description('Disk size in GB')
param osDiskSizeGB int = 0

@description('The number of agents for the user node pool')
param agentCount int = 3

@description('The maximum number of nodes for the user node pool')
param agentCountMax int = 0
var autoScale = agentCountMax > agentCount

@description('The maximum number of pods per node.')
param maxPods int = 30

@allowed([
  'azure'
  'kubenet'
])
@description('The network plugin type')
param networkPlugin string = 'azure'

@allowed([
  ''
  'azure'
  'calico'
])
@description('The network policy to use.')
param networkPolicy string = ''

@allowed([
  ''
  'audit'
  'deny'
])
@description('Enable the Azure Policy addon')
param azurepolicy string = ''

@allowed([
  'Baseline'
  'Restricted'
])
param azurePolicyInitiative string = 'Baseline'

@description('The IP addresses that are allowed to access the API server')
param authorizedIPRanges array = []

@description('Enable private cluster')
param enablePrivateCluster bool = false

@allowed([
  'system'
  'none'
  'privateDnsZone'
])
@description('Private cluster dns advertisment method, leverages the dnsApiPrivateZoneId parameter')
param privateClusterDnsMethod string = 'system'

@description('The full Azure resource ID of the privatelink DNS zone to use for the AKS cluster API Server')
param dnsApiPrivateZoneId string = ''

@description('The zones to use for a node pool')
param availabilityZones array = []

@description('Disable local K8S accounts for AAD enabled clusters')
param AksDisableLocalAccounts bool = false

@description('Use the paid sku for SLA rather than SLO')
param AksPaidSkuForSLA bool = false

@minLength(9)
@maxLength(18)
@description('The address range to use for pods')
param podCidr string = '10.240.100.0/22'

@minLength(9)
@maxLength(18)
@description('The address range to use for services')
param serviceCidr string = '172.10.0.0/16'

@minLength(7)
@maxLength(15)
@description('The IP address to reserve for DNS')
param dnsServiceIP string = '172.10.0.10'

@minLength(9)
@maxLength(18)
@description('The address range to use for the docker bridge')
param dockerBridgeCidr string = '172.17.0.1/16'

@description('Enable Microsoft Defender for Containers (preview)')
param defenderForContainers bool = false

@description('Only use the system node pool')
param JustUseSystemPool bool = false

@allowed([
  'CostOptimised'
  'Standard'
  'HighSpec'
  'Custom'
])
@description('The System Pool Preset sizing')
param SystemPoolType string = 'CostOptimised'

@description('A custom system pool spec')
param SystemPoolCustomPreset object = {}

param AutoscaleProfile object = {
  'balance-similar-node-groups': 'true'
  expander: 'random'
  'max-empty-bulk-delete': '10'
  'max-graceful-termination-sec': '600'
  'max-node-provision-time': '15m'
  'max-total-unready-percentage': '45'
  'new-pod-scale-up-delay': '0s'
  'ok-total-unready-count': '3'
  'scale-down-delay-after-add': '10m'
  'scale-down-delay-after-delete': '20s'
  'scale-down-delay-after-failure': '3m'
  'scale-down-unneeded-time': '10m'
  'scale-down-unready-time': '20m'
  'scale-down-utilization-threshold': '0.5'
  'scan-interval': '10s'
  'skip-nodes-with-local-storage': 'true'
  'skip-nodes-with-system-pods': 'true'
}

@allowed([
  'loadBalancer'
  'managedNATGateway'
  'userAssignedNATGateway'
])
@description('Outbound traffic type for the egress traffic of your cluster')
param aksOutboundTrafficType string = 'loadBalancer'

@description('Create a new Nat Gateway, applies to custom networking only')
param createNatGateway bool = false

@minValue(1)
@maxValue(16)
@description('The effective outbound IP resources of the cluster NAT gateway')
param natGwIpCount int = 2

@minValue(4)
@maxValue(120)
@description('Outbound flow idle timeout in minutes for NatGw')
param natGwIdleTimeout int = 30

@description('Configures the cluster as an OIDC issuer for use with Workload Identity')
param oidcIssuer bool = false

@description('Installs Azure Workload Identity into the cluster')
param workloadIdentity bool = false

param warIngressNginx bool = false

@description('System Pool presets are derived from the recommended system pool specs')
var systemPoolPresets = {
  CostOptimised : {
    vmSize: 'Standard_B4ms'
    count: 1
    minCount: 1
    maxCount: 3
    enableAutoScaling: true
    availabilityZones: []
  }
  Standard : {
    vmSize: 'Standard_DS2_v2'
    count: 3
    minCount: 3
    maxCount: 5
    enableAutoScaling: true
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
  HighSpec : {
    vmSize: 'Standard_D4s_v3'
    count: 3
    minCount: 3
    maxCount: 5
    enableAutoScaling: true
    availabilityZones: [
      '1'
      '2'
      '3'
    ]
  }
}

var systemPoolBase = {
  name: 'npsystem'
  mode: 'System'
  osType: 'Linux'
  maxPods: 30
  type: 'VirtualMachineScaleSets'
  vnetSubnetID: !empty(aksSubnetId) ? aksSubnetId : json('null')
  upgradeSettings: {
    maxSurge: '33%'
  }
  nodeTaints: [
    JustUseSystemPool ? '' : 'CriticalAddonsOnly=true:NoSchedule'
  ]
}

var userPoolVmProfile = {
  vmSize: agentVMSize
  count: agentCount
  minCount: autoScale ? agentCount : json('null')
  maxCount: autoScale ? agentCountMax : json('null')
  enableAutoScaling: autoScale
  availabilityZones: !empty(availabilityZones) ? availabilityZones : null
}

var agentPoolProfileUser = union({
  name: 'npuser01'
  mode: 'User'
  osDiskType: osDiskType
  osDiskSizeGB: osDiskSizeGB
  osType: 'Linux'
  maxPods: maxPods
  type: 'VirtualMachineScaleSets'
  vnetSubnetID: !empty(aksSubnetId) ? aksSubnetId : json('null')
  upgradeSettings: {
    maxSurge: '33%'
  }
}, userPoolVmProfile)

var agentPoolProfiles = JustUseSystemPool ? array(union(systemPoolBase, userPoolVmProfile)) : concat(array(union(systemPoolBase, SystemPoolType=='Custom' && SystemPoolCustomPreset != {} ? SystemPoolCustomPreset : systemPoolPresets[SystemPoolType])), array(agentPoolProfileUser))

var akssku = AksPaidSkuForSLA ? 'Paid' : 'Free'


var aks_addons = union({
  azurepolicy: {
    config: {
      version: !empty(azurepolicy) ? 'v2' : json('null')
    }
    enabled: !empty(azurepolicy)
  }
  azureKeyvaultSecretsProvider: {
    config: {
      enableSecretRotation: 'true'
      rotationPollInterval: keyVaultAksCSIPollInterval
    }
    enabled: keyVaultAksCSI
  }
  openServiceMesh: {
    enabled: openServiceMeshAddon
    config: {}
  }
}, createLaw && omsagent ? {
  omsagent: {
    enabled: createLaw && omsagent
    config: {
      logAnalyticsWorkspaceResourceID: createLaw && omsagent ? aks_law.id : json('null')
    }
  }} : {})

var aks_addons1 = ingressApplicationGateway ? union(aks_addons, deployAppGw ? {
  ingressApplicationGateway: {
    config: {
      applicationGatewayId: appgw.id
    }
    enabled: true
  }
} : {
  // AKS RP will deploy the AppGateway for us (not creating custom or BYO VNET)
  ingressApplicationGateway: {
    enabled: true
    config: {
      applicationGatewayName: appgwName
      subnetCIDR: '10.2.0.0/16'
    }
  }
}) : aks_addons


var aks_identity = {
  type: 'UserAssigned'
  userAssignedIdentities: {
    '${aksUai.id}': {}
  }
}

@description('Sets the private dns zone id if provided')
var aksPrivateDnsZone = privateClusterDnsMethod=='privateDnsZone' ? (!empty(dnsApiPrivateZoneId) ? dnsApiPrivateZoneId : 'system') : privateClusterDnsMethod
output aksPrivateDnsZone string = aksPrivateDnsZone


@description('Needing to seperately declare and union this because of https://github.com/Azure/AKS-Construction/issues/344')
var managedNATGatewayProfile =  {
  natGatewayProfile : {
    managedOutboundIPProfile: {
      count: natGwIpCount
    }
    idleTimeoutInMinutes: natGwIdleTimeout
  }
}

@description('Needing to seperately declare and union this because of https://github.com/Azure/AKS/issues/2774')
var azureDefenderSecurityProfile = {
  securityProfile : {
    defender: {
      logAnalyticsWorkspaceResourceId: createLaw ? aks_law.id : null
      securityMonitoring: {
        enabled: defenderForContainers
      }
    }
  }
}

var aksProperties = union({
  kubernetesVersion: kubernetesVersion
  enableRBAC: true
  dnsPrefix: dnsPrefix
  aadProfile: enable_aad ? {
    managed: true
    enableAzureRBAC: enableAzureRBAC
    tenantID: aad_tenant_id
  } : null
  apiServerAccessProfile: !empty(authorizedIPRanges) ? {
    authorizedIPRanges: authorizedIPRanges
  } : {
    enablePrivateCluster: enablePrivateCluster
    privateDNSZone: enablePrivateCluster ? aksPrivateDnsZone : ''
    enablePrivateClusterPublicFQDN: enablePrivateCluster && privateClusterDnsMethod=='none'
  }
  agentPoolProfiles: agentPoolProfiles
  workloadAutoScalerProfile: {
    keda: {
        enabled: kedaAddon
    }
  }
  networkProfile: {
    loadBalancerSku: 'standard'
    networkPlugin: networkPlugin
    #disable-next-line BCP036 //Disabling validation of this parameter to cope with empty string to indicate no Network Policy required.
    networkPolicy: networkPolicy
    podCidr: networkPlugin=='kubenet' ? podCidr : json('null')
    serviceCidr: serviceCidr
    dnsServiceIP: dnsServiceIP
    dockerBridgeCidr: dockerBridgeCidr
    outboundType: aksOutboundTrafficType
  }
  disableLocalAccounts: AksDisableLocalAccounts && enable_aad
  autoUpgradeProfile: {upgradeChannel: upgradeChannel}
  addonProfiles: !empty(aks_addons1) ? aks_addons1 : aks_addons
  autoScalerProfile: autoScale ? AutoscaleProfile : {}
  oidcIssuerProfile: {
    enabled: oidcIssuer
  }
  securityProfile: {
    workloadIdentity: {
      enabled: workloadIdentity
    }
  }
  ingressProfile: {
    webAppRouting: {
      enabled: warIngressNginx
    }
  }
  storageProfile: {
    blobCSIDriver: {
      enabled: blobCSIAddon
    }
  }
},
aksOutboundTrafficType == 'managedNATGateway' ? managedNATGatewayProfile : {},
defenderForContainers && createLaw ? azureDefenderSecurityProfile : {}
)

resource aks 'Microsoft.ContainerService/managedClusters@2022-05-02-preview' = {
  name: 'aks-${resourceName}'
  location: location
  properties: aksProperties
  identity: createAksUai ? aks_identity : {
    type: 'SystemAssigned'
  }
  sku: {
    name: 'Basic'
    tier: akssku
  }
  dependsOn: [
    privateDnsZoneRbac
  ]
}
output aksClusterName string = aks.name
output aksOidcIssuerUrl string = oidcIssuer ? aks.properties.oidcIssuerProfile.issuerURL : ''
output aksNodeResourceGroup string = aks.properties.nodeResourceGroup
//output aksNodePools array = [for nodepool in agentPoolProfiles: name]

@description('Not giving Rbac at the vnet level when using private dns results in ReconcilePrivateDNS. Therefore we need to upgrade the scope when private dns is being used, because it wants to set up the dns->vnet integration.')
var uaiNetworkScopeRbac = enablePrivateCluster && !empty(dnsApiPrivateZoneId) ? 'Vnet' : 'Subnet'
module privateDnsZoneRbac './dnsZoneRbac.bicep' = if (enablePrivateCluster && !empty(dnsApiPrivateZoneId) && createAksUai) {
  name: 'addPrivateK8sApiDnsContributor'
  params: {
    vnetId: ''
    dnsZoneId: dnsApiPrivateZoneId
    principalId: createAksUai ? aksUai.properties.principalId : ''
  }
}

var policySetBaseline = '/providers/Microsoft.Authorization/policySetDefinitions/a8640138-9b0a-4a28-b8cb-1666c838647d'
var policySetRestrictive = '/providers/Microsoft.Authorization/policySetDefinitions/42b8ef37-b724-4e24-bbc8-7a7708edfe00'

resource aks_policies 'Microsoft.Authorization/policyAssignments@2020-09-01' = if (!empty(azurepolicy)) {
  name: '${resourceName}-${azurePolicyInitiative}'
  location: location
  properties: {
    policyDefinitionId: azurePolicyInitiative == 'Baseline' ? policySetBaseline : policySetRestrictive
    parameters: {
      excludedNamespaces: {
        value: [
            'kube-system'
            'gatekeeper-system'
            'azure-arc'
            'cluster-baseline-setting'
        ]
      }
      effect: {
        value: azurepolicy
      }
    }
    metadata: {
      assignedBy: 'Aks Construction'
    }
    displayName: 'Kubernetes cluster pod security ${azurePolicyInitiative} standards for Linux-based workloads'
    description: 'As per: https://github.com/Azure/azure-policy/blob/master/built-in-policies/policySetDefinitions/Kubernetes/'
  }
}

@description('If automated deployment, for the 3 automated user assignments, set Principal Type on each to "ServicePrincipal" rarter than "User"')
param automatedDeployment bool = false

@description('The principal ID to assign the AKS admin role.')
param adminPrincipalId string = ''
// for AAD Integrated Cluster wusing 'enableAzureRBAC', add Cluster admin to the current user!
var buildInAKSRBACClusterAdmin = resourceId('Microsoft.Authorization/roleDefinitions', 'b1ff04bb-8a4e-4dc4-8eb5-8693973ce19b')
resource aks_admin_role_assignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (enableAzureRBAC && !empty(adminPrincipalId)) {
  scope: aks // Use when specifying a scope that is different than the deployment scope
  name: guid(aks.id, 'aksadmin', buildInAKSRBACClusterAdmin)
  properties: {
    roleDefinitionId: buildInAKSRBACClusterAdmin
    principalType: automatedDeployment ? 'ServicePrincipal' : 'User'
    principalId: adminPrincipalId
  }
}

param fluxGitOpsAddon bool = false

resource fluxAddon 'Microsoft.KubernetesConfiguration/extensions@2022-04-02-preview' = if(fluxGitOpsAddon) {
  name: 'flux'
  scope: aks
  properties: {
    extensionType: 'microsoft.flux'
    autoUpgradeMinorVersion: true
    releaseTrain: 'Stable'
    scope: {
      cluster: {
        releaseNamespace: 'flux-system'
      }
    }
    configurationProtectedSettings: {}
  }
  dependsOn: [daprExtension] //Chaining dependencies because of: https://github.com/Azure/AKS-Construction/issues/385
}
output fluxReleaseNamespace string = fluxGitOpsAddon ? fluxAddon.properties.scope.cluster.releaseNamespace : ''

@description('Add the Dapr extension')
param daprAddon bool = false
@description('Enable high availability (HA) mode for the Dapr control plane')
param daprAddonHA bool = false

resource daprExtension 'Microsoft.KubernetesConfiguration/extensions@2022-04-02-preview' = if(daprAddon) {
    name: 'dapr'
    scope: aks
    properties: {
        extensionType: 'Microsoft.Dapr'
        autoUpgradeMinorVersion: true
        releaseTrain: 'Stable'
        configurationSettings: {
            'global.ha.enabled': '${daprAddonHA}'
        }
        scope: {
          cluster: {
            releaseNamespace: 'dapr-system'
          }
        }
        configurationProtectedSettings: {}
    }
}

output daprReleaseNamespace string = daprAddon ? daprExtension.properties.scope.cluster.releaseNamespace : ''

/*__  ___.   ______   .__   __.  __  .___________.  ______   .______       __  .__   __.   _______
|   \/   |  /  __  \  |  \ |  | |  | |           | /  __  \  |   _  \     |  | |  \ |  |  /  _____|
|  \  /  | |  |  |  | |   \|  | |  | `---|  |----`|  |  |  | |  |_)  |    |  | |   \|  | |  |  __
|  |\/|  | |  |  |  | |  . `  | |  |     |  |     |  |  |  | |      /     |  | |  . `  | |  | |_ |
|  |  |  | |  `--'  | |  |\   | |  |     |  |     |  `--'  | |  |\  \----.|  | |  |\   | |  |__| |
|__|  |__|  \______/  |__| \__| |__|     |__|      \______/  | _| `._____||__| |__| \__|  \______| */


@description('Diagnostic categories to log')
param AksDiagCategories array = [
  'cluster-autoscaler'
  'kube-controller-manager'
  'kube-audit-admin'
  'guard'
]

resource AksDiags 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' =  if (createLaw && omsagent)  {
  name: 'aksDiags'
  scope: aks
  properties: {
    workspaceId: aks_law.id
    logs: [for aksDiagCategory in AksDiagCategories: {
      category: aksDiagCategory
      enabled: true
    }]
    metrics: [
      {
        category: 'AllMetrics'
        enabled: true
      }
    ]
  }
}

@description('Enable Metric Alerts')
param createAksMetricAlerts bool = true

@allowed([
  'Short'
  'Long'
])
@description('Which Metric polling frequency model to use')
param AksMetricAlertMetricFrequencyModel string = 'Long'

var AlertFrequencyLookup = {
  Short: {
    evalFrequency: 'PT1M'
    windowSize: 'PT5M'
  }
  Long: {
    evalFrequency: 'PT15M'
    windowSize: 'PT1H'
  }
}
var AlertFrequency = AlertFrequencyLookup[AksMetricAlertMetricFrequencyModel]

module aksmetricalerts './aksmetricalerts.bicep' = if (createLaw) {
  name: 'aksmetricalerts'
  scope: resourceGroup()
  params: {
    clusterName: aks.name
    logAnalyticsWorkspaceName: aks_law.name
    metricAlertsEnabled: createAksMetricAlerts
    evalFrequency: AlertFrequency.evalFrequency
    windowSize: AlertFrequency.windowSize
    alertSeverity: 'Informational'
    logAnalyticsWorkspaceLocation: location
  }
}

//---------------------------------------------------------------------------------- Container Insights

@description('The Log Analytics retention period')
param retentionInDays int = 30

var aks_law_name = 'log-${resourceName}'

var createLaw = (omsagent || deployAppGw || azureFirewalls || CreateNetworkSecurityGroups || defenderForContainers)

resource aks_law 'Microsoft.OperationalInsights/workspaces@2021-06-01' = if (createLaw) {
  name: aks_law_name
  location: location
  properties: {
    retentionInDays: retentionInDays
  }
}

//This role assignment enables AKS->LA Fast Alerting experience
var MonitoringMetricsPublisherRole = resourceId('Microsoft.Authorization/roleDefinitions', '3913510d-42f4-4e42-8a64-420c390055eb')
resource FastAlertingRole_Aks_Law 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (omsagent) {
  scope: aks
  name: guid(aks.id, 'omsagent', MonitoringMetricsPublisherRole)
  properties: {
    roleDefinitionId: MonitoringMetricsPublisherRole
    principalId: aks.properties.addonProfiles.omsagent.identity.objectId
    principalType: 'ServicePrincipal'
  }
}

output LogAnalyticsName string = (createLaw) ? aks_law.name : ''
output LogAnalyticsGuid string = (createLaw) ? aks_law.properties.customerId : ''
output LogAnalyticsId string = (createLaw) ? aks_law.id : ''

//---------------------------------------------------------------------------------- AKS events with Event Grid
// Supported events : https://docs.microsoft.com/en-gb/azure/event-grid/event-schema-aks?tabs=event-grid-event-schema#available-event-types

@description('Create an Event Grid System Topic for AKS events')
param createEventGrid bool = false

resource eventGrid 'Microsoft.EventGrid/systemTopics@2021-12-01' = if(createEventGrid) {
  name: 'evgt-${aks.name}'
  location: location
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    source: aks.id
    topicType: 'Microsoft.ContainerService.ManagedClusters'
  }
}

output eventGridName string = createEventGrid ? eventGrid.name : ''

resource eventGridDiags 'Microsoft.Insights/diagnosticSettings@2021-05-01-preview' = if (createLaw && createEventGrid) {
  name: 'eventGridDiags'
  scope: eventGrid
  properties: {
    workspaceId:aks_law.id
    logs: [
      {
        category: 'DeliveryFailures'
        enabled: true
      }
    ]
    metrics: [
      {
        category: 'AllMetrics'
        enabled: true
      }
    ]
  }
}


//ACSCII Art link : https://textkool.com/en/ascii-art-generator?hl=default&vl=default&font=Star%20Wars&text=changeme
