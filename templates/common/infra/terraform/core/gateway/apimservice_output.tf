output "URI" {
  value = "https://${azurerm_linux_web_app.web.default_hostname}" 
}

output "apimServiceName" {
  value = module.apimService.name
}