// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.
package project

import (
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---- createTypedResourceProps ----

func Test_createTypedResourceProps_Coverage3(t *testing.T) {
	tests := []struct {
		name         string
		resourceType ResourceType
		config       []byte
		expectNil    bool // when default case returns (nil, nil)
		expectType   string
	}{
		{"AppService_empty", ResourceTypeHostAppService, nil, false, "AppServiceProps"},
		{"AppService_json", ResourceTypeHostAppService,
			mustJSON(t, AppServiceProps{Port: 8080}), false, "AppServiceProps"},
		{"ContainerApp_empty", ResourceTypeHostContainerApp, nil, false, "ContainerAppProps"},
		{"ContainerApp_json", ResourceTypeHostContainerApp,
			mustJSON(t, ContainerAppProps{Port: 3000}), false, "ContainerAppProps"},
		{"Cosmos_empty", ResourceTypeDbCosmos, nil, false, "CosmosDBProps"},
		{"Cosmos_json", ResourceTypeDbCosmos,
			mustJSON(t, CosmosDBProps{Containers: []CosmosDBContainerProps{{Name: "c1"}}}), false, "CosmosDBProps"},
		{"Storage_empty", ResourceTypeStorage, nil, false, "StorageProps"},
		{"Storage_json", ResourceTypeStorage,
			mustJSON(t, StorageProps{Containers: []string{"blob1"}}), false, "StorageProps"},
		{"AiProject_empty", ResourceTypeAiProject, nil, false, "AiFoundryModelProps"},
		{"AiProject_json", ResourceTypeAiProject,
			mustJSON(t, AiFoundryModelProps{}), false, "AiFoundryModelProps"},
		{"Mongo_empty", ResourceTypeDbMongo, nil, false, "CosmosDBProps"},
		{"Mongo_json", ResourceTypeDbMongo,
			mustJSON(t, CosmosDBProps{}), false, "CosmosDBProps"},
		{"EventHubs_empty", ResourceTypeMessagingEventHubs, nil, false, "EventHubsProps"},
		{"EventHubs_json", ResourceTypeMessagingEventHubs,
			mustJSON(t, EventHubsProps{Hubs: []string{"hub1"}}), false, "EventHubsProps"},
		{"ServiceBus_empty", ResourceTypeMessagingServiceBus, nil, false, "ServiceBusProps"},
		{"ServiceBus_json", ResourceTypeMessagingServiceBus,
			mustJSON(t, ServiceBusProps{Queues: []string{"q1"}}), false, "ServiceBusProps"},
		{"Unknown_returns_nil", ResourceType("unknown"), nil, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := createTypedResourceProps(tt.resourceType, tt.config)
			require.NoError(t, err)
			if tt.expectNil {
				assert.Nil(t, result)
			} else {
				assert.NotNil(t, result)
			}
		})
	}
}

func Test_createTypedResourceProps_InvalidJSON_Coverage3(t *testing.T) {
	badJSON := []byte(`{invalid}`)

	types := []ResourceType{
		ResourceTypeHostAppService,
		ResourceTypeHostContainerApp,
		ResourceTypeDbCosmos,
		ResourceTypeStorage,
		ResourceTypeAiProject,
		ResourceTypeDbMongo,
		ResourceTypeMessagingEventHubs,
		ResourceTypeMessagingServiceBus,
	}

	for _, rt := range types {
		t.Run(string(rt), func(t *testing.T) {
			_, err := createTypedResourceProps(rt, badJSON)
			require.Error(t, err)
		})
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

// ---- getResourceTypeKinds ----

func Test_getResourceTypeKinds_Coverage3(t *testing.T) {
	tests := []struct {
		name     string
		rt       ResourceType
		expected []string
	}{
		{"Cosmos", ResourceTypeDbCosmos, []string{"GlobalDocumentDB"}},
		{"Mongo", ResourceTypeDbMongo, []string{"MongoDB"}},
		{"AppService", ResourceTypeHostAppService, []string{"app", "app,linux"}},
		{"Unknown", ResourceType("unknown"), []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := getResourceTypeKinds(tt.rt)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// ---- protoToLocationKind ----

func Test_protoToLocationKind_Coverage3(t *testing.T) {
	tests := []struct {
		name      string
		kind      azdext.LocationKind
		expected  LocationKind
		expectErr bool
	}{
		{"Local", azdext.LocationKind_LOCATION_KIND_LOCAL, LocationKindLocal, false},
		{"Remote", azdext.LocationKind_LOCATION_KIND_REMOTE, LocationKindRemote, false},
		{"Unspecified", azdext.LocationKind_LOCATION_KIND_UNSPECIFIED, "", true},
		{"Unknown_value", azdext.LocationKind(999), "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := protoToLocationKind(tt.kind)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expected, result)
			}
		})
	}
}
