param resourceId string = ''
param roleDefinitionId string
param principalId string
param principalType string = ''

var fullRoleDefinitionId = az.subscriptionResourceId('Microsoft.Authorization/roleDefinitions', roleDefinitionId)

#disable-next-line no-deployments-resources
resource roleAssignment 'Microsoft.Resources/deployments@2021-04-01' = {
    name: guid(resourceId, principalId, fullRoleDefinitionId)
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
                value: guid(resourceId, principalId, fullRoleDefinitionId)
            }
            roleDefinitionId: {
                value: fullRoleDefinitionId
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
