// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

func TestPromptModelInfrastructureOnDiskPaths(t *testing.T) {
	for _, layered := range []bool{false, true} {
		t.Run(fmt.Sprintf("layered=%t", layered), func(t *testing.T) {
			for _, tt := range []struct {
				name     string
				path     string
				module   string
				provider string
				file     string
				symlink  string
				errField string
				errText  string
			}{
				{
					name: "parent path", path: "../outside",
					errField: "path", errText: "must not contain '..'",
				},
				{
					name: "absolute path", path: "/outside",
					errField: "path", errText: "must not be absolute",
				},
				{
					name: "parent module", module: "../outside/main",
					errField: "module", errText: "must not contain '..'",
				},
				{
					name: "absolute module", module: "/outside/main",
					errField: "module", errText: "must not be absolute",
				},
				{
					name: "Terraform parent path", provider: "terraform", path: "../outside",
					errField: "path", errText: "must not contain '..'",
				},
				{
					name: "Terraform parent module", provider: "terraform", module: "../outside/main",
					errField: "module", errText: "must not contain '..'",
				},
				{
					name: "outside infra directory", symlink: "infra",
					errField: "path", errText: "escapes project root",
				},
				{
					name: "outside module file", symlink: "infra/main.bicep",
					errField: "module", errText: "escapes project root",
				},
				{
					name: "outside parameter file", symlink: "infra/main.bicepparam",
					errField: "module", errText: "escapes project root",
				},
				{
					name: "outside Terraform marker", provider: "terraform", symlink: "infra/.azd-foundry",
					errField: "path", errText: "escapes project root",
				},
				{
					name: "outside Terraform source", provider: "terraform", symlink: "infra/main.tf",
					errField: "path", errText: "escapes project root",
				},
				{name: "defaults", file: "infra/main.bicep"},
				{name: "nested module", module: "nested/models", file: "infra/nested/models.bicep"},
				{
					name: "nested module with suffix", module: "nested/models.bicep",
					file: "infra/nested/models.bicepparam",
				},
				{name: "root directory", path: ".", file: "main.bicep"},
			} {
				t.Run(tt.name, func(t *testing.T) {
					root := t.TempDir()
					outside := t.TempDir()
					provider := tt.provider
					if provider == "" {
						provider = "microsoft.foundry"
					}
					target := map[string]any{"provider": provider, "path": tt.path, "module": tt.module}
					infra := target
					field := "infra"
					if layered {
						infra = map[string]any{"layers": []any{target}}
						field = "infra.layers[0]"
					}
					config, err := yaml.Marshal(map[string]any{"infra": infra})
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), config, 0600))
					if tt.file != "" {
						file := filepath.Join(root, filepath.FromSlash(tt.file))
						require.NoError(t, os.MkdirAll(filepath.Dir(file), 0700))
						require.NoError(t, os.WriteFile(file, nil, 0600))
					}
					if tt.symlink != "" {
						target := outside
						if tt.symlink != "infra" {
							require.NoError(t, os.MkdirAll(filepath.Join(root, "infra"), 0700))
							target = filepath.Join(outside, filepath.Base(tt.symlink))
							require.NoError(t, os.WriteFile(target, []byte("terraform-v1\n"), 0600))
						}
						err := os.Symlink(target, filepath.Join(root, filepath.FromSlash(tt.symlink)))
						if err != nil && runtime.GOOS == "windows" {
							t.Skipf("cannot create symlink: %v", err)
						}
						require.NoError(t, err)
					}

					got, err := promptModelInfrastructureOnDisk(root)
					if tt.errField != "" {
						require.ErrorContains(t, err, "azure.yaml "+field+"."+tt.errField)
						require.ErrorContains(t, err, tt.errText)
						require.NotNil(t, errors.Unwrap(err))
						require.False(t, got)
						return
					}
					require.NoError(t, err)
					require.True(t, got)
				})
			}
		})
	}
}
