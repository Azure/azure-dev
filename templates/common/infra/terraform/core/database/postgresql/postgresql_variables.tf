variable "location" {
  description = "The supported Azure location where the resource deployed"
  type        = string
}

variable "rg_name" {
  description = "The name of the resource group to deploy resources into"
  type        = string
}

variable "tags" {
  description = "A list of tags used for deployed services."
  type        = map(string)
}

variable "resource_token" {
  description = "A suffix string to centrally mitigate resource name collisions."
  type        = string
}

variable "administrator_login" {
  type        = string
  description = "The PostgreSQL administrator login"
  default     = "psqladmin"
}

variable "database_name" {
  type        = string
  description = "The database name of PostgreSQL"
  default     = "todo"
}

variable "client_id" {
  type        = string
  description = "Client id of current account"
  default     = ""
}

variable "tenant_id" {
  type        = string
  description = "TenantId id of current account"
  default     = ""
}

variable "object_id" {
  type        = string
  description = "Object id of current account"
  default     = ""
}

variable "principal_name" {
  type        = string
  description = "Principal name of current account"
  default     = ""
}