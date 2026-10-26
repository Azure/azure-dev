// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalizeEndpointAzureHosts(t *testing.T) {
	for _, test := range []struct{ endpoint, want string }{
		{"https://RESOURCE.services.ai.azure.com/", "https://resource.services.ai.azure.com/contentunderstanding"},
		{"https://resource.cognitiveservices.azure.com",
			"https://resource.cognitiveservices.azure.com/contentunderstanding"},
		{"https://resource.services.ai.azure.com:443", "https://resource.services.ai.azure.com:443/contentunderstanding"},
		{" https://resource.services.ai.azure.com/contentunderstanding/ ",
			"https://resource.services.ai.azure.com/contentunderstanding"},
	} {
		t.Run(test.endpoint, func(t *testing.T) {
			got, err := NormalizeEndpoint(test.endpoint)
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestUntrustedEndpointsFailBeforeTokenAcquisition(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	defer server.Close()
	for _, endpoint := range []string{
		server.URL, "https://private-attacker.example",
		"https://resource.services.ai.azure.com.private-attacker.example",
		"https://resource.cognitiveservices.azure.com.private-attacker.example",
		"https://resource-services.ai.azure.com", "https://resource-cognitiveservices.azure.com",
		"https://services.ai.azure.com", "https://cognitiveservices.azure.com",
		"https://nested.resource.services.ai.azure.com", "https://resource.services.ai.azure.com.",
		"https://resource..services.ai.azure.com", "https://-resource.services.ai.azure.com",
		"https://resource-.services.ai.azure.com", "https://localhost", "https://127.0.0.1", "https://[::1]",
		"https://resource.services.ai.azure.com:8443", "https://resource.services.ai.azure.com:0443",
		"https://resource.services.ai.azure.com:",
		"https://private-user:private-password@resource.services.ai.azure.com",
		"https://resource.services.ai.azure.com?sig=private-signature#private-fragment",
	} {
		t.Run(endpoint, func(t *testing.T) {
			_, err := NormalizeEndpoint(endpoint)
			require.Error(t, err)
			assert.NotContains(t, err.Error(), "private")
			credential := &testCredential{}
			client, err := NewRESTTransport(endpoint, credential, &TransportOptions{Version: "0.1.0-preview"})
			require.Error(t, err)
			assert.Nil(t, client)
			assert.NotContains(t, err.Error(), "private")
			assert.Empty(t, credential.options, "reject an untrusted endpoint before acquiring a token")
		})
	}
	assert.Zero(t, hits.Load(), "untrusted endpoints must not receive any requests")
}

func TestCustomEndpointRequiresTrustedTransport(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		assert.Equal(t, "Bearer private-access-token", request.Header.Get("Authorization"))
		_, err := w.Write([]byte(`{"modelDeployments":{}}`))
		assert.NoError(t, err)
	}))
	defer server.Close()
	credential := &testCredential{}
	client, err := NewRESTTransport(server.URL, credential, &TransportOptions{Transport: server.Client()})
	require.NoError(t, err)
	_, err = client.GetDefaults(t.Context())
	require.NoError(t, err)
	assert.Len(t, credential.options, 1)
}
