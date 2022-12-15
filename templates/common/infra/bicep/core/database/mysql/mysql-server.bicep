@description('Server Name for Azure database for MySQL')
param name string
@description('Location for all resources.')
param location string = resourceGroup().location
param tags object = {}

param keyVaultName string

@description('Database administrator login name')
@minLength(1)
param adminName string

param adminPassKey string = 'MYSQL-PASS'

@description('Database administrator password')
@minLength(8)
@secure()
param adminPassword string

@description('Azure database for MySQL sku name ')
param skuName string = 'Standard_B1s'

@description('Enable Storage Auto Grow or not')
param enableAutoGrow bool = true

@description('Azure database for MySQL storage Size ')
param storageSizeGB int = 20

@description('Azure database for MySQL storage Iops')
param storageIops int = 360

@description('Azure database for MySQL pricing tier')
@allowed([
  'GeneralPurpose'
  'MemoryOptimized'
  'Burstable'
])
param skuTier string = 'Burstable'

@description('MySQL version')
@allowed([
  '5.7'
  '8.0.21'
])
param version string = '8.0.21'

@description('MySQL Server backup retention days')
param backupRetentionDays int = 7

@description('Geo-Redundant Backup setting')
param geoRedundantBackup string = 'Disabled'

@allowed([
  'Disabled'
  'ZoneRedundant'
  'SameZone'
])
param highAvailabilityMode string = 'Disabled'

resource server 'Microsoft.DBforMySQL/flexibleServers@2021-05-01' = {
  name: name
  location: location
  tags: tags
  sku: {
    name: skuName
    tier: skuTier
  }
  properties: {
    administratorLogin: adminName
    administratorLoginPassword: adminPassword
    storage: {
      autoGrow: enableAutoGrow ? 'Enabled' : 'Disabled'
      iops: storageIops
      storageSizeGB: storageSizeGB
    }
    createMode: 'Default'
    version: version
    backup: {
      backupRetentionDays: backupRetentionDays
      geoRedundantBackup: geoRedundantBackup
    }
    highAvailability: {
      mode: highAvailabilityMode
    }
  }
}

resource firewallRuleAllowAllAzureIps 'Microsoft.DBforMySQL/flexibleServers/firewallRules@2021-05-01' = {
  parent: server
  name: 'AllowAzureIPs'
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '0.0.0.0'
  }
}

resource mySQLAdminPasswordSecret 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  parent: keyVault
  name: adminPassKey
  properties: {
    value: adminPassword
  }
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: keyVaultName
}

output name string = server.name
output adminName string = adminName
output adminPassKey string = adminPassKey
output fullyQualifiedDomainName string = server.properties.fullyQualifiedDomainName
output endpoint string = 'jdbc:mysql://${server.properties.fullyQualifiedDomainName}:3306/?useSSL=true&requireSSL=false'
