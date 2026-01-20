// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package openai

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewOpenAIProvider(t *testing.T) {
	t.Run("WithNilClient", func(t *testing.T) {
		provider := NewOpenAIProvider(nil)

		require.NotNil(t, provider)
		require.Nil(t, provider.client)
	})
}

func TestOpenAIProvider_UploadFile_Validation(t *testing.T) {
	// Tests input validation that prevents invalid API calls
	provider := NewOpenAIProvider(nil)

	t.Run("EmptyPath_ReturnsError", func(t *testing.T) {
		fileID, err := provider.UploadFile(context.Background(), "")

		require.Error(t, err)
		require.Empty(t, fileID)
		require.Contains(t, err.Error(), "file path cannot be empty")
	})
}
