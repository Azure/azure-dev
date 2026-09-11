// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConnectionParameterNamesUseNormalSubstitution(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"connections", "connectionCredentials", "ConnectionCredentials"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), onDiskParamsFile)
			body := minimalARMParametersFile(t, map[string]any{name: map[string]any{"target": "${NETWORK}"}})
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			parameters, err := loadParametersFile(path, map[string]string{"NETWORK": "network-value"})
			require.NoError(t, err)
			assert.Equal(t, map[string]any{name: map[string]any{
				"value": map[string]any{"target": "network-value"},
			}}, parameters)
		})
	}
}

func TestConnectionNamedSourceParametersReachCompiler(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, file, source string
	}{
		{"Bicep array", onDiskBicepFile, "param connections array = []"},
		{"Bicep secure object", onDiskBicepFile, "@secure()\nparam connectionCredentials object = {}"},
		{"Bicep commented whitespace", onDiskBicepFile, "param /* note */ connections array = []"},
		{"BicepParam credentials", onDiskBicepParamFile,
			"using './main.bicep'\nparam connectionCredentials = {}"},
		{"BicepParam array", onDiskBicepParamFile, "using './main.bicep'\nparam connections = []"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			infraDir := filepath.Join(root, onDiskInfraDir)
			require.NoError(t, os.MkdirAll(infraDir, 0o750))
			path := filepath.Join(infraDir, tt.file)
			require.NoError(t, os.WriteFile(path, []byte(tt.source), 0o600))
			envelope, err := json.Marshal(map[string]string{
				"templateJson": minimalARMTemplate(), "parametersJson": minimalARMParametersFile(t, nil),
			})
			require.NoError(t, err)
			compiler := &stubCompiler{
				buildResult:      bicep.BuildResult{Compiled: minimalARMTemplate()},
				buildParamResult: bicep.BuildResult{Compiled: string(envelope)},
			}
			source, err := loadOnDiskTemplate(t.Context(), root, compiler, nil)
			require.NoError(t, err)
			require.NotNil(t, source)
			assert.Equal(t, 1, len(compiler.buildCalls)+len(compiler.buildParamCalls))
			//nolint:gosec // Test-owned file under t.TempDir.
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tt.source, string(raw))
		})
	}
}

func TestConnectionNamedParametersDoNotMaskCompilerErrors(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	infraDir := filepath.Join(root, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte("param connections array"), 0o600))
	compiler := &stubCompiler{buildErr: errors.New("synthetic compiler failure")}
	source, err := loadOnDiskTemplate(t.Context(), root, compiler, nil)
	require.ErrorContains(t, err, "synthetic compiler failure")
	assert.NotContains(t, err.Error(), "removed generic Connection provisioning contract")
	assert.Nil(t, source)
}

func TestConnectionSourceCheckIgnoresCommentsAndLiterals(t *testing.T) {
	root := t.TempDir()
	infraDir := filepath.Join(root, onDiskInfraDir)
	require.NoError(t, os.MkdirAll(infraDir, 0o750))
	raw := `// param connections array = []
/*
param connectionCredentials object = {}
*/
var example = '''
param connections array = []
'''
@description('Example: param connectionCredentials object')
param custom string = 'quoted \' param connections'
param connectionsEnabled bool = true
`
	require.NoError(t, os.WriteFile(filepath.Join(infraDir, onDiskBicepFile), []byte(raw), 0o600))
	compiler := &stubCompiler{buildResult: bicep.BuildResult{Compiled: minimalARMTemplate()}}
	_, err := loadOnDiskTemplate(t.Context(), root, compiler, nil)
	require.NoError(t, err)
	assert.Len(t, compiler.buildCalls, 1)
}
