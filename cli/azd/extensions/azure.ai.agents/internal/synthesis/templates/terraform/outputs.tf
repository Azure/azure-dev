# Outputs. azd-core writes each Terraform output verbatim into the azd
# environment (terraform output -json), so the names here must exactly match
# the Bicep template's output contract (main.bicep). Empty strings are emitted
# for ACR outputs when include_acr is false, mirroring the Bicep ternaries.

output "AZURE_RESOURCE_GROUP" {
  value = azurerm_resource_group.this.name
}

output "AZURE_AI_PROJECT_ID" {
  value = azapi_resource.project.id
}

output "AZURE_AI_ACCOUNT_NAME" {
  value = azapi_resource.foundry_account.name
}

output "AZURE_AI_PROJECT_NAME" {
  value = azapi_resource.project.name
}

output "AZURE_OPENAI_ENDPOINT" {
  value = "https://${azapi_resource.foundry_account.name}.openai.azure.com/"
}

output "FOUNDRY_PROJECT_ENDPOINT" {
  value = "https://${azapi_resource.foundry_account.name}.services.ai.azure.com/api/projects/${azapi_resource.project.name}"
}

output "AZURE_CONTAINER_REGISTRY_ENDPOINT" {
  value = var.include_acr ? azurerm_container_registry.this[0].login_server : ""
}

output "AZURE_CONTAINER_REGISTRY_RESOURCE_ID" {
  value = var.include_acr ? azurerm_container_registry.this[0].id : ""
}

output "AZURE_AI_PROJECT_ACR_CONNECTION_NAME" {
  value = var.include_acr ? azapi_resource.acr_connection[0].name : ""
}
