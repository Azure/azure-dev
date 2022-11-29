targetScope = 'subscription'

@description('A required string parameter')
param stringParam string

@description('An optional string parameter')
param optionalStringParam string = ''

output allParameters object = {
  stringParam: stringParam
  optionalStringParam: optionalStringParam
}
