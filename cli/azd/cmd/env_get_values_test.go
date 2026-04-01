// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"testing"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestEnvGetValuesExport(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		export   bool
		shell    string
		expected string
	}{
		{
			name: "export basic values",
			envVars: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
			export: true,
			shell:  "bash",
			expected: "export AZURE_ENV_NAME=\"test\"\n" +
				"export BAZ=\"qux\"\n" +
				"export FOO=\"bar\"\n",
		},
		{
			name: "export values with special characters",
			envVars: map[string]string{
				"CONN": `host="localhost" pass=$ecret`,
			},
			export: true,
			shell:  "bash",
			expected: "export AZURE_ENV_NAME=\"test\"\n" +
				"export CONN=" +
				`"host=\"localhost\" pass=\$ecret"` +
				"\n",
		},
		{
			name: "export empty value",
			envVars: map[string]string{
				"EMPTY": "",
			},
			export: true,
			shell:  "bash",
			expected: "export AZURE_ENV_NAME=\"test\"\n" +
				"export EMPTY=\"\"\n",
		},
		{
			name: "export values with newlines",
			envVars: map[string]string{
				"MULTILINE": "line1\nline2\nline3",
			},
			export: true,
			shell:  "bash",
			expected: "export AZURE_ENV_NAME=\"test\"\n" +
				"export MULTILINE=$'line1\\nline2\\nline3'\n",
		},
		{
			name: "export values with backslashes",
			envVars: map[string]string{
				"WIN_PATH": `C:\path\to\dir`,
			},
			export: true,
			shell:  "bash",
			expected: "export AZURE_ENV_NAME=\"test\"\n" +
				"export WIN_PATH=\"C:\\\\path\\\\to\\\\dir\"\n",
		},
		{
			name: "export values with backticks and command substitution",
			envVars: map[string]string{
				"DANGEROUS": "value with `backticks` and $(command)",
			},
			export: true,
			shell:  "bash",
			expected: "export AZURE_ENV_NAME=\"test\"\n" +
				"export DANGEROUS=\"value with \\`backticks\\` and \\$(command)\"\n",
		},
		{
			name: "export values with carriage returns",
			envVars: map[string]string{
				"CR_VALUE": "line1\rline2",
			},
			export: true,
			shell:  "bash",
			expected: "export AZURE_ENV_NAME=\"test\"\n" +
				"export CR_VALUE=$'line1\\rline2'\n",
		},
		{
			name: "no export outputs dotenv format",
			envVars: map[string]string{
				"KEY": "value",
			},
			export: false,
			shell:  "bash",
			expected: "AZURE_ENV_NAME=\"test\"\n" +
				"KEY=\"value\"\n",
		},
		{
			name: "export skips invalid shell keys",
			envVars: map[string]string{
				"VALID_KEY":   "ok",
				"bad;key":     "injected",
				"has spaces":  "nope",
				"_UNDERSCORE": "fine",
			},
			export: true,
			shell:  "bash",
			expected: "export AZURE_ENV_NAME=\"test\"\n" +
				"export VALID_KEY=\"ok\"\n" +
				"export _UNDERSCORE=\"fine\"\n",
		},
		{
			name: "pwsh export basic values",
			envVars: map[string]string{
				"FOO": "bar",
				"BAZ": "qux",
			},
			export: true,
			shell:  "pwsh",
			expected: "$env:AZURE_ENV_NAME = \"test\"\n" +
				"$env:BAZ = \"qux\"\n" +
				"$env:FOO = \"bar\"\n",
		},
		{
			name: "pwsh export special characters",
			envVars: map[string]string{
				"CONN": `host="localhost" pass=$ecret`,
			},
			export: true,
			shell:  "pwsh",
			expected: "$env:AZURE_ENV_NAME = \"test\"\n" +
				"$env:CONN = \"host=`\"localhost`\" pass=`$ecret\"\n",
		},
		{
			name: "pwsh export with backticks",
			envVars: map[string]string{
				"CMD": "value with `backtick`",
			},
			export: true,
			shell:  "pwsh",
			expected: "$env:AZURE_ENV_NAME = \"test\"\n" +
				"$env:CMD = \"value with ``backtick``\"\n",
		},
		{
			name: "pwsh export with newlines",
			envVars: map[string]string{
				"MULTILINE": "line1\nline2",
			},
			export: true,
			shell:  "pwsh",
			expected: "$env:AZURE_ENV_NAME = \"test\"\n" +
				"$env:MULTILINE = \"line1`nline2\"\n",
		},
		{
			name: "pwsh export empty value",
			envVars: map[string]string{
				"EMPTY": "",
			},
			export: true,
			shell:  "pwsh",
			expected: "$env:AZURE_ENV_NAME = \"test\"\n" +
				"$env:EMPTY = \"\"\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(
				t.Context(),
			)

			azdCtx := azdcontext.NewAzdContextWithDirectory(
				t.TempDir(),
			)
			err := azdCtx.SetProjectState(
				azdcontext.ProjectState{
					DefaultEnvironment: "test",
				},
			)
			require.NoError(t, err)

			testEnv := environment.New("test")
			for k, v := range tt.envVars {
				testEnv.DotenvSet(k, v)
			}

			envMgr := &mockenv.MockEnvManager{}
			envMgr.On(
				"Get", mock.Anything, "test",
			).Return(testEnv, nil)

			var buf bytes.Buffer
			formatter, err := output.NewFormatter("dotenv")
			require.NoError(t, err)

			action := &envGetValuesAction{
				azdCtx:     azdCtx,
				console:    mockContext.Console,
				envManager: envMgr,
				formatter:  formatter,
				writer:     &buf,
				flags: &envGetValuesFlags{
					global: &internal.GlobalCommandOptions{},
					export: tt.export,
					shell:  tt.shell,
				},
			}

			_, err = action.Run(t.Context())
			require.NoError(t, err)
			require.Equal(t, tt.expected, buf.String())
		})
	}
}

