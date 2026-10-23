// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSetDeploymentBinding(t *testing.T) {
	for _, test := range []struct {
		name     string
		bindings map[string]string
		target   string
		want     map[string]string
	}{
		{
			name:     "new declaration",
			bindings: map[string]string{"other": "untouched"},
			target:   "Existing",
			want:     map[string]string{"other": "untouched", "template-chat": "Existing"},
		},
		{
			name:     "preserve existing spelling",
			bindings: map[string]string{"Template-Chat": "old", "other": "untouched"},
			target:   "Existing",
			want:     map[string]string{"Template-Chat": "Existing", "other": "untouched"},
		},
		{
			name:     "update every existing alias",
			bindings: map[string]string{"Template-Chat": "old", "TEMPLATE-CHAT": "", "other": "untouched"},
			target:   "Existing",
			want:     map[string]string{"Template-Chat": "Existing", "TEMPLATE-CHAT": "Existing", "other": "untouched"},
		},
		{
			name:     "skip existing declaration",
			bindings: map[string]string{"Template-Chat": "old", "other": "untouched"},
			want:     map[string]string{"Template-Chat": "", "other": "untouched"},
		},
		{
			name:     "skip new declaration",
			bindings: map[string]string{},
			want:     map[string]string{"template-chat": ""},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			bindings := maps.Clone(test.bindings)
			SetDeploymentBinding(bindings, "template-chat", test.target)
			require.Equal(t, test.want, bindings)
		})
	}
}

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
