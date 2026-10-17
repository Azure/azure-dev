// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveContainedFileRefsCredentialURLDoesNotLeak(t *testing.T) {
	const ref = "https://USER_MARKER:CREDENTIAL_MARKER@example.test/ref.yaml" +
		"?sig=QUERY_MARKER#FRAGMENT_MARKER"

	_, err := ResolveContainedFileRefs(map[string]any{"$ref": ref}, t.TempDir())
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "USER_MARKER")
	assert.NotContains(t, err.Error(), "CREDENTIAL_MARKER")
	assert.NotContains(t, err.Error(), "QUERY_MARKER")
	assert.NotContains(t, err.Error(), "FRAGMENT_MARKER")
	assert.NotContains(t, err.Error(), "example.test")
}
