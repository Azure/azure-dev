// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerapps

import (
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCreateApiVersionPolicy(t *testing.T) {
	t.Run("nil options returns nil", func(t *testing.T) {
		result := createApiVersionPolicy(nil)
		assert.Nil(t, result)
	})

	t.Run("empty api version returns nil", func(t *testing.T) {
		result := createApiVersionPolicy(
			&ContainerAppOptions{ApiVersion: ""},
		)
		assert.Nil(t, result)
	})

	t.Run("non-empty api version returns policy",
		func(t *testing.T) {
			opts := &ContainerAppOptions{
				ApiVersion: "2024-02-02-preview",
			}
			result := createApiVersionPolicy(opts)
			require.NotNil(t, result)
			assert.Equal(
				t, "2024-02-02-preview", result.apiVersion,
			)
		})
}

func TestWithApiVersionSuggestion(t *testing.T) {
	t.Run("wraps error with suggestion", func(t *testing.T) {
		original := errors.New("some api error")
		wrapped := withApiVersionSuggestion(original)

		require.Error(t, wrapped)
		// The error message should be the original
		assert.Equal(t, "some api error", wrapped.Error())

		// Should be an ErrorWithSuggestion
		var sugErr *internal.ErrorWithSuggestion
		require.True(t, errors.As(wrapped, &sugErr))
		assert.Contains(
			t, sugErr.Suggestion, "apiVersion",
		)
		assert.Contains(
			t, sugErr.Suggestion, "azure.yaml",
		)
	})

	t.Run("underlying error preserved", func(t *testing.T) {
		sentinel := errors.New("sentinel")
		wrapped := withApiVersionSuggestion(sentinel)

		var sugErr *internal.ErrorWithSuggestion
		require.True(t, errors.As(wrapped, &sugErr))
		assert.True(t, errors.Is(sugErr.Err, sentinel))
	})
}

func TestContainerAppOptions(t *testing.T) {
	t.Run("zero value has empty api version",
		func(t *testing.T) {
			opts := ContainerAppOptions{}
			assert.Equal(t, "", opts.ApiVersion)
		})

	t.Run("api version can be set", func(t *testing.T) {
		opts := ContainerAppOptions{
			ApiVersion: "2025-02-02-preview",
		}
		assert.Equal(
			t, "2025-02-02-preview", opts.ApiVersion,
		)
	})
}

func TestContainerAppIngressConfiguration(t *testing.T) {
	t.Run("empty hostnames", func(t *testing.T) {
		config := ContainerAppIngressConfiguration{
			HostNames: []string{},
		}
		assert.Empty(t, config.HostNames)
	})

	t.Run("single hostname", func(t *testing.T) {
		config := ContainerAppIngressConfiguration{
			HostNames: []string{"myapp.azurecontainerapps.io"},
		}
		require.Len(t, config.HostNames, 1)
		assert.Equal(
			t,
			"myapp.azurecontainerapps.io",
			config.HostNames[0],
		)
	})

	t.Run("multiple hostnames", func(t *testing.T) {
		config := ContainerAppIngressConfiguration{
			HostNames: []string{
				"myapp.azurecontainerapps.io",
				"custom.domain.com",
			},
		}
		require.Len(t, config.HostNames, 2)
	})
}
