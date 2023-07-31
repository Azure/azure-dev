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