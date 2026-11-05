// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package recording

import (
	"encoding/json"
	"net/url"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
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

func TestSanitizeBlobStorageSasSig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		request  cassette.Request
		wantURL  string
		wantForm url.Values
	}{
		{
			name: "signature in URL and form",
			request: cassette.Request{
				Host: "storage.blob.core.windows.net",
				URL:  "https://storage.blob.core.windows.net/container/blob?sig=sensitive&sv=version",
				Form: url.Values{"sig": {"sensitive", "another-sensitive"}, "sv": {"version"}},
			},
			wantURL:  "https://storage.blob.core.windows.net/container/blob?sig=SANITIZED&sv=version",
			wantForm: url.Values{"sig": {"SANITIZED"}, "sv": {"version"}},
		},
		{
			name: "nil form",
			request: cassette.Request{
				Host: "storage.blob.core.windows.net",
				URL:  "https://storage.blob.core.windows.net/container/blob?sig=sensitive",
			},
			wantURL: "https://storage.blob.core.windows.net/container/blob?sig=SANITIZED",
		},
		{
			name: "no signature",
			request: cassette.Request{
				Host: "storage.blob.core.windows.net",
				URL:  "https://storage.blob.core.windows.net/container/blob?sv=version",
				Form: url.Values{"sv": {"version"}},
			},
			wantURL:  "https://storage.blob.core.windows.net/container/blob?sv=version",
			wantForm: url.Values{"sv": {"version"}},
		},
		{
			name: "non-blob host",
			request: cassette.Request{
				Host: "example.com",
				URL:  "https://example.com/blob?sig=sensitive",
				Form: url.Values{"sig": {"sensitive"}},
			},
			wantURL:  "https://example.com/blob?sig=sensitive",
			wantForm: url.Values{"sig": {"sensitive"}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			interaction := &cassette.Interaction{Request: tt.request}

			require.NoError(t, sanitizeBlobStorageSasSig(interaction))
			require.Equal(t, tt.wantURL, interaction.Request.URL)
			require.Equal(t, tt.wantForm, interaction.Request.Form)
		})
	}
}
