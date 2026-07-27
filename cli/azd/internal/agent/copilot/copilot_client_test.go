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

func TestCopilotClientManager_Stop_NilClient(t *testing.T) {
	t.Parallel()
	m := NewCopilotClientManager(nil, nil)
	require.NoError(t, m.Stop())
	require.Nil(t, m.Client())
}

func TestCopilotClientManager_NewWithOptions(t *testing.T) {
	t.Parallel()
	opts := &CopilotClientOptions{LogLevel: "info", CLIPath: "/tmp/copilot"}
	m := NewCopilotClientManager(opts, nil)
	require.NotNil(t, m)
	require.Equal(t, opts, m.options)
}
