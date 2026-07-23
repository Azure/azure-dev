// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package recording

import (
	"encoding/json"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v3/cassette"
)

func TestSanitizeContainerAppUpdate_IgnoresOtherPatchRequests(t *testing.T) {
	interaction := &cassette.Interaction{
		Request: cassette.Request{
			Method: "PATCH",
			Host:   "management.azure.com",
			URL: "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.Web/sites/app?api-version=2023-12-01",
			Body: `{"properties":{"siteConfig":{"linuxFxVersion":"DOCKER|registry/image:tag"}}}`,
		},
	}
	originalBody := interaction.Request.Body

	require.NoError(t, sanitizeContainerAppUpdate(interaction))
	require.Equal(t, originalBody, interaction.Request.Body)
}

func TestSanitizeContainerAppUpdate_SanitizesSecrets(t *testing.T) {
	interaction := &cassette.Interaction{
		Request: cassette.Request{
			Method: "PATCH",
			Host:   "management.azure.com",
			URL: "https://management.azure.com/subscriptions/sub/resourceGroups/rg/providers/" +
				"Microsoft.App/containerApps/app?api-version=2024-03-01",
			Body: `{"properties":{"configuration":{"secrets":[{"name":"secret","value":"sensitive"}]}}}`,
		},
	}

	require.NoError(t, sanitizeContainerAppUpdate(interaction))

	var app armappcontainers.ContainerApp
	require.NoError(t, json.Unmarshal([]byte(interaction.Request.Body), &app))
	require.NotNil(t, app.Properties)
	require.NotNil(t, app.Properties.Configuration)
	require.Len(t, app.Properties.Configuration.Secrets, 1)
	require.Equal(t, "SANITIZED", *app.Properties.Configuration.Secrets[0].Value)
}
