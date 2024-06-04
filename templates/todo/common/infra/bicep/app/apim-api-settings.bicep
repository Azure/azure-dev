@description('Resource name for the existing apim service')
param name string

@description('Resource name to uniquely identify this API within the API Management service instance')
@minLength(1)
param apiName string

@description('Relative URL uniquely identifying this API and all of its resource paths within the API Management service instance. It is appended to the API endpoint base URL specified during the service instance creation to form a public URL for this API.')
@minLength(1)
param apiPath string

@description('Resource name for the existing applicationInsights service')
param applicationInsightsName string

@description('Resource name for backend Web App or Function App')
param apiAppName string = ''

// Necessary due to https://github.com/Azure/bicep/issues/9594
// placeholderName is never deployed, it is merely used to make the child name validation pass
var appNameForBicep = !empty(apiAppName) ? apiAppName : 'placeholderName'

resource apiDiagnostics 'Microsoft.ApiManagement/service/apis/diagnostics@2021-12-01-preview' = {
  name: 'applicationinsights'
  parent: apimService::restApi
  properties: {
    alwaysLog: 'allErrors'
    backend: {
      request: {
        body: {
          bytes: 1024
        }
      }
      response: {
        body: {
          bytes: 1024
        }
      }
    }
    frontend: {
      request: {
        body: {
          bytes: 1024
        }
      }
      response: {
        body: {
          bytes: 1024
        }
      }
    }
    httpCorrelationProtocol: 'W3C'
    logClientIp: true
    loggerId: apimLogger.id
    metrics: true
    sampling: {
      percentage: 100
      samplingType: 'fixed'
    }
    verbosity: 'verbose'
  }
}

resource apiAppProperties 'Microsoft.Web/sites/config@2022-03-01' = if (!empty(apiAppName)) {
  name: '${appNameForBicep}/web'
  kind: 'string'
  properties: {
      apiManagementConfig: {
        id: '${apimService.id}/apis/${apiName}'
      }
  }
}

resource apimLogger 'Microsoft.ApiManagement/service/loggers@2021-12-01-preview' = if (!empty(applicationInsightsName)) {
  name: 'app-insights-logger'
  parent: apimService
  properties: {
    credentials: {
      instrumentationKey: applicationInsights.properties.InstrumentationKey
    }
    description: 'Logger to Azure Application Insights'
    isBuffered: false
    loggerType: 'applicationInsights'
    resourceId: applicationInsights.id
  }
}

resource applicationInsights 'Microsoft.Insights/components@2020-02-02' existing = if (!empty(applicationInsightsName)) {
  name: applicationInsightsName
}

resource apimService 'Microsoft.ApiManagement/service@2021-08-01' existing = {
  name: name

  resource restApi 'apis@2021-12-01-preview' existing = {
    name: apiName
    }
}

output SERVICE_API_URI string = '${apimService.properties.gatewayUrl}/${apiPath}'
