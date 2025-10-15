Update copilot-setup-steps.yml to use a federated credential to access Azure resources.

## Final steps

**NOTE: one final step, and you're ready to use the Copilot coding agent with Azure!**

After this pull request merges, you'll need to update the Copilot coding agent's MCP settings [(link)](%s) with
the following JSON to activate the Azure MCP server:

```json
%s
```

## Extending the managed identity's roles and scopes

By default, the identity is configured with the Reader role, on the resource group you created/selected. You can expand the role and scope for the identity, to fit better with your needs:

Some further instructions on how to assign roles:

- [Using the Azure portal to assign roles](https://learn.microsoft.com/azure/role-based-access-control/role-assignments-portal-managed-identity)
- [Using the Azure CLI to assign roles](https://learn.microsoft.com/azure/role-based-access-control/role-assignments-cli)
- [Azure built-in roles](https://learn.microsoft.com/azure/role-based-access-control/built-in-roles)

## Resources

- `coding-agent` readme: ([link](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.coding-agent/README.md#troubleshooting))
