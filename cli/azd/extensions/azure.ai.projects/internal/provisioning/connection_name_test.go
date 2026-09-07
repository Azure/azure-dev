// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLegacyConnectionParameterNamesRejectedBeforeSubstitution(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"connections", "connectionCredentials"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), onDiskParamsFile)
			// A malformed env expression would fail if substitution were reached.
			body := minimalARMParametersFile(t, map[string]any{name: "${unterminated"})
			require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
			parameters, err := loadParametersFile(path, nil)
			requireConnectionMigrationError(t, err)
			assert.Nil(t, parameters)
		})
	}
}

func TestLegacyConnectionSourceRejectedBeforeCompile(t *testing.T) {
	for _, tt := range []struct {
		name, file, source string
	}{
		{"Bicep array", onDiskBicepFile, "param connections array = []"},
		{"Bicep secure object", onDiskBicepFile, "@secure()\nparam connectionCredentials object = {}"},
		{"Bicep commented whitespace", onDiskBicepFile, "param /* note */ connections array = []"},
		{"BicepParam credentials", onDiskBicepParamFile,
			"using './main.bicep'\nparam connectionCredentials = readEnvironmentVariable('UNSET_KEY')"},
		{"BicepParam array", onDiskBicepParamFile, "using './main.bicep'\nparam connections = []"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			infraDir := filepath.Join(root, onDiskInfraDir)
			require.NoError(t, os.MkdirAll(infraDir, 0o750))
			path := filepath.Join(infraDir, tt.file)
			require.NoError(t, os.WriteFile(path, []byte(tt.source), 0o600))
			compiler := &stubCompiler{
				buildErr:      errors.New("must not compile"),
				buildParamErr: errors.New("must not evaluate credentials"),
			}
			source, err := loadOnDiskTemplate(t.Context(), root, compiler, nil)
			requireConnectionMigrationError(t, err)
			assert.Nil(t, source)
			assert.Empty(t, compiler.buildCalls)
			assert.Empty(t, compiler.buildParamCalls)
			//nolint:gosec // Test-owned file under t.TempDir.
			raw, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tt.source, string(raw))
		})
	}
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
