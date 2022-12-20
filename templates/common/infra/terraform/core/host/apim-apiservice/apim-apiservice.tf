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
# Deploy app service web app
# ------------------------------------------------------------------------------------------------------
# resource "azurecaf_name" "web_name" {
#   name          = "${var.service_name}-${var.resource_token}"
#   resource_type = "azurerm_app_service"
#   random_length = 0
#   clean_input   = true
# }

# resource "azurerm_linux_web_app" "web" {
#   name                = azurecaf_name.web_name.result
#   location            = var.location
#   resource_group_name = var.rg_name
#   service_plan_id     = var.appservice_plan_id
#   https_only          = true
#   tags                = var.tags

#   site_config {
#     always_on        = true
#     ftps_state       = "FtpsOnly"
#     app_command_line = var.app_command_line
#     application_stack {
#       python_version = var.python_version
#     }
#   }

#   app_settings = var.app_settings

#   dynamic "identity" {
#     for_each = { for k, v in var.identity : k => v if var.identity != [] }
#     content {
#       type = identity.value["type"]
#     }
#   }

#   logs {
#     application_logs {
#       file_system_level = "Verbose"
#     }
#     detailed_error_messages = true
#     failed_request_tracing  = true
#     http_logs {
#       file_system {
#         retention_in_days = 1
#         retention_in_mb   = 35
#       }
#     }
#   }
# }

# ------------------------------------------------------------------------------------------------------
# Deploy apim-api service 
# ------------------------------------------------------------------------------------------------------
# 未完成
resource restApi 'Microsoft.ApiManagement/service/apis@2021-12-01-preview' = {
  name: apiName
  parent: apimService
  properties: {
    description: apiDescription
    displayName: apiDisplayName
    path: apiPath
    protocols: [ 'https' ]
    subscriptionRequired: false
    type: 'http'
    format: 'openapi'
    serviceUrl: apiBackendUrl
    value: loadTextContent('../../src/api/openapi.yaml')
  }
}

resource apiPolicy 'Microsoft.ApiManagement/service/apis/policies@2021-12-01-preview' = {
  name: 'policy'
  parent: restApi
  properties: {
    format: 'rawxml'
    value: apiPolicyContent
  }
}

resource apiDiagnostics 'Microsoft.ApiManagement/service/apis/diagnostics@2021-12-01-preview' = {
  name: 'applicationinsights'
  parent: restApi
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

resource apimService 'Microsoft.ApiManagement/service@2021-08-01' existing = {
  name: name
}

resource apimLogger 'Microsoft.ApiManagement/service/loggers@2021-12-01-preview' existing = {
  name: 'app-insights-logger'
  parent: apimService
}