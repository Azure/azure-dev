output "SERVICE_API_URI" {
  value = "${data.azurerm_api_management.apim.gateway_url}/${var.api_path}"
}
