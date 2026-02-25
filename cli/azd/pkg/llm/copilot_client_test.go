// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package llm

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCopilotClientManager(t *testing.T) {
	t.Run("NilOptions", func(t *testing.T) {
		mgr := NewCopilotClientManager(nil)
		require.NotNil(t, mgr)
		require.NotNil(t, mgr.Client())
	})

	t.Run("WithLogLevel", func(t *testing.T) {
		mgr := NewCopilotClientManager(&CopilotClientOptions{
			LogLevel: "debug",
		})
		require.NotNil(t, mgr)
		require.NotNil(t, mgr.Client())
	})
}
