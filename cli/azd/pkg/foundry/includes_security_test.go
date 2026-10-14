// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package foundry

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveFileRefsCredentialURLDoesNotLeak(t *testing.T) {
	const ref = "https://USER_MARKER:CREDENTIAL_MARKER@example.com/agents/a.yaml" +
		"?sig=QUERY_MARKER#FRAGMENT_MARKER"

	_, err := ResolveFileRefs(map[string]any{refKey: ref}, t.TempDir())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "remote includes are not supported")
	assert.NotContains(t, err.Error(), "USER_MARKER")
	assert.NotContains(t, err.Error(), "CREDENTIAL_MARKER")
	assert.NotContains(t, err.Error(), "QUERY_MARKER")
	assert.NotContains(t, err.Error(), "FRAGMENT_MARKER")
	assert.NotContains(t, err.Error(), "example.com")
}
