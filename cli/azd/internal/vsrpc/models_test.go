// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgressMessage_WithMessage(t *testing.T) {
	before := time.Now().Add(-time.Second)
	original := ProgressMessage{
		Message:            "original",
		Severity:           Warning,
		Kind:               Important,
		Code:               "E001",
		AdditionalInfoLink: "https://example.com",
	}

	updated := original.WithMessage("updated text")

	// Message and Time should change
	assert.Equal(t, "updated text", updated.Message)
	assert.True(
		t, updated.Time.After(before),
		"Time should be set to now",
	)

	// Other fields should be preserved
	assert.Equal(t, Warning, updated.Severity)
	assert.Equal(t, Important, updated.Kind)
	assert.Equal(t, "E001", updated.Code)
	assert.Equal(
		t, "https://example.com", updated.AdditionalInfoLink,
	)

	// Original should be unchanged (value receiver)
	assert.Equal(t, "original", original.Message)
}

func TestNewInfoProgressMessage(t *testing.T) {
	before := time.Now()
	msg := newInfoProgressMessage("hello info")
	after := time.Now()

	assert.Equal(t, "hello info", msg.Message)
	assert.Equal(t, Info, msg.Severity)
	assert.Equal(t, Logging, msg.Kind)
	assert.True(
		t,
		!msg.Time.Before(before) && !msg.Time.After(after),
		"Time should be approximately now",
	)
	assert.Empty(t, msg.Code)
	assert.Empty(t, msg.AdditionalInfoLink)
}

func TestNewImportantProgressMessage(t *testing.T) {
	before := time.Now()
	msg := newImportantProgressMessage("hello important")
	after := time.Now()

	assert.Equal(t, "hello important", msg.Message)
	assert.Equal(t, Info, msg.Severity)
	assert.Equal(t, Important, msg.Kind)
	assert.True(
		t,
		!msg.Time.Before(before) && !msg.Time.After(after),
		"Time should be approximately now",
	)
}

func TestMessageSeverity_Values(t *testing.T) {
	assert.Equal(t, MessageSeverity(0), Info)
	assert.Equal(t, MessageSeverity(1), Warning)
	assert.Equal(t, MessageSeverity(2), Error)
}

func TestMessageKind_Values(t *testing.T) {
	assert.Equal(t, MessageKind(0), Logging)
	assert.Equal(t, MessageKind(1), Important)
}

func TestDeleteMode_BitFlags(t *testing.T) {
	// Verify they are distinct bit flags (use EqualValues
	// since iota constants are untyped int, DeleteMode is uint32)
	assert.EqualValues(t, 1, DeleteModeLocal)
	assert.EqualValues(t, 2, DeleteModeAzureResources)

	// Verify they can be combined
	combined := DeleteModeLocal | DeleteModeAzureResources
	assert.True(t, combined&DeleteModeLocal != 0)
	assert.True(t, combined&DeleteModeAzureResources != 0)

	// Verify single flags don't overlap
	assert.EqualValues(
		t, 0, DeleteModeLocal&DeleteModeAzureResources,
	)
}

