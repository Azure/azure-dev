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
# Deploy apimservice
# ------------------------------------------------------------------------------------------------------
resource "apimService" "apim" { 
  name     = name
  location = var.location
  tags     = merge(local.tags, { "azd-service-name" : name })
  sku {
    name     = sku
    capacity = (sku == "Consumption") ? 0 : ((sku == "Developer") ? 1 : skuCount)
  }
  properties  {
    publisherEmail = publisherEmail 
    publisherName  = publisherName
  }
}

resource "apimLogger" "logger" {
  count  = (!empty(applicationInsightsName)) ? true : false
  name   = "app-insights-logger"
  parent = apimService
  properties {
    credentials {
      instrumentationKey = applicationInsights.properties.InstrumentationKey
    }
    description = "Logger to Azure Application Insights"
    isBuffered  = false
    loggerType  = "applicationInsights"
    resourceId  = applicationInsights.id
  }
}

resource "applicationInsights" "applicationInsightsName"  {
  count = !(empty(applicationInsightsName)) ? true : false
  name  = applicationInsightsName
  # 源代码 resource 之后判断此服务是否存在 "existing = if (!empty(applicationInsightsName))" 报错无法直接增加
  # 此处作为判断
}

