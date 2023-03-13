param name string
param location string = resourceGroup().location
param tags object = {}

param sku object
param storage object
param administratorLogin string
@secure()
param administratorLoginPassword string
param activeDirectoryAuth string = 'Disabled'
param databaseNames array = []
param allowAzureIPsFirewall bool = false
param allowAllIPsFirewall bool = false
param allowedSingleIPs array = []

param highAvailabilityMode string = 'Disabled'

param backupRetentionDays int = 7
param geoRedundantBackup string = 'Disabled'

param maintenanceWindowCustomWindow string = 'Disabled'
param maintenanceWindowDayOfWeek int = 0
param maintenanceWindowStartHour int = 0
param maintenanceWindowStartMinute int = 0

// PostgreSQL version
param version string

// Latest official version 2022-12-01 does not have Bicep types available
resource postgresServer 'Microsoft.DBforPostgreSQL/flexibleServers@2022-12-01' = {
  location: location
  tags: tags
  name: name
  sku: sku
  properties: {
    version: version
    administratorLogin: administratorLogin
    administratorLoginPassword: administratorLoginPassword
    storage: storage
    highAvailability: {
      mode: highAvailabilityMode
    }
    authConfig: {
      activeDirectoryAuth: activeDirectoryAuth
      passwordAuth: (administratorLoginPassword == null) ? 'Disabled' : 'Enabled'
    }
    backup: {
      backupRetentionDays: backupRetentionDays
      geoRedundantBackup: geoRedundantBackup
    }
    maintenanceWindow: {
      customWindow: maintenanceWindowCustomWindow
      dayOfWeek: maintenanceWindowDayOfWeek
      startHour: maintenanceWindowStartHour
      startMinute: maintenanceWindowStartMinute
    }
  }

  resource database 'databases' = [for name in databaseNames: {
    name: name
  }]

  resource firewall_all 'firewallRules' = if (allowAllIPsFirewall) {
    name: 'allow-all-IPs'
    properties: {
      startIpAddress: '0.0.0.0'
      endIpAddress: '255.255.255.255'
    }
  }

  resource firewall_azure 'firewallRules' = if (allowAzureIPsFirewall) {
    name: 'allow-all-azure-internal-IPs'
    properties: {
      startIpAddress: '0.0.0.0'
      endIpAddress: '0.0.0.0'
    }
  }

  resource firewall_single 'firewallRules' = [for ip in allowedSingleIPs: {
    name: 'allow-single-${replace(ip, '.', '')}'
    properties: {
      startIpAddress: ip
      endIpAddress: ip
    }
  }]

}

output POSTGRES_DOMAIN_NAME string = postgresServer.properties.fullyQualifiedDomainName
