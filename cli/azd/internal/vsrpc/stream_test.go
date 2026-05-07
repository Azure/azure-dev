// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package vsrpc

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWsStream_Close_ReturnsNil(t *testing.T) {
	// wsStream.Close is a no-op that returns nil.
	// See TODO in stream.go referencing issue #3286.
	s := wsStream{}
	err := s.Close()
	assert.NoError(t, err)
}
