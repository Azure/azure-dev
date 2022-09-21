variable "location" {
  description = "The supported azure location where the resource deployed"
  type        = string
}

variable "rg_name" {
  description = "The name of the resource group to deploy resources into"
  type        = string
}

variable "environment_name" {
  description = "The name of the environment to be deployed"
  type        = string
}

variable "workspace_id" {
  description = "The name of the Azure log analytics workspace"
  type        = string
}

variable "tags" {
  description = "A list of tags used for deployed services."
  type        = map(string)
}

variable "resource_token" {
  description = "A postfix string to centrally mitigate resource name collisions."
  type        = string
}