// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeploymentBindings(t *testing.T) {
	want := map[string]string{"chat": "existing-chat", "embeddings": "existing-embeddings", "skipped": ""}
	encoded, err := EncodeDeploymentBindings(want)
	require.NoError(t, err)
	for _, value := range []string{
		encoded, `{"chat":"existing-chat","embeddings":"existing-embeddings","skipped":""}`,
	} {
		got, err := DecodeDeploymentBindings(value)
		require.NoError(t, err)
		require.Equal(t, want, got)
	}
	got, err := DecodeDeploymentBindings("")
	require.NoError(t, err)
	require.Empty(t, got)
	for _, value := range []string{"{", "[]", "null", `{"chat":5}`, `{"":"existing"}`} {
		_, err := DecodeDeploymentBindings(value)
		require.ErrorContains(t, err, DeploymentBindingsEnvKey)
	}
}
