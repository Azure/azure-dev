// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewAspireService(t *testing.T) {
	s := newTestServer()
	svc := newAspireService(s)
	require.NotNil(t, svc)
	require.Same(t, s, svc.server)
}
