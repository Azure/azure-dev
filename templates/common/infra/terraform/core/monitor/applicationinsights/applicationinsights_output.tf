output "APPLICATIONINSIGHTS_CONNECTION_STRING" {
  value     = azurerm_application_insights.applicationinsights.connection_string
  sensitive = true
}

output "APPLICATIONINSIGHTS_NAME" {
  value     = azurerm_application_insights.applicationinsights.name
  sensitive = false
}

output "APPLICATIONINSIGHTS_INSTRUMENTATION_KEY" {
  value     = azurerm_application_insights.applicationinsights.instrumentation_key
  sensitive = true
}
