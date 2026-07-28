@description('Foundry project name. 3-32 characters for newly created projects.')
@minLength(3)
@maxLength(32)
param name string

output validatedName string = name
