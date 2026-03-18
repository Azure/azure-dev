// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package copilot

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewCopilotClientManager(t *testing.T) {
	t.Run("NilOptions", func(t *testing.T) {
		mgr := NewCopilotClientManager(nil, nil)
		require.NotNil(t, mgr)
	})

	t.Run("WithLogLevel", func(t *testing.T) {
		mgr := NewCopilotClientManager(&CopilotClientOptions{
			LogLevel: "debug",
		}, nil)
		require.NotNil(t, mgr)
	})
}
