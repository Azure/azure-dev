// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"encoding/json"
	"testing"

	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/stretchr/testify/require"
)

func TestBuildCreateBody_ApiKey(t *testing.T) {
	params := CreateConnectionParams{
		Category: "ApiKey",
		Target:   "https://httpbin.org/get",
		AuthType: "ApiKey",
		Key:      "test-key-12345",
		Metadata: map[string]string{"ApiType": "Azure"},
	}

	body, err := buildCreateBody(params)
	require.NoError(t, err)
	require.NotNil(t, body)
	require.NotNil(t, body.Properties)

	props := body.Properties.GetConnectionPropertiesV2()
	require.Equal(t, "ApiKey", string(*props.AuthType))
	require.Equal(t, "ApiKey", string(*props.Category))
	require.Equal(t, "https://httpbin.org/get", *props.Target)
	require.Equal(t, "Azure", *props.Metadata["ApiType"])
}

func TestBuildCreateBody_CustomKeys(t *testing.T) {
	params := CreateConnectionParams{
		Category: "RemoteTool",
		Target:   "https://mcp.tavily.com/mcp",
		AuthType: "CustomKeys",
		Keys:     map[string]string{"x-api-key": "tvly-abc123"},
		Metadata: map[string]string{"type": "custom_MCP"},
	}

	body, err := buildCreateBody(params)
	require.NoError(t, err)
	require.NotNil(t, body)

	props := body.Properties.GetConnectionPropertiesV2()
	require.Equal(t, "CustomKeys", string(*props.AuthType))
	require.Equal(t, "RemoteTool", string(*props.Category))
	require.Equal(t, "https://mcp.tavily.com/mcp", *props.Target)
}

func TestBuildCreateBody_None(t *testing.T) {
	params := CreateConnectionParams{
		Category: "RemoteTool",
		Target:   "https://learn.microsoft.com/api/mcp",
		AuthType: "None",
	}

	body, err := buildCreateBody(params)
	require.NoError(t, err)
	require.NotNil(t, body)

	props := body.Properties.GetConnectionPropertiesV2()
	require.Equal(t, "None", string(*props.AuthType))
	require.Equal(t, "RemoteTool", string(*props.Category))
	require.Equal(t, "https://learn.microsoft.com/api/mcp", *props.Target)
	require.Nil(t, props.Metadata)
}

func TestBuildCreateBody_UnsupportedAuthType(t *testing.T) {
	params := CreateConnectionParams{
		Category: "RemoteTool",
		Target:   "https://example.com",
		AuthType: "OAuth2",
	}

	_, err := buildCreateBody(params)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unsupported auth type")
	require.Contains(t, err.Error(), "OAuth2")
}

func TestBuildCreateBody_NilMetadata(t *testing.T) {
	params := CreateConnectionParams{
		Category: "ApiKey",
		Target:   "https://example.com",
		AuthType: "ApiKey",
		Key:      "key",
	}

	body, err := buildCreateBody(params)
	require.NoError(t, err)

	props := body.Properties.GetConnectionPropertiesV2()
	require.Nil(t, props.Metadata)
}

func TestToStringPtrMap(t *testing.T) {
	t.Run("nil input", func(t *testing.T) {
		result := toStringPtrMap(nil)
		require.Nil(t, result)
	})

	t.Run("empty map", func(t *testing.T) {
		result := toStringPtrMap(map[string]string{})
		require.NotNil(t, result)
		require.Empty(t, result)
	})

	t.Run("populated map", func(t *testing.T) {
		input := map[string]string{"a": "1", "b": "2"}
		result := toStringPtrMap(input)
		require.Len(t, result, 2)
		require.Equal(t, "1", *result["a"])
		require.Equal(t, "2", *result["b"])
	})
}

func TestLastSegment(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/subscriptions/sub/resourceGroups/rg/connections/my-conn", "my-conn"},
		{"my-conn", "my-conn"},
		{"/a/b/c", "c"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			require.Equal(t, tt.want, lastSegment(tt.input))
		})
	}
}

func TestParseRawCredentials(t *testing.T) {
	t.Run("ApiKey credentials", func(t *testing.T) {
		raw := `{
			"credentials": {
				"key": "my-api-key",
				"type": "ApiKey"
			}
		}`

		var envelope struct {
			Credentials map[string]json.RawMessage `json:"credentials"`
		}
		err := json.Unmarshal([]byte(raw), &envelope)
		require.NoError(t, err)

		result := make(map[string]string)
		for k, v := range envelope.Credentials {
			if k == "type" {
				continue
			}
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				continue
			}
			result[k] = s
		}

		require.Equal(t, map[string]string{"key": "my-api-key"}, result)
	})

	t.Run("CustomKeys credentials", func(t *testing.T) {
		raw := `{
			"credentials": {
				"x-api-key": "tvly-abc123",
				"secret": "another-secret",
				"type": "CustomKeys"
			}
		}`

		var envelope struct {
			Credentials map[string]json.RawMessage `json:"credentials"`
		}
		err := json.Unmarshal([]byte(raw), &envelope)
		require.NoError(t, err)

		result := make(map[string]string)
		for k, v := range envelope.Credentials {
			if k == "type" {
				continue
			}
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				continue
			}
			result[k] = s
		}

		require.Equal(t, "tvly-abc123", result["x-api-key"])
		require.Equal(t, "another-secret", result["secret"])
		require.Len(t, result, 2)
	})

	t.Run("CustomKeys with key named 'key'", func(t *testing.T) {
		raw := `{
			"credentials": {
				"key": "value-456",
				"type": "CustomKeys"
			}
		}`

		var envelope struct {
			Credentials map[string]json.RawMessage `json:"credentials"`
		}
		err := json.Unmarshal([]byte(raw), &envelope)
		require.NoError(t, err)

		result := make(map[string]string)
		for k, v := range envelope.Credentials {
			if k == "type" {
				continue
			}
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				continue
			}
			result[k] = s
		}

		require.Equal(t, map[string]string{"key": "value-456"}, result)
	})

	t.Run("no credentials", func(t *testing.T) {
		raw := `{
			"credentials": {
				"type": "AAD"
			}
		}`

		var envelope struct {
			Credentials map[string]json.RawMessage `json:"credentials"`
		}
		err := json.Unmarshal([]byte(raw), &envelope)
		require.NoError(t, err)

		result := make(map[string]string)
		for k, v := range envelope.Credentials {
			if k == "type" {
				continue
			}
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				continue
			}
			result[k] = s
		}

		require.Empty(t, result)
	})
}

func TestConnectionInfoFromARM_NilProperties(t *testing.T) {
	r := &armcognitiveservices.ConnectionPropertiesV2BasicResource{}
	info := connectionInfoFromARM(r)
	require.Empty(t, info.Name)
	require.Empty(t, info.Category)
}

func TestConnectionInfoFromARM_WithID(t *testing.T) {
	id := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct/projects/proj/connections/my-conn"
	r := &armcognitiveservices.ConnectionPropertiesV2BasicResource{
		ID: &id,
	}
	info := connectionInfoFromARM(r)
	require.Equal(t, "my-conn", info.Name)
	require.Equal(t, id, info.ID)
}
