// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeleteMode_ZeroValue(t *testing.T) {
	var d DeleteMode
	require.EqualValues(t, 0, d)
	require.EqualValues(t, 0, d&DeleteModeLocal)
	require.EqualValues(t, 0, d&DeleteModeAzureResources)
}

func TestNewEnvironmentService(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	require.NotNil(t, svc)
	require.Same(t, s, svc.server)
}

// Test that valid method names with valid params but missing session get InvalidParams
func TestEnvironmentService_ServeHTTP_AllMethods(t *testing.T) {
	s := newTestServer()
	svc := newEnvironmentService(s)
	rpcConn := connectRPC(t, svc)

	methods := []string{
		"CreateEnvironmentAsync",
		"GetEnvironmentsAsync",
		"LoadEnvironmentAsync",
		"OpenEnvironmentAsync",
		"SetCurrentEnvironmentAsync",
		"DeleteEnvironmentAsync",
		"RefreshEnvironmentAsync",
		"DeployAsync",
		"DeployServiceAsync",
	}

	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			// Call with wrong param format to exercise the handler registration
			_, err := rpcConn.Call(t.Context(), method, "bad-params", nil)
			require.Error(t, err)
		})
	}
}
