// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadARMParametersFileEnvironmentPrecedence(t *testing.T) {
	for _, test := range []struct {
		name    string
		env     map[string]string
		process string
		want    string
	}{
		{name: "process fallback", process: "registry", want: "registry"},
		{name: "escaped process value", process: "registry\"\\\n", want: "registry\"\\\n"},
		{name: "explicit empty", env: map[string]string{"SHARED_REGISTRY": ""}, process: "registry"},
		{name: "persisted value", env: map[string]string{"SHARED_REGISTRY": "persisted"},
			process: "registry", want: "persisted"},
		{name: "empty process value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("SHARED_REGISTRY", test.process)
			path := filepath.Join(t.TempDir(), "main.parameters.json")
			require.NoError(t, os.WriteFile(path, []byte(
				`{"parameters":{"existingAcrResourceId":{"value":"${SHARED_REGISTRY}"}}}`), 0o600))
			parameters, err := LoadARMParametersFile(path, test.env)
			require.NoError(t, err)
			require.Equal(t, map[string]any{
				"existingAcrResourceId": map[string]any{"value": test.want},
			}, parameters)
		})
	}
	t.Run("unset in both sources", func(t *testing.T) {
		t.Setenv("SHARED_REGISTRY", "")
		require.NoError(t, os.Unsetenv("SHARED_REGISTRY"))
		resolved, err := substituteParameter(map[string]any{"value": "${SHARED_REGISTRY}"},
			"main.parameters.json", "existingAcrResourceId", nil)
		require.NoError(t, err)
		require.Nil(t, resolved)
	})
}
