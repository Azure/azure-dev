param environmentName string
param location string = resourceGroup().location

param serviceName string = 'web'
param appCommandLine string = 'pm2 serve /home/site/wwwroot --no-daemon --spa'
param applicationInsightsName string

output WEB_IDENTITY_PRINCIPAL_ID string = ''
output WEB_NAME string = ''
output WEB_URI string = ''
