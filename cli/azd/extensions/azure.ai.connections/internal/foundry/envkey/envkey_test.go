// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package envkey

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestConnectionProjectEndpoint(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"search":         "CONNECTION_SEARCH_PROJECT_ENDPOINT",
		"my connection":  "CONNECTION_MY_CONNECTION_PROJECT_ENDPOINT",
		"my--connection": "CONNECTION_MY_CONNECTION_PROJECT_ENDPOINT",
	}
	for name, expected := range tests {
		assert.Equal(t, expected, ConnectionProjectEndpoint(name))
	}
}
