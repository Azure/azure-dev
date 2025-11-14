// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package grpcserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/azure/azure-dev/pkg/azdext"
	"github.com/azure/azure-dev/pkg/config"
	"github.com/azure/azure-dev/test/mocks"
	"github.com/stretchr/testify/require"
)

// Test_UserConfigService_GetSetUnset verifies the basic workflow of the user config service:
// 1. Ensuring that a missing config returns no value.
// 2. Setting a value makes it retrievable.
// 3. Unsetting the value removes it.
func Test_UserConfigService_GetSetUnset(t *testing.T) {
	// Setup context and config manager using mocks.
	mockContext := mocks.NewMockContext(context.Background())
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	mockConfig := config.NewEmptyConfig()
	mockContext.ConfigManager.WithConfig(mockConfig)

	// Create the service.
	service, err := NewUserConfigService(configManager)
	require.NoError(t, err)
	require.NotNil(t, service)

	// Test: Initially the config should not be found.
	getRequest := &azdext.GetUserConfigRequest{
		Path: "test",
	}
	getResponse, err := service.Get(*mockContext.Context, getRequest)
	require.NoError(t, err)
	require.False(t, getResponse.Found)
	require.Nil(t, getResponse.Value)

	// Test: Set a configuration and verify retrieval.
	configValue := map[string]any{"key": "value"}
	jsonBytes, err := json.Marshal(configValue)
	require.NoError(t, err)
	_, err = service.Set(*mockContext.Context, &azdext.SetUserConfigRequest{
		Path:  "test",
		Value: jsonBytes,
	})
	require.NoError(t, err)
	getResponse, err = service.Get(*mockContext.Context, getRequest)
	require.NoError(t, err)
	require.True(t, getResponse.Found)
	require.Equal(t, jsonBytes, getResponse.Value)

	// Test: Unset the configuration and verify removal.
	_, err = service.Unset(*mockContext.Context, &azdext.UnsetUserConfigRequest{
		Path: "test",
	})
	require.NoError(t, err)
	getResponse, err = service.Get(*mockContext.Context, getRequest)
	require.NoError(t, err)
	require.False(t, getResponse.Found)
	require.Nil(t, getResponse.Value)
}

// Test_UserConfigService_GetSetUnset_String validates the get/set/unset flow for string values.
// It confirms proper marshalling and unmarshalling for string config.
func Test_UserConfigService_GetSetUnset_String(t *testing.T) {
	// Setup context and config manager using mocks.
	mockContext := mocks.NewMockContext(context.Background())
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	mockConfig := config.NewEmptyConfig()
	mockContext.ConfigManager.WithConfig(mockConfig)

	// Create the service.
	service, err := NewUserConfigService(configManager)
	require.NoError(t, err)
	require.NotNil(t, service)

	path := "test/string"

	// Test: Verify initial absence of the string config.
	getStrReq := &azdext.GetUserConfigStringRequest{Path: path}
	getStrResp, err := service.GetString(*mockContext.Context, getStrReq)
	require.NoError(t, err)
	require.False(t, getStrResp.Found)

	// Test: Set a string value and verify retrieval.
	value := "hello world"
	jsonValue, err := json.Marshal(value)
	require.NoError(t, err)
	_, err = service.Set(*mockContext.Context, &azdext.SetUserConfigRequest{
		Path:  path,
		Value: jsonValue,
	})
	require.NoError(t, err)
	getStrResp, err = service.GetString(*mockContext.Context, getStrReq)
	require.NoError(t, err)
	require.True(t, getStrResp.Found)
	require.Equal(t, value, getStrResp.Value)

	// Test: Unset the string value and confirm removal.
	_, err = service.Unset(*mockContext.Context, &azdext.UnsetUserConfigRequest{Path: path})
	require.NoError(t, err)
	getStrResp, err = service.GetString(*mockContext.Context, getStrReq)
	require.NoError(t, err)
	require.False(t, getStrResp.Found)
}

// Test_UserConfigService_GetSetUnset_Section examines the get/set/unset flow for structured config sections.
// It ensures that JSON marshalling correctly handles section data.
func Test_UserConfigService_GetSetUnset_Section(t *testing.T) {
	// Setup context and config manager using mocks.
	mockContext := mocks.NewMockContext(context.Background())
	configManager := config.NewUserConfigManager(mockContext.ConfigManager)
	mockConfig := config.NewEmptyConfig()
	mockContext.ConfigManager.WithConfig(mockConfig)

	// Create the service.
	service, err := NewUserConfigService(configManager)
	require.NoError(t, err)
	require.NotNil(t, service)

	path := "test/section"
	sectionValue := map[string]any{"foo": "bar", "baz": float64(42)}

	// Test: Verify that the section is initially absent.
	getSecReq := &azdext.GetUserConfigSectionRequest{Path: path}
	getSecResp, err := service.GetSection(*mockContext.Context, getSecReq)
	require.NoError(t, err)
	require.False(t, getSecResp.Found)

	// Test: Set the section and verify its contents.
	jsonSection, err := json.Marshal(sectionValue)
	require.NoError(t, err)
	_, err = service.Set(*mockContext.Context, &azdext.SetUserConfigRequest{
		Path:  path,
		Value: jsonSection,
	})
	require.NoError(t, err)
	getSecResp, err = service.GetSection(*mockContext.Context, getSecReq)
	require.NoError(t, err)
	require.True(t, getSecResp.Found)
	var gotSection map[string]any
	err = json.Unmarshal(getSecResp.Section, &gotSection)
	require.NoError(t, err)
	require.Equal(t, sectionValue["foo"], gotSection["foo"])
	require.Equal(t, sectionValue["baz"], gotSection["baz"])

	// Test: Unset the section and verify its removal.
	_, err = service.Unset(*mockContext.Context, &azdext.UnsetUserConfigRequest{Path: path})
	require.NoError(t, err)
	getSecResp, err = service.GetSection(*mockContext.Context, getSecReq)
	require.NoError(t, err)
	require.False(t, getSecResp.Found)
}
