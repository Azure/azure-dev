param environmentName string
param location string = resourceGroup().location
param allowedOrigins array

module api 'function.bicep' = {
  name: 'api-python-function-resources'
  params: {
    environmentName: environmentName
    location: location
    allowedOrigins: allowedOrigins
    linuxFxVersion: 'PYTHON|3.8'
    functionsWorkerRuntime: 'python'
  }
}

output API_URI string = api.outputs.API_URI
