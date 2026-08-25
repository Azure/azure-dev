variable "subscription_id" {
  description = "Subscription where adjunct resources are created."
  type        = string
}

variable "tenant_id" {
  description = "Microsoft Entra tenant that owns the target subscriptions."
  type        = string
}

variable "project_resource_id" {
  description = "ARM resource ID of the existing Foundry project."
  type        = string

  validation {
    condition = can(regex(
      "(?i)^/subscriptions/[^/]+/resourceGroups/[^/]+/providers/Microsoft\\.CognitiveServices/accounts/[^/]+/projects/[^/]+$",
      var.project_resource_id,
    ))
    error_message = "project_resource_id must be a Foundry project ARM resource ID."
  }
}

variable "project_endpoint" {
  description = "Endpoint of the existing Foundry project."
  type        = string
}

variable "location" {
  description = "Azure region for adjunct resources."
  type        = string
}

variable "resource_group_name" {
  description = "Resource group to create for adjunct resources such as ACR."
  type        = string
}

variable "environment_name" {
  description = "azd environment name. Used to tag adjunct resources."
  type        = string
}

variable "tags" {
  description = "Tags applied to adjunct resources."
  type        = map(string)
  default     = {}
}

variable "resource_token_salt" {
  description = "Optional salt to vary adjunct resource names."
  type        = string
  default     = ""
}

variable "deployments" {
  description = "Model deployments to provision on the existing Foundry account."
  type = list(object({
    name = string
    model = object({
      name    = string
      format  = string
      version = string
    })
    sku = object({
      name     = string
      capacity = number
    })
  }))
  default = []
}

variable "connections" {
  description = "Connections to provision on the existing Foundry project."
  type = list(object({
    name        = string
    category    = string
    target      = string
    authType    = string
    credentials = optional(any)
    metadata    = optional(map(string))
  }))
  default = []
}

variable "existing_acr_endpoint" {
  type    = string
  default = ""
}

variable "existing_acr_resource_id" {
  type    = string
  default = ""
}

variable "existing_acr_connection_name" {
  type    = string
  default = ""
}
