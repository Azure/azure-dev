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

func TestDeploymentBindingLookupAndMembership(t *testing.T) {
	bindings := map[string]string{
		" Chat ":        "existing-chat",
		"MyDeployment ": "existing-deployment",
		"Skipped":       "",
	}

	target, ok := LookupDeploymentBinding(bindings, "chat")
	require.True(t, ok)
	require.Equal(t, "existing-chat", target)

	target, ok = LookupDeploymentBinding(bindings, "mydeployment")
	require.True(t, ok)
	require.Equal(t, "existing-deployment", target)

	target, ok = LookupDeploymentBinding(bindings, " skipped ")
	require.True(t, ok)
	require.Equal(t, "", target)

	_, ok = LookupDeploymentBinding(bindings, "undeclared")
	require.False(t, ok)

	_, ok = LookupDeploymentBinding(bindings, "   ")
	require.False(t, ok)

	key, ok := FindDeploymentBindingKey(bindings, "CHAT")
	require.True(t, ok)
	require.Equal(t, " Chat ", key)

	require.True(t, ContainsDeploymentName([]string{" Chat ", "Embeddings"}, "chat"))
	require.True(t, ContainsDeploymentName([]string{" Chat ", "Embeddings"}, " embeddings "))
	require.False(t, ContainsDeploymentName([]string{" Chat ", "Embeddings"}, "vision"))
	require.False(t, ContainsDeploymentName([]string{" Chat ", "Embeddings"}, "  "))
}
