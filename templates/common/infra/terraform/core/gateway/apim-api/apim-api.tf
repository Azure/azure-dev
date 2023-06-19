terraform {
  required_providers {
    azurerm = {
      version = "~>3.47.0"
      source  = "hashicorp/azurerm"
    }
    azurecaf = {
      source  = "aztfmod/azurecaf"
      version = "~>1.2.24"
    }
  }
}

data "azurerm_api_management" "apim" {
  name                = var.name
  resource_group_name = var.rg_name
}

# ------------------------------------------------------------------------------------------------------
# Deploy apim-api service 
# ------------------------------------------------------------------------------------------------------
resource "azurerm_api_management_api" "api" {
  name                = var.api_name
  resource_group_name = var.rg_name
  api_management_name = data.azurerm_api_management.apim.name
  revision            = "1"
  display_name        = var.api_display_name
  path                = var.api_path
  protocols           = ["https"]
  service_url         = var.api_backend_url
  subscription_required = false

  import {
    content_format = "openapi"
    content_value  = file("${path.module}/../../../src/api/openapi.yaml")
  }
}

resource "azurerm_api_management_api_policy" "policies" {
  api_name            = azurerm_api_management_api.api.name
  api_management_name = azurerm_api_management_api.api.api_management_name
  resource_group_name = var.rg_name

  xml_content = replace(file("${path.module}/apim-api-policy.xml"), "{origin}", var.web_front_end_url)
}

resource "azurerm_api_management_api_diagnostic" "diagnostics" {
  identifier               = "applicationinsights"
  resource_group_name      = var.rg_name
  api_management_name      = azurerm_api_management_api.api.api_management_name
  api_name                 = azurerm_api_management_api.api.name
  api_management_logger_id = var.api_management_logger_id

  sampling_percentage       = 100.0
  always_log_errors         = true
  log_client_ip             = true
  verbosity                 = "verbose"
  http_correlation_protocol = "W3C"

  frontend_request {
    body_bytes = 1024
    headers_to_log = [
      "content-type",
      "accept",
      "origin",
    ]
  }

  frontend_response {
    body_bytes = 1024
    headers_to_log = [
      "content-type",
      "content-length",
      "origin",
    ]
  }

  backend_request {
    body_bytes = 1024
    headers_to_log = [
      "content-type",
      "accept",
      "origin",
    ]
  }

  backend_response {
    body_bytes = 1024
    headers_to_log = [
      "content-type",
      "content-length",
      "origin",
    ]
  }
}
