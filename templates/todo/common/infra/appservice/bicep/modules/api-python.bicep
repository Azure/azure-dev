param environmentName string
param location string = resourceGroup().location

module apiResources 'api.bicep' = {
  name: 'api-appservice-python-resources'
  params: {
    environmentName: environmentName
    location: location
    linuxFxVersion: 'PYTHON|3.8'
    appCommandLine: 'gunicorn --workers 4 --threads 2 --timeout 60 --access-logfile "-" --error-logfile "-" --bind=0.0.0.0:8000 -k uvicorn.workers.UvicornWorker todo.app:app'
  }
}

output API_PRINCIPAL_ID string = apiResources.outputs.API_PRINCIPAL_ID
output API_URI string = apiResources.outputs.API_URI
