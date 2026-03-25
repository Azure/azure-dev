// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package contracts

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRFC3339Time_MarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{
			name:     "truncates nanoseconds",
			input:    time.Date(2023, 1, 9, 6, 39, 0, 313323855, time.UTC),
			expected: `"2023-01-09T06:39:00Z"`,
		},
		{
			name:     "preserves timezone offset",
			input:    time.Date(2024, 6, 15, 14, 30, 0, 0, time.FixedZone("EST", -5*3600)),
			expected: `"2024-06-15T14:30:00-05:00"`,
		},
		{
			name:     "zero time",
			input:    time.Time{},
			expected: `"0001-01-01T00:00:00Z"`,
		},
		{
			name:     "epoch",
			input:    time.Unix(0, 0).UTC(),
			expected: `"1970-01-01T00:00:00Z"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := json.Marshal(RFC3339Time(tt.input))
			require.NoError(t, err)
			assert.Equal(t, tt.expected, string(result))
		})
	}
}

func TestRFC3339Time_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected time.Time
	}{
		{
			name:     "UTC time",
			input:    `"2023-01-09T06:39:00Z"`,
			expected: time.Date(2023, 1, 9, 6, 39, 0, 0, time.UTC),
		},
		{
			name:     "with timezone offset",
			input:    `"2024-06-15T14:30:00-05:00"`,
			expected: time.Date(2024, 6, 15, 14, 30, 0, 0, time.FixedZone("", -5*3600)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result RFC3339Time
			err := json.Unmarshal([]byte(tt.input), &result)
			require.NoError(t, err)
			assert.True(t, time.Time(result).Equal(tt.expected),
				"got %v, want %v", time.Time(result), tt.expected)
		})
	}
}

func TestRFC3339Time_UnmarshalJSON_errors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{
			name:  "invalid JSON",
			input: `not-json`,
		},
		{
			name:  "invalid date format",
			input: `"2023-01-09 06:39:00"`,
		},
		{
			name:  "number instead of string",
			input: `12345`,
		},
		{
			name:  "empty string",
			input: `""`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var result RFC3339Time
			err := json.Unmarshal([]byte(tt.input), &result)
			assert.Error(t, err)
		})
	}
}

func TestRFC3339Time_roundtrip(t *testing.T) {
	original := time.Date(2024, 12, 25, 10, 0, 0, 0, time.UTC)
	rfc := RFC3339Time(original)

	data, err := json.Marshal(rfc)
	require.NoError(t, err)

	var restored RFC3339Time
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.True(t, time.Time(restored).Equal(original))
}

func TestAuthTokenResult_JSON(t *testing.T) {
	expiresOn := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	result := AuthTokenResult{
		Token:     "test-token-value", //nolint:gosec // test data, not a real credential
		ExpiresOn: RFC3339Time(expiresOn),
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "test-token-value", parsed["token"])
	assert.Equal(t, "2024-06-15T12:00:00Z", parsed["expiresOn"])
}

func TestAuthTokenResult_JSON_roundtrip(t *testing.T) {
	original := AuthTokenResult{
		Token:     "test-token",
		ExpiresOn: RFC3339Time(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)),
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var restored AuthTokenResult
	err = json.Unmarshal(data, &restored)
	require.NoError(t, err)

	assert.Equal(t, original.Token, restored.Token)
	assert.True(t, time.Time(restored.ExpiresOn).Equal(time.Time(original.ExpiresOn)))
}

func TestLoginResult_JSON(t *testing.T) {
	t.Run("success with expiry", func(t *testing.T) {
		expiresOn := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
		result := LoginResult{
			Status:    LoginStatusSuccess,
			ExpiresOn: &expiresOn,
		}

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "success", parsed["status"])
		assert.Contains(t, parsed, "expiresOn")
	})

	t.Run("unauthenticated omits expiresOn", func(t *testing.T) {
		result := LoginResult{
			Status: LoginStatusUnauthenticated,
		}

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "unauthenticated", parsed["status"])
		assert.NotContains(t, parsed, "expiresOn")
	})
}

func TestStatusResult_JSON(t *testing.T) {
	t.Run("authenticated user", func(t *testing.T) {
		result := StatusResult{
			Status: AuthStatusAuthenticated,
			Type:   AccountTypeUser,
			Email:  "user@example.com",
		}

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "authenticated", parsed["status"])
		assert.Equal(t, "user", parsed["type"])
		assert.Equal(t, "user@example.com", parsed["email"])
		assert.NotContains(t, parsed, "clientId")
	})

	t.Run("authenticated service principal", func(t *testing.T) {
		result := StatusResult{
			Status:   AuthStatusAuthenticated,
			Type:     AccountTypeServicePrincipal,
			ClientID: "00000000-0000-0000-0000-000000000001",
		}

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "authenticated", parsed["status"])
		assert.Equal(t, "servicePrincipal", parsed["type"])
		assert.Equal(t, "00000000-0000-0000-0000-000000000001", parsed["clientId"])
		assert.NotContains(t, parsed, "email")
	})

	t.Run("unauthenticated omits optional fields", func(t *testing.T) {
		result := StatusResult{
			Status: AuthStatusUnauthenticated,
		}

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.Equal(t, "unauthenticated", parsed["status"])
		assert.NotContains(t, parsed, "type")
		assert.NotContains(t, parsed, "email")
		assert.NotContains(t, parsed, "clientId")
	})
}

func TestShowResult_JSON(t *testing.T) {
	result := ShowResult{
		Name: "my-app",
		Services: map[string]ShowService{
			"api": {
				Project: ShowServiceProject{
					Path: "./src/api",
					Type: ShowTypeDotNet,
				},
				Target: &ShowTargetArm{
					ResourceIds: []string{"/subscriptions/sub1/resourceGroups/rg1"},
				},
				IngresUrl: "https://api.example.com",
			},
		},
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "my-app", parsed["name"])

	// IngresUrl should be excluded (json:"-")
	services := parsed["services"].(map[string]any)
	api := services["api"].(map[string]any)
	assert.NotContains(t, api, "ingresUrl")
	assert.NotContains(t, api, "IngresUrl")

	// Project fields should be present
	project := api["project"].(map[string]any)
	assert.Equal(t, "./src/api", project["path"])
	assert.Equal(t, "dotnet", project["language"])

	// Target should be present
	target := api["target"].(map[string]any)
	resourceIds := target["resourceIds"].([]any)
	assert.Len(t, resourceIds, 1)
}

func TestShowService_JSON_nil_target(t *testing.T) {
	result := ShowService{
		Project: ShowServiceProject{
			Path: "./src/web",
			Type: ShowTypeNode,
		},
		Target: nil,
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.NotContains(t, parsed, "target")
}

func TestShowType_values(t *testing.T) {
	assert.Equal(t, ShowType(""), ShowTypeNone)
	assert.Equal(t, ShowType("dotnet"), ShowTypeDotNet)
	assert.Equal(t, ShowType("python"), ShowTypePython)
	assert.Equal(t, ShowType("node"), ShowTypeNode)
	assert.Equal(t, ShowType("java"), ShowTypeJava)
	assert.Equal(t, ShowType("custom"), ShowTypeCustom)
}

func TestVersionResult_JSON(t *testing.T) {
	result := VersionResult{}
	result.Azd.Version = "1.5.0"
	result.Azd.Commit = "abc123"

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	azd := parsed["azd"].(map[string]any)
	assert.Equal(t, "1.5.0", azd["version"])
	assert.Equal(t, "abc123", azd["commit"])
}

func TestVsServerResult_JSON(t *testing.T) {
	t.Run("with certificate", func(t *testing.T) {
		cert := "MIIC+zCCAeOgAwIBAgIJAL..."
		result := VsServerResult{
			Port:             5000,
			Pid:              12345,
			CertificateBytes: &cert,
		}
		result.Azd.Version = "1.5.0"
		result.Azd.Commit = "abc123"

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.Equal(t, float64(5000), parsed["port"])
		assert.Equal(t, float64(12345), parsed["pid"])
		assert.Equal(t, "MIIC+zCCAeOgAwIBAgIJAL...", parsed["certificateBytes"])

		// Embedded VersionResult
		azd := parsed["azd"].(map[string]any)
		assert.Equal(t, "1.5.0", azd["version"])
		assert.Equal(t, "abc123", azd["commit"])
	})

	t.Run("without certificate omits field", func(t *testing.T) {
		result := VsServerResult{
			Port: 5000,
			Pid:  12345,
		}

		data, err := json.Marshal(result)
		require.NoError(t, err)

		var parsed map[string]any
		err = json.Unmarshal(data, &parsed)
		require.NoError(t, err)

		assert.NotContains(t, parsed, "certificateBytes")
	})
}

func TestConsoleMessage_JSON(t *testing.T) {
	msg := ConsoleMessage{Message: "Deploying services..."}

	data, err := json.Marshal(msg)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "Deploying services...", parsed["message"])
}

func TestEventEnvelope_JSON(t *testing.T) {
	ts := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
	envelope := EventEnvelope{
		Type:      ConsoleMessageEventDataType,
		Timestamp: ts,
		Data:      ConsoleMessage{Message: "hello"},
	}

	data, err := json.Marshal(envelope)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	assert.Equal(t, "consoleMessage", parsed["type"])
	assert.Contains(t, parsed["timestamp"], "2024-01-15")
}

func TestEnvListEnvironment_JSON(t *testing.T) {
	env := EnvListEnvironment{
		Name:       "dev",
		IsDefault:  true,
		DotEnvPath: "/home/user/.azure/dev/.env",
		ConfigPath: "/home/user/.azure/dev/config.json",
	}

	data, err := json.Marshal(env)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	// Note: keys use uppercase per json tags
	assert.Equal(t, "dev", parsed["Name"])
	assert.Equal(t, true, parsed["IsDefault"])
	assert.Equal(t, "/home/user/.azure/dev/.env", parsed["DotEnvPath"])
	assert.Equal(t, "/home/user/.azure/dev/config.json", parsed["ConfigPath"])
}

func TestEnvRefreshResult_JSON(t *testing.T) {
	result := EnvRefreshResult{
		Outputs: map[string]EnvRefreshOutputParameter{
			"endpoint": {
				Type:  EnvRefreshOutputTypeString,
				Value: "https://app.example.com",
			},
			"port": {
				Type:  EnvRefreshOutputTypeNumber,
				Value: 8080,
			},
			"enabled": {
				Type:  EnvRefreshOutputTypeBoolean,
				Value: true,
			},
		},
		Resources: []EnvRefreshResource{
			{Id: "/subscriptions/sub1/resourceGroups/rg1"},
			{Id: "/subscriptions/sub1/resourceGroups/rg2"},
		},
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var parsed map[string]any
	err = json.Unmarshal(data, &parsed)
	require.NoError(t, err)

	outputs := parsed["outputs"].(map[string]any)
	assert.Len(t, outputs, 3)

	endpoint := outputs["endpoint"].(map[string]any)
	assert.Equal(t, "string", endpoint["type"])
	assert.Equal(t, "https://app.example.com", endpoint["value"])

	resources := parsed["resources"].([]any)
	assert.Len(t, resources, 2)
}

func TestEnvRefreshOutputType_values(t *testing.T) {
	assert.Equal(t, EnvRefreshOutputType("boolean"), EnvRefreshOutputTypeBoolean)
	assert.Equal(t, EnvRefreshOutputType("string"), EnvRefreshOutputTypeString)
	assert.Equal(t, EnvRefreshOutputType("number"), EnvRefreshOutputTypeNumber)
	assert.Equal(t, EnvRefreshOutputType("object"), EnvRefreshOutputTypeObject)
	assert.Equal(t, EnvRefreshOutputType("array"), EnvRefreshOutputTypeArray)
}

func TestLoginStatus_values(t *testing.T) {
	assert.Equal(t, LoginStatus("success"), LoginStatusSuccess)
	assert.Equal(t, LoginStatus("unauthenticated"), LoginStatusUnauthenticated)
}

func TestAuthStatus_values(t *testing.T) {
	assert.Equal(t, AuthStatus("authenticated"), AuthStatusAuthenticated)
	assert.Equal(t, AuthStatus("unauthenticated"), AuthStatusUnauthenticated)
}

func TestAccountType_values(t *testing.T) {
	assert.Equal(t, AccountType("user"), AccountTypeUser)
	assert.Equal(t, AccountType("servicePrincipal"), AccountTypeServicePrincipal)
}
