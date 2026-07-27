// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewDebugService(t *testing.T) {
	s := newTestServer()
	svc := newDebugService(s)
	require.NotNil(t, svc)
	require.Same(t, s, svc.server)
}

func TestNewDebugService_NilServer(t *testing.T) {
	svc := newDebugService(nil)
	require.NotNil(t, svc)
	require.Nil(t, svc.server)
}
