resource "azapi_resource_action" "model_deployment" {
  for_each = { for deployment in var.deployments : deployment.name => deployment }

  type        = "Microsoft.CognitiveServices/accounts/deployments@2025-06-01"
  resource_id = "${local.foundry_account_id}/deployments/${each.value.name}"
  method      = "PUT"
  locks       = [local.foundry_account_id]

  body = {
    properties = {
      model = each.value.model
    }
    sku = each.value.sku
  }
}
