targetScope = 'subscription'

@description('A required string parameter')
param stringParam string

@description('An optional string parameter')
param optionalStringParam string = ''

@description('A required bool parameter')
param boolParam bool

@description('An optional bool parameter')
param optionalBoolParam bool = false

@description('A required bool parameter')
param intParam int

@description('An optional bool parameter')
param optionalIntParam int = 0

@description('A required array parameter')
param arrayParam array

@description('An optional array parameter')
param optionalArrayParam array = []

@description('A required object parameter')
param objectParam object

@description('An optional object parameter')
param optionalObjectParam object = {}

output allParameters object = {
  stringParam: stringParam
  optionalStringParam: optionalStringParam
  boolParam: boolParam
  optionalBoolParam: optionalBoolParam
  intParam: intParam
  optionalIntParam: optionalIntParam
  arrayParam: arrayParam
  optionalArrayParam: optionalArrayParam
  objectParam: objectParam
  optionalObjectParam: optionalObjectParam
}
