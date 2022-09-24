param environmentName string
param location string = resourceGroup().location

param allowedOrigins array = []
param applicationInsightsName string
param keyVaultName string
param serviceName string = 'api'

output API_IDENTITY_PRINCIPAL_ID string = ''
output API_NAME string = ''
output API_URI string = ''
