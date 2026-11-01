// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"azure.ai.projects/internal/testutil"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBYOResourceTokenUsesActualBicepAndPreservesScopeAndSalt(t *testing.T) {
	tool := testutil.LookupBicep(t)
	var tokens []string
	for _, salt := range []string{"", "custom"} {
		scope := byoNamingScope{
			SubscriptionID:  "/subscriptions/Target-Sub",
			ResourceGroupID: "/subscriptions/Target-Sub/resourceGroups/Target-RG",
			Location:        "westus", Salt: salt,
		}
		var directory string
		compiler := &stubCompiler{buildParam: func(
			ctx context.Context, path string, env []string,
		) (bicep.BuildResult, error) {
			directory = filepath.Dir(path)
			//nolint:gosec // Probe path is created by evaluateBYOResourceToken in a temporary directory.
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Contains(t, string(raw), "uniqueString(scope.subscriptionId, scope.resourceGroupId, scope.location)")
			assert.Contains(t, string(raw),
				"uniqueString(scope.subscriptionId, scope.resourceGroupId, scope.location, scope.salt)")
			//nolint:gosec // Scope file is written next to the test-owned probe.
			raw, err = os.ReadFile(filepath.Join(directory, "scope.json"))
			require.NoError(t, err)
			var actual byoNamingScope
			require.NoError(t, json.Unmarshal(raw, &actual))
			assert.Equal(t, scope, actual)
			assert.Equal(t, []string{"BICEP_PARAMETERS_OVERRIDES={}"}, env)
			//nolint:gosec // Explicit Bicep tool override used by the existing offline template checks.
			command := exec.CommandContext(ctx, tool, "build-params", path, "--stdout")
			command.Env = append(os.Environ(), env...)
			output, err := command.CombinedOutput()
			return bicep.BuildResult{Compiled: string(output)}, err
		}}
		token, err := evaluateBYOResourceToken(t.Context(), compiler, scope)
		require.NoError(t, err)
		require.Regexp(t, officialResourceToken, token)
		assert.Len(t, token, 13)
		tokens = append(tokens, token)
		_, err = os.Stat(directory)
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
	assert.NotEqual(t, tokens[0], tokens[1], "the existing optional salt remains part of default resource naming")
}

func TestBYOResourceTokenSurfacesCompilerAndEvidenceFailures(t *testing.T) {
	cause := errors.New("offline compiler failure")
	_, err := evaluateBYOResourceToken(t.Context(), &stubCompiler{buildParamErr: cause}, byoNamingScope{})
	require.ErrorIs(t, err, cause)
	for _, output := range []string{
		"not-json", `{}`, `{"parametersJson":"{}"}`,
		`{"parametersJson":"{\"parameters\":{\"resourceToken\":{\"value\":\"invalid\"}}}"}`,
	} {
		compiler := &stubCompiler{buildParamResult: bicep.BuildResult{Compiled: output}}
		_, err := evaluateBYOResourceToken(t.Context(), compiler, byoNamingScope{})
		require.Error(t, err)
		require.Len(t, compiler.buildParamCalls, 1)
		_, err = os.Stat(filepath.Dir(compiler.buildParamCalls[0]))
		assert.ErrorIs(t, err, os.ErrNotExist)
	}
}
