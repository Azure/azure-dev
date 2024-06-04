param location string = resourceGroup().location

@description('Application user name')
param appUser string = 'appUser'

@description('SQL Server administrator name')
param sqlAdmin string = 'sqlAdmin'

@description('The name for sql database ')
param sqlDatabaseName string

@description('Resource name for sql service')
param sqlServiceName string

@secure()
@description('SQL Server administrator password')
param sqlAdminPassword string

@secure()
@description('Application user password')
param appUserPassword string

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
          value: !empty(sqlDatabaseName) ? sqlDatabaseName : 'Todo'
        }
        {
          name: 'DBSERVER'
          value: '${sqlServiceName}${environment().suffixes.sqlServerHostname}'
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
