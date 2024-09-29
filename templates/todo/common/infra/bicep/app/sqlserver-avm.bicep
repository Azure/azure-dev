param location string = resourceGroup().location

@description('Application user name')
param appUser string 

@description('SQL Server administrator name')
param sqlAdmin string = 'sqlAdmin'

@description('The name for sql database ')
param sqlDatabaseName string = ''

@description('Resource name for sql service')
param sqlServiceName string

@secure()
@description('SQL Server administrator password')
param sqlAdminPassword string

@secure()
@description('Application user password')
param appUserPassword string

param tags object = {}

var defaultDatabaseName = 'Todo'
var actualDatabaseName = !empty(sqlDatabaseName) ? sqlDatabaseName : defaultDatabaseName

module sqlServer 'br/public:avm/res/sql/server:0.2.0' = {
  name: 'sqlservice'
  params: {
    name: sqlServiceName
    administratorLogin: sqlAdmin
    administratorLoginPassword: sqlAdminPassword
    location: location
    tags: tags
    publicNetworkAccess: 'Enabled'
    databases: [
      {
        name: actualDatabaseName
      }
    ]
    firewallRules: [
      {
        name: 'Azure Services'
        startIpAddress: '0.0.0.1'
        endIpAddress: '255.255.255.254'
      }
    ]
  }
}

module deploymentScript 'br/public:avm/res/resources/deployment-script:0.1.3' = {
  name: 'deployment-script'
  params: {
    kind: 'AzureCLI'
    name: 'deployment-script'
    azCliVersion: '2.37.0'
    location: location
    retentionInterval: 'PT1H'
    timeout: 'PT5M'
    cleanupPreference: 'OnSuccess'
    environmentVariables:{
      secureList: [
        {
          name: 'APPUSERNAME'
          value: appUser
        }
        {
          name: 'APPUSERPASSWORD'
          secureValue: appUserPassword
        }
        {
          name: 'DBNAME'
          value: actualDatabaseName
        }
        {
          name: 'DBSERVER'
          value: '${sqlServer.outputs.name}${environment().suffixes.sqlServerHostname}'
        }
        {
          name: 'SQLCMDPASSWORD'
          secureValue: sqlAdminPassword
        }
        {
          name: 'SQLADMIN'
          value: sqlAdmin
        }
      ]
    }
    scriptContent: '''
wget https://github.com/microsoft/go-sqlcmd/releases/download/v0.8.1/sqlcmd-v0.8.1-linux-x64.tar.bz2
tar x -f sqlcmd-v0.8.1-linux-x64.tar.bz2 -C .

cat <<SCRIPT_END > ./initDb.sql
drop user if exists ${APPUSERNAME}
go
create user ${APPUSERNAME} with password = '${APPUSERPASSWORD}'
go
alter role db_owner add member ${APPUSERNAME}
go
SCRIPT_END

./sqlcmd -S ${DBSERVER} -d ${DBNAME} -U ${SQLADMIN} -i ./initDb.sql
    '''
  }
}

output databaseName string = actualDatabaseName
output sqlServerName string = sqlServer.outputs.name
