metadata description = 'Creates an Azure Kubernetes Service (AKS) cluster with a system agent pool.'
@description('The name for the AKS managed cluster')
param name string

@description('The name of the resource group for the managed resources of the AKS cluster')
param nodeResourceGroupName string

@description('The Azure region/location for the AKS resources')
param location string = resourceGroup().location

@description('Custom tags to apply to the AKS resources')
param tags object = {}

@description('Kubernetes Version')
param kubernetesVersion string = '1.28'

@description('The DNS prefix to associate with the AKS cluster')
param dnsPrefix string = ''

@description('The object IDs of the Azure AD groups that will have admin access to the AKS cluster')
param adminGroupObjectIDs array = []

resource aks 'Microsoft.ContainerService/managedClusters@2024-03-02-preview' = {
  name: name
  location: location
  tags: tags
  identity: {
    type: 'SystemAssigned'
  }
  sku: {
    name: 'Automatic'
    tier: 'Standard'
  }
  properties: {
    nodeResourceGroup: !empty(nodeResourceGroupName) ? nodeResourceGroupName : 'rg-mc-${name}'
    nodeResourceGroupProfile: {
      restrictionLevel: 'ReadOnly'
    }
    nodeProvisioningProfile: {
      mode: 'Auto'
    }
    disableLocalAccounts: true
    aadProfile: {
      managed: true
      enableAzureRBAC: true
      adminGroupObjectIDs: adminGroupObjectIDs
    }
    autoUpgradeProfile: {
      upgradeChannel: 'stable'
      nodeOSUpgradeChannel: 'NodeImage'
    }
    kubernetesVersion: kubernetesVersion
    dnsPrefix: empty(dnsPrefix) ? '${name}-dns' : dnsPrefix
    enableRBAC: true
    agentPoolProfiles: [
      {
        name: 'systempool'
        mode: 'System'
        vmSize: 'Standard_DS4_v2'
        count: 3
        securityProfile: {
          sshAccess: 'Disabled'
        }
      }
    ]
    supportPlan: 'KubernetesOfficial'
    addonProfiles: {}
  }

  resource aksManagedAutoUpgradeSchedule 'maintenanceConfigurations@2023-10-01' = {
    name: 'aksManagedAutoUpgradeSchedule'
    properties: {
      maintenanceWindow: {
        schedule: {
          daily: null
          weekly: {
            intervalWeeks: 1
            dayOfWeek: 'Sunday'
          }
          absoluteMonthly: null
          relativeMonthly: null
        }
        durationHours: 4
        utcOffset: '+00:00'
        startDate: '2024-07-03'
        startTime: '00:00'
      }
    }
  }
}

@description('The resource name of the AKS cluster')
output clusterName string = aks.name

@description('The AKS cluster identity')
output clusterIdentity object = {
  clientId: aks.properties.identityProfile.kubeletidentity.clientId
  objectId: aks.properties.identityProfile.kubeletidentity.objectId
  resourceId: aks.properties.identityProfile.kubeletidentity.resourceId
}
