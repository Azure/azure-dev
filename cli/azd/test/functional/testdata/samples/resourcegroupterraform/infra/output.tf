output "AZURE_LOCATION" {
  value = var.location
}

output "RG_NAME" {
  value = azurerm_resource_group.rg.name
}


// test cases for all supported types
output "STRING" {
  value = "abc"
}

output "BOOL" {
  value = true
}

output "INT" {
  value = 1234
}

output "ARRAY" {
  value = var.dummy_tuple
}

output "ARRAY_INT" {
  value = var.dummy_set_numbers
}

output "ARRAY_STRING" {
  value = var.dummy_list_string
}

output "NULL" {
  value = null
}

output "OBJECT" {
  value = {
    foo : "bar"
    inner: var.dummy_map
    array: var.dummy_tuple
  }
}
