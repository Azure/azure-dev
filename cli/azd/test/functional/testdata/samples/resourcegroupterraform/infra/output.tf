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
  value = [true, "abc", 1234]
}

output "OBJECT" {
  value = {
    foo : "bar"
    inner: {
      foo: "bar"
    }
    array: [true, "abc", 1234]
  }
}