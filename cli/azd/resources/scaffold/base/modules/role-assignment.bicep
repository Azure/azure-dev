param resourceId string
param roleDefinitionId string
param principalId string
param principalType string = ''

#disable-next-line no-deployments-resources
resource roleAssignment 'Microsoft.Resources/deployments@2021-04-01' = {
    name: guid(resourceId, principalId, roleDefinitionId)
    properties: {
        mode: 'Incremental'
        expressionEvaluationOptions: {
            scope: 'Outer'
        }
        template: json(loadTextContent('./role-assignment.json'))
        parameters: {
            scope: {
                value: resourceId
            }
            name: {
                value: guid(resourceId, principalId, roleDefinitionId)
            }
            roleDefinitionId: {
                value: roleDefinitionId
            }
            principalId: {
                value: principalId
            }
            principalType: {
                value: principalType
            }
        }
    }
}

output roleAssignmentId string = roleAssignment.properties.outputs.roleAssignmentId.value