func TestEnvGetValuesExportOutputMutualExclusion(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	azdCtx := azdcontext.NewAzdContextWithDirectory(
		t.TempDir(),
	)
	err := azdCtx.SetProjectState(
		azdcontext.ProjectState{
			DefaultEnvironment: "test",
		},
	)
	require.NoError(t, err)

	formatter, err := output.NewFormatter("json")
	require.NoError(t, err)

	var buf bytes.Buffer
	action := &envGetValuesAction{
		azdCtx:    azdCtx,
		console:   mockContext.Console,
		formatter: formatter,
		writer:    &buf,
		flags: &envGetValuesFlags{
			global: &internal.GlobalCommandOptions{},
			export: true,
			shell:  "bash",
		},
	}

	_, err = action.Run(t.Context())
	require.Error(t, err)
	require.Contains(
		t, err.Error(), "mutually exclusive",
	)
}

func TestEnvGetValuesExportInvalidShell(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	azdCtx := azdcontext.NewAzdContextWithDirectory(
		t.TempDir(),
	)
	err := azdCtx.SetProjectState(
		azdcontext.ProjectState{
			DefaultEnvironment: "test",
		},
	)
	require.NoError(t, err)

	formatter, err := output.NewFormatter("dotenv")
	require.NoError(t, err)

	var buf bytes.Buffer
	action := &envGetValuesAction{
		azdCtx:    azdCtx,
		console:   mockContext.Console,
		formatter: formatter,
		writer:    &buf,
		flags: &envGetValuesFlags{
			global: &internal.GlobalCommandOptions{},
			export: true,
			shell:  "fish",
		},
	}

	_, err = action.Run(t.Context())
	require.Error(t, err)
	require.Contains(
		t, err.Error(), "unsupported shell",
	)
}

func TestEnvGetValuesShellWithoutExport(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())

	azdCtx := azdcontext.NewAzdContextWithDirectory(
		t.TempDir(),
	)
	err := azdCtx.SetProjectState(
		azdcontext.ProjectState{
			DefaultEnvironment: "test",
		},
	)
	require.NoError(t, err)

	formatter, err := output.NewFormatter("dotenv")
	require.NoError(t, err)

	var buf bytes.Buffer
	action := &envGetValuesAction{
		azdCtx:    azdCtx,
		console:   mockContext.Console,
		formatter: formatter,
		writer:    &buf,
		flags: &envGetValuesFlags{
			global: &internal.GlobalCommandOptions{},
			export: false,
			shell:  "pwsh",
		},
	}

	_, err = action.Run(t.Context())
	require.Error(t, err)
	require.Contains(
		t, err.Error(), "--shell requires --export",
	)
}
