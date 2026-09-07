# Foundry Connections

Manage Microsoft Foundry Connections from your terminal. (Preview)

## `azure.yaml` ownership

This extension owns `host: azure.ai.connection` services. Each service block
describes one Foundry project Connection and is reconciled by `azd deploy` or
`azd up` after the referenced `azure.ai.project` service is provisioned.

```yaml
services:
	search:
		host: azure.ai.connection
		uses:
			- my-project
		category: CognitiveSearch
		target: ${SEARCH_ENDPOINT}
		authType: ApiKey
		credentials:
			key: ${SEARCH_KEY}
		env:
			SEARCH_ENDPOINT: ${SEARCH_ENDPOINT}
			SEARCH_KEY: ${SEARCH_KEY}
```

The service key identifies the dependency in `uses`. When the block also has a
`name`, that value is the Foundry Connection name; otherwise the service key is
used. Local `$ref` files and nested credential values are resolved by this
extension. Removing the service from `azure.yaml` stops managing it but does
not delete the remote Connection; use `azd ai connection delete` to delete it.

The embedded `microsoft.foundry` provider no longer provisions split
`azure.ai.connection` services. Existing ejected or user-owned infrastructure
continues to receive its declared Connection inputs for compatibility.
