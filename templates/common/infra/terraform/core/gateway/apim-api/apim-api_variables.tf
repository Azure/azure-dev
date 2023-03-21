variable "name" {
  type        = string
}

variable "rg_name" {
  description = "The name of the resource group to deploy resources into"
  type        = string
}

variable "api_management_logger_id" {
  description = "The name of the resource application insights"
  type        = string
}

variable "web_front_end_url" {
  description = "The url of the web"
  type        = string
}

variable "api_backend_url" {
  description = "Absolute URL of the backend service implementing this API."
  type        = string
}

variable "api_name" {
  description = "Resource name to uniquely identify this API within the API Management service instance"
  type        = string
}

variable "api_display_name" {

  description = "The Display Name of the API"
  type        = string
}

variable "api_path" {
  description = "Relative URL uniquely identifying this API and all of its resource paths within the API Management service instance. It is appended to the API endpoint base URL specified during the service instance creation to form a public URL for this API."
  type        = string
}
