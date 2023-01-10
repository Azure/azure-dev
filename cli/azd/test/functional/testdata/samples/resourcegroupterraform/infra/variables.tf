variable "location" {
  description = "The supported Azure location where the resource deployed"
  type        = string
}

variable "environment_name" {
  description = "The name of the azd environment to be deployed"
  type        = string
}

variable "dummy_tuple" {
  description = "Dummy tuple-typed value."
  type = tuple([bool, string, number])
  default = [true, "abc", 1234]
}


variable "dummy_list_string" {
  description = "Dummy list-typed value."
  type = list(string)
  default = ["elem1", "elem2", "elem3"]
}

variable "dummy_map" {
  description = "Dummy map-typed value."
  type = map(string)
  default = {
    foo: "bar"
  }
}

variable "dummy_set_numbers" {
  description = "Dummy set-typed value."
  type = set(number)
  default = [1, 2,3, "1", 1]
}