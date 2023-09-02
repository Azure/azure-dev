using './main.bicep'

param environmentName = readEnvironmentVariable('AZURE_ENV_NAME')
param location = readEnvironmentVariable('AZURE_LOCATION')
param intTagValue = int(readEnvironmentVariable('INT_TAG_VALUE', '678'))
param boolTagValue = bool(readEnvironmentVariable('BOOL_TAG_VALUE', 'false'))
