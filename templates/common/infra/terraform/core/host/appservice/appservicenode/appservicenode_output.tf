output "URI" {
  value = "https://${azurerm_linux_web_app.web.default_hostname}"
}

output "IDENTITY_PRINCIPAL_ID" {
  value     = length(azurerm_linux_web_app.web.identity) == 0 ? "" : azurerm_linux_web_app.web.identity.0.principal_id
  sensitive = true
}

output "APPSERVICE_NAME" {
  value = azurerm_linux_web_app.web.name
}
