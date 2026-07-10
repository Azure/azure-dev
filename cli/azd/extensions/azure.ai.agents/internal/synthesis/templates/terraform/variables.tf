variable "subscription_id" {
  description = "Azure subscription id."
  type        = string
}

variable "location" {
  description = "Azure region for all resources."
  type        = string
}

variable "resource_group_name" {
  description = "Name of the resource group to create and deploy resources into."
  type        = string
}

variable "environment_name" {
  description = "azd environment name. Used to tag resources and to derive default names."
  type        = string
}

variable "tags" {
  description = "Tags applied to all resources."
  type        = map(string)
  default     = {}
}

variable "resource_token_salt" {
  description = "Optional salt to vary resource names across re-provisions."
  type        = string
  default     = ""
}

variable "foundry_project_name" {
  description = "Foundry project name (3-32 alphanumeric/hyphen chars). When empty, derived from environment_name."
  type        = string
  default     = ""

  validation {
    condition     = var.foundry_project_name == "" || can(regex("^[a-zA-Z0-9-]{3,32}$", var.foundry_project_name))
    error_message = "foundry_project_name must be empty or 3-32 alphanumeric/hyphen characters."
  }
}

variable "deployments" {
  description = "Model deployments to provision on the Foundry account."
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
  description = "Foundry project connections to create (host: azure.ai.connection services)."
  type = list(object({
    name        = string
    category    = string
    target      = string
    authType    = string
    credentials = optional(map(any))
    metadata    = optional(map(string))
  }))
  default = []
}

variable "principal_id" {
  description = "Object id of the developer running azd. When empty, the developer role assignment is skipped."
  type        = string
  default     = ""
}

variable "principal_type" {
  description = "Principal type used in the developer role assignment."
  type        = string
  default     = "User"
}
