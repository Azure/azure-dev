terraform {
  required_providers {
    azurerm = {
      version = "~>3.18.0"
      source  = "hashicorp/azurerm"
    }
    azurecaf = {
      source  = "aztfmod/azurecaf"
      version = "~>1.2.15"
    }
  }
}

# ------------------------------------------------------------------------------------------------------
# Deploy apim-api service 
# ------------------------------------------------------------------------------------------------------
resource "restApi" "api" {
  name   = apiName
  parent = apimService
  properties{
    description          = apiDescription
    displayName          = apiDisplayName
    path                 = apiPath
    protocols            = [ "https"]
    subscriptionRequired = false
    type                 = "http"
    format               = "openapi"
    serviceUrl           = API_ENDPOINT
    value                = loadTextContent("../../src/api/openapi.yaml")
  }
}

resource "apiPolicy" "policies"{
  name   = "policy"
  parent = "restApi"
  properties {
    format = "rawxml"
    value  = apiPolicyContent
  }
}

resource "apiDiagnostics" "diagnostics"{
  name   = "applicationinsights"
  parent = restApi
  properties {
    alwaysLog = "llErrors"
    backend {
      request {
        body {
          bytes = 1024
        }
      }
      response {
        body {
          bytes = 1024
        }
      }
    }
    frontend {
      request {
        body {
          bytes = 1024
        }
      }
      response {
        body {
          bytes = 1024
        }
      }
    }
    httpCorrelationProtocol = "W3C"
    logClientIp             = true
    loggerId                = apimLogger.id
    metrics                 = true
    sampling {
      percentage = 100
      samplingType = "fixed"
    }
    verbosity = "verbose"
  }
}

resource "apimService" {
  name = name
}

resource "apimLogger" {
  name   = "app-insights-logger"
  parent = apimService
}