// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package connections contains the stable connection category values consumed
// by toolbox tool translation. Connection lookup is owned by azure.ai.connections.
package connections

// ConnectionType is the ARM category of a Foundry project connection.
type ConnectionType string

// String returns the wire value of the connection category.
func (t ConnectionType) String() string {
	return string(t)
}

const (
	ConnectionTypeAzureOpenAI               ConnectionType = "AzureOpenAI"
	ConnectionTypeAzureBlob                 ConnectionType = "AzureBlob"
	ConnectionTypeAzureStorageAccount       ConnectionType = "AzureStorageAccount"
	ConnectionTypeCognitiveSearch           ConnectionType = "CognitiveSearch"
	ConnectionTypeContainerRegistry         ConnectionType = "ContainerRegistry"
	ConnectionTypeCosmosDB                  ConnectionType = "CosmosDB"
	ConnectionTypeApiKey                    ConnectionType = "ApiKey"
	ConnectionTypeAppConfig                 ConnectionType = "AppConfig"
	ConnectionTypeAppInsights               ConnectionType = "AppInsights"
	ConnectionTypeCustomKeys                ConnectionType = "CustomKeys"
	ConnectionTypeRemoteTool                ConnectionType = "RemoteTool"
	ConnectionTypeRemoteA2A                 ConnectionType = "RemoteA2A"
	ConnectionTypeGroundingWithCustomSearch ConnectionType = "GroundingWithCustomSearch"
)
