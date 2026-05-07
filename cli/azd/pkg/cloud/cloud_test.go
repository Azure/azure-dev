// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cloud

import (
	"testing"

	azcloud "github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAzurePublic(t *testing.T) {
	c := AzurePublic()
	require.NotNil(t, c)
	assert.Equal(t, azcloud.AzurePublic, c.Configuration)
	assert.Equal(t, "https://portal.azure.com", c.PortalUrlBase)
	assert.Equal(t, "core.windows.net", c.StorageEndpointSuffix)
	assert.Equal(t, "azurecr.io", c.ContainerRegistryEndpointSuffix)
	assert.Equal(t, "vault.azure.net", c.KeyVaultEndpointSuffix)
}

func TestAzureGovernment(t *testing.T) {
	c := AzureGovernment()
	require.NotNil(t, c)
	assert.Equal(t, azcloud.AzureGovernment, c.Configuration)
	assert.Equal(t, "https://portal.azure.us", c.PortalUrlBase)
	assert.Equal(
		t,
		"core.usgovcloudapi.net",
		c.StorageEndpointSuffix,
	)
	assert.Equal(t, "azurecr.us", c.ContainerRegistryEndpointSuffix)
	assert.Equal(
		t,
		"vault.usgovcloudapi.net",
		c.KeyVaultEndpointSuffix,
	)
}

func TestAzureChina(t *testing.T) {
	c := AzureChina()
	require.NotNil(t, c)
	assert.Equal(t, azcloud.AzureChina, c.Configuration)
	assert.Equal(t, "https://portal.azure.cn", c.PortalUrlBase)
	assert.Equal(
		t,
		"core.chinacloudapi.cn",
		c.StorageEndpointSuffix,
	)
	assert.Equal(t, "azurecr.cn", c.ContainerRegistryEndpointSuffix)
	assert.Equal(t, "vault.azure.cn", c.KeyVaultEndpointSuffix)
}

func TestNewCloud(t *testing.T) {
	tests := []struct {
		name        string
		cloudName   string
		wantPortal  string
		wantErr     bool
		errContains string
	}{
		{
			name:       "AzurePublicByName",
			cloudName:  AzurePublicName,
			wantPortal: "https://portal.azure.com",
		},
		{
			name:       "EmptyNameDefaultsToPublic",
			cloudName:  "",
			wantPortal: "https://portal.azure.com",
		},
		{
			name:       "AzureChinaCloud",
			cloudName:  AzureChinaCloudName,
			wantPortal: "https://portal.azure.cn",
		},
		{
			name:       "AzureUSGovernment",
			cloudName:  AzureUSGovernmentName,
			wantPortal: "https://portal.azure.us",
		},
		{
			name:        "InvalidCloudNameReturnsError",
			cloudName:   "SomeInvalidCloud",
			wantErr:     true,
			errContains: "not found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{Name: tt.cloudName}
			c, err := NewCloud(cfg)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errContains)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, c)
			assert.Equal(t, tt.wantPortal, c.PortalUrlBase)
		})
	}
}

func TestNewCloud_EndpointSuffixes(t *testing.T) {
	tests := []struct {
		name                  string
		cloudName             string
		wantStorage           string
		wantContainerRegistry string
		wantKeyVault          string
	}{
		{
			name:                  "PublicEndpoints",
			cloudName:             AzurePublicName,
			wantStorage:           "core.windows.net",
			wantContainerRegistry: "azurecr.io",
			wantKeyVault:          "vault.azure.net",
		},
		{
			name:                  "GovernmentEndpoints",
			cloudName:             AzureUSGovernmentName,
			wantStorage:           "core.usgovcloudapi.net",
			wantContainerRegistry: "azurecr.us",
			wantKeyVault:          "vault.usgovcloudapi.net",
		},
		{
			name:                  "ChinaEndpoints",
			cloudName:             AzureChinaCloudName,
			wantStorage:           "core.chinacloudapi.cn",
			wantContainerRegistry: "azurecr.cn",
			wantKeyVault:          "vault.azure.cn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := NewCloud(&Config{Name: tt.cloudName})
			require.NoError(t, err)
			assert.Equal(t, tt.wantStorage, c.StorageEndpointSuffix)
			assert.Equal(
				t,
				tt.wantContainerRegistry,
				c.ContainerRegistryEndpointSuffix,
			)
			assert.Equal(
				t,
				tt.wantKeyVault,
				c.KeyVaultEndpointSuffix,
			)
		})
	}
}

func TestParseCloudConfig(t *testing.T) {
	tests := []struct {
		name     string
		input    any
		wantName string
		wantErr  bool
	}{
		{
			name:     "MapWithName",
			input:    map[string]string{"name": "AzureCloud"},
			wantName: "AzureCloud",
		},
		{
			name:     "MapWithoutName",
			input:    map[string]string{"other": "value"},
			wantName: "",
		},
		{
			name:     "EmptyMap",
			input:    map[string]string{},
			wantName: "",
		},
		{
			name:     "StructWithMatchingField",
			input:    struct{ Name string }{"AzureChina"},
			wantName: "AzureChina",
		},
		{
			name:    "UnmarshalableChannelInput",
			input:   make(chan int),
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseCloudConfig(tt.input)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.NotNil(t, cfg)
			assert.Equal(t, tt.wantName, cfg.Name)
		})
	}
}

func TestParseCloudConfig_NilInput(t *testing.T) {
	// json.Marshal(nil) produces "null", which unmarshals to nil *Config
	cfg, err := ParseCloudConfig(nil)
	require.NoError(t, err)
	assert.Nil(t, cfg)
}

func TestParseCloudConfig_RoundTrip(t *testing.T) {
	input := map[string]any{"name": "AzureUSGovernment"}
	cfg, err := ParseCloudConfig(input)
	require.NoError(t, err)
	require.NotNil(t, cfg)
	assert.Equal(t, "AzureUSGovernment", cfg.Name)

	c, err := NewCloud(cfg)
	require.NoError(t, err)
	assert.Equal(t, "https://portal.azure.us", c.PortalUrlBase)
}

func TestConstants(t *testing.T) {
	assert.Equal(t, "cloud", ConfigPath)
	assert.Equal(t, "AzureCloud", AzurePublicName)
	assert.Equal(t, "AzureChinaCloud", AzureChinaCloudName)
	assert.Equal(t, "AzureUSGovernment", AzureUSGovernmentName)
}