func TestEnvironment_JSONRoundTrip(t *testing.T) {
	endpoint := "https://api.example.com"
	resourceId := "/subscriptions/sub-id/rg/rg-name"

	env := Environment{
		Name:      "dev",
		IsCurrent: true,
		Properties: map[string]string{
			"Subscription": "sub-123",
			"Location":     "eastus",
		},
		Services: []*Service{
			{
				Name:       "web",
				IsExternal: false,
				Path:       "./src/web",
				Endpoint:   &endpoint,
				ResourceId: &resourceId,
			},
		},
		Values: map[string]string{
			"AZURE_LOCATION": "eastus",
		},
		LastDeployment: &DeploymentResult{
			Success:      true,
			Time:         time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			Message:      "Deployed successfully",
			DeploymentId: "deploy-abc",
		},
		Resources: []*Resource{
			{
				Name: "rg-dev",
				Type: "Microsoft.Resources/resourceGroups",
				Id:   "/subscriptions/sub-123/resourceGroups/rg-dev",
			},
		},
	}

	data, err := json.Marshal(env)
	require.NoError(t, err)

	var decoded Environment
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, env.Name, decoded.Name)
	assert.Equal(t, env.IsCurrent, decoded.IsCurrent)
	assert.Equal(t, env.Properties, decoded.Properties)
	require.Len(t, decoded.Services, 1)
	assert.Equal(t, "web", decoded.Services[0].Name)
	assert.Equal(t, env.Values, decoded.Values)
	require.NotNil(t, decoded.LastDeployment)
	assert.Equal(
		t, env.LastDeployment.DeploymentId,
		decoded.LastDeployment.DeploymentId,
	)
	require.Len(t, decoded.Resources, 1)
	assert.Equal(t, "rg-dev", decoded.Resources[0].Name)
}

func TestEnvironment_OmitsNilLastDeployment(t *testing.T) {
	env := Environment{
		Name:           "prod",
		LastDeployment: nil,
	}

	data, err := json.Marshal(env)
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	_, has := raw["LastDeployment"]
	assert.False(t, has, "nil LastDeployment should be omitted")
}

func TestService_OmitsNilOptionalFields(t *testing.T) {
	svc := Service{
		Name:       "api",
		IsExternal: true,
		Path:       "./src/api",
		Endpoint:   nil,
		ResourceId: nil,
	}

	data, err := json.Marshal(svc)
	require.NoError(t, err)

	var raw map[string]any
	err = json.Unmarshal(data, &raw)
	require.NoError(t, err)

	_, hasEndpoint := raw["Endpoint"]
	_, hasResourceId := raw["ResourceId"]
	assert.False(t, hasEndpoint, "nil Endpoint should be omitted")
	assert.False(
		t, hasResourceId, "nil ResourceId should be omitted",
	)
}

func TestInitializeServerOptions_JSON(t *testing.T) {
	tests := []struct {
		name string
		opts InitializeServerOptions
	}{
		{
			name: "all nil",
			opts: InitializeServerOptions{},
		},
		{
			name: "all set",
			opts: InitializeServerOptions{
				AuthenticationEndpoint:    new("https://auth.local"),
				AuthenticationKey:         new("secret-key"),
				AuthenticationCertificate: new("base64cert=="),
			},
		},
		{
			name: "partial",
			opts: InitializeServerOptions{
				AuthenticationEndpoint: new("https://auth.local"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.opts)
			require.NoError(t, err)

			var decoded InitializeServerOptions
			err = json.Unmarshal(data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.opts, decoded)
		})
	}
}

func TestRequestContext_Fields(t *testing.T) {
	rc := RequestContext{
		Session:         Session{Id: "sess-123"},
		HostProjectPath: "/home/user/project",
	}

	assert.Equal(t, "sess-123", rc.Session.Id)
	assert.Equal(t, "/home/user/project", rc.HostProjectPath)
}

func TestEnvironmentInfo_Fields(t *testing.T) {
	info := EnvironmentInfo{
		Name:       "staging",
		IsCurrent:  true,
		DotEnvPath: "/home/user/.env",
	}

	assert.Equal(t, "staging", info.Name)
	assert.True(t, info.IsCurrent)
	assert.Equal(t, "/home/user/.env", info.DotEnvPath)
}

func TestAspireHost_Fields(t *testing.T) {
	host := AspireHost{
		Name: "my-aspire-host",
		Path: "/path/to/apphost.csproj",
		Services: []*Service{
			{Name: "api", Path: "./api"},
			{Name: "web", Path: "./web"},
		},
	}

	assert.Equal(t, "my-aspire-host", host.Name)
	assert.Len(t, host.Services, 2)
}
