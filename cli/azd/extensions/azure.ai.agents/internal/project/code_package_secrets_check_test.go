// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/require"
)

func TestCheckCodePackageSecrets(t *testing.T) {
	tests := []struct {
		name        string
		codeConfig  map[string]any
		wantWarning bool
	}{
		{
			name:        "code agent",
			codeConfig:  map[string]any{"runtime": "python_3_13", "entryPoint": "main.py"},
			wantWarning: true,
		},
		{
			// .NET bundled mode packages `dotnet publish` output, not the source directory.
			name: "dotnet bundled",
			codeConfig: map[string]any{
				"runtime": "dotnet_10", "entryPoint": "app.dll", "dependencyResolution": "bundled",
			},
		},
		{
			name: "container agent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			files := map[string]string{
				"src/.agentignore": "!.env\n",
				"src/main.py":      "print('ready')\n",
			}
			for _, dir := range []string{"", "a/", "b/", "c/", "d/", "e/", "f/"} {
				files["src/"+dir+".env"] = "API_KEY=secret\n"
			}
			writeTestFiles(t, root, files)

			properties := map[string]any{"kind": "hosted", "name": "worker"}
			if tt.codeConfig != nil {
				properties["codeConfiguration"] = tt.codeConfig
			}
			svc := &azdext.ServiceConfig{
				Name:                 "worker",
				Host:                 foundryAgentHost,
				RelativePath:         "src",
				AdditionalProperties: mustStruct(t, properties),
			}

			messages := CheckCodePackageSecrets(t.Context(), svc, root)
			if !tt.wantWarning {
				require.Empty(t, messages)
				return
			}

			require.Len(t, messages, 1)
			require.Equal(t, azdext.BetaServiceEventMessageWarning, messages[0].Kind)
			require.Contains(t, messages[0].Message, `service "worker"`)
			require.Contains(t, messages[0].Message, ".env, a/.env, b/.env, c/.env, d/.env, and 2 more")
			require.Contains(t, messages[0].Suggestion, ".agentignore")
		})
	}
}
