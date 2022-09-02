output "APPLICATIONINSIGHTS_CONNECTION_STRING" {
  value     = azurerm_application_insights.applicationinsights.connection_string
  sensitive = true
}