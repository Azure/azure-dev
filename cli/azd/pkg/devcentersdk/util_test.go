// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package devcentersdk_test

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func TestSetHttpRequestBody(t *testing.T) {
	t.Parallel()

	t.Run("SerializesStruct", func(t *testing.T) {
		t.Parallel()
		req, err := runtime.NewRequest(t.Context(), http.MethodPost, "https://example.com/api")
		require.NoError(t, err)

		spec := devcentersdk.EnvironmentSpec{
			CatalogName:               "catalog",
			EnvironmentDefinitionName: "definition",
			EnvironmentType:           "Dev",
			Parameters: map[string]any{
				"name": "env1",
			},
		}

		err = devcentersdk.SetHttpRequestBody(req, spec)
		require.NoError(t, err)

		raw := req.Raw()
		require.Equal(t, "application/json", raw.Header.Get("Content-Type"))

		body, err := io.ReadAll(raw.Body)
		require.NoError(t, err)
		require.NoError(t, raw.Body.Close())

		var roundTrip devcentersdk.EnvironmentSpec
		require.NoError(t, json.Unmarshal(body, &roundTrip))
		require.Equal(t, spec.CatalogName, roundTrip.CatalogName)
		require.Equal(t, spec.EnvironmentDefinitionName, roundTrip.EnvironmentDefinitionName)
		require.Equal(t, spec.EnvironmentType, roundTrip.EnvironmentType)
		require.Equal(t, spec.Parameters["name"], roundTrip.Parameters["name"])
	})

	t.Run("ReturnsErrorForUnmarshalableValue", func(t *testing.T) {
		t.Parallel()
		req, err := runtime.NewRequest(t.Context(), http.MethodPost, "https://example.com/api")
		require.NoError(t, err)

		// channels cannot be JSON marshaled
		ch := make(chan int)
		err = devcentersdk.SetHttpRequestBody(req, ch)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed serializing JSON")
	})
}

func TestNewPipeline(t *testing.T) {
	t.Parallel()

	mockContext := mocks.NewMockContext(t.Context())
	pipeline := devcentersdk.NewPipeline(
		mockContext.Credentials,
		devcentersdk.ServiceConfig,
		mockContext.CoreClientOptions,
	)
	require.NotNil(t, pipeline)
}
