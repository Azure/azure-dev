targetScope = 'subscription'

@minValue(1)
@description('A required int parameter which much be at least 1')
param requiredIntWithMin int

@maxValue(10)
@description('A required int parameter which much at most 10')
param requiredIntWithMax int

@minValue(1)
@maxValue(10)
@description('A required int parameter which must be in the range 1-10')
param requiredIntWithRange int

@minLength(1)
@description('A required string parameter which must have length of at least 1')
param requiredStringWithMin string

@maxLength(10)
@description('A required string parameter which must have length of at most 10')
param requiredStringWithMax string

@minLength(1)
@maxLength(10)
@description('A required string parameter which must have length in the range 1-10')
param requiredStringWithRange string

output allParameters object = {
  requiredIntWithMin: requiredIntWithMin
  requiredIntWithMax: requiredIntWithMax
  requiredIntWithRange: requiredIntWithRange
  requiredStringWithMin: requiredStringWithMin
  requiredStringWithMax: requiredStringWithMax
  requiredStringWithRange: requiredStringWithRange
}
