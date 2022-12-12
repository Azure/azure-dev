@description('Server Name for Azure database for MySQL')
param name string
@description('Location for all resources.')
param location string = resourceGroup().location
param tags object = {}

param keyVaultName string
@description('Database administrator login name')
@minLength(1)
param mysqlAdminName string

param mysqlAdminPassKey string = 'MYSQL-PASS'

@description('Database administrator password')
@minLength(8)
@secure()
param mysqlAdminPassword string

@description('Azure database for MySQL sku name ')
param skuName string = 'Standard_B1s'

@description('Azure database for MySQL storage Size ')
param StorageSizeGB int = 20

@description('Azure database for MySQL storage Iops')
param StorageIops int = 360

@description('Azure database for MySQL pricing tier')
@allowed([
  'GeneralPurpose'
  'MemoryOptimized'
  'Burstable'
])
param SkuTier string = 'Burstable'

@description('MySQL version')
@allowed([
  '5.7'
  '8.0.21'
])
param mysqlVersion string = '8.0.21'

@description('MySQL Server backup retention days')
param backupRetentionDays int = 7

@description('Geo-Redundant Backup setting')
param geoRedundantBackup string = 'Disabled'

resource mysqlServer 'Microsoft.DBforMySQL/flexibleServers@2021-05-01' = {
  name: name
  location: location
  tags: tags
  sku: {
    name: skuName
    tier: SkuTier
  }
  properties: {
    administratorLogin: mysqlAdminName
    administratorLoginPassword: mysqlAdminPassword
    storage: {
      autoGrow: 'Enabled'
      iops: StorageIops
      storageSizeGB: StorageSizeGB
    }
    createMode: 'Default'
    version: mysqlVersion
    backup: {
      backupRetentionDays: backupRetentionDays
      geoRedundantBackup: geoRedundantBackup
    }
    highAvailability: {
      mode: 'Disabled'
    }
  }
}

resource firewallRule_all_azure_ips 'Microsoft.DBforMySQL/flexibleServers/firewallRules@2021-05-01' = {
  parent: mysqlServer
  name: 'AllowAzureIPs'
  properties: {
    startIpAddress: '0.0.0.0'
    endIpAddress: '0.0.0.0'
  }
}

resource mysqlAdminPasswordSecret 'Microsoft.KeyVault/vaults/secrets@2022-07-01' = {
  parent: keyVault
  name: mysqlAdminPassKey
  properties: {
    value: mysqlAdminPassword
  }
}

resource keyVault 'Microsoft.KeyVault/vaults@2022-07-01' existing = {
  name: keyVaultName
}

output name string = mysqlServer.name
output mysqlAdminName string = mysqlAdminName
output mysqlAdminPassKey string = mysqlAdminPassKey
output jdbcUrl string = 'jdbc:mysql://${mysqlServer.properties.fullyQualifiedDomainName}:3306/?useSSL=true&requireSSL=false'
