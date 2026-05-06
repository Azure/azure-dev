// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tool

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockexec"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// PackageManagerVersionProvider — npm
// ---------------------------------------------------------------------------

func TestPackageManagerVersionProvider_Npm(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "npm" &&
				len(args.Args) >= 3 &&
				args.Args[0] == "view" &&
				args.Args[1] == "@azure/mcp" &&
				args.Args[2] == "version"
		}).Respond(exec.RunResult{
			Stdout: "1.2.3\n",
		})

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "@azure/mcp",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "npm",
					PackageId:      "@azure/mcp",
				},
			},
		}

		version, err := provider.GetLatestVersion(
			t.Context(), tool,
		)
		require.NoError(t, err)
		assert.Equal(t, "1.2.3", version)
	})

	t.Run("EmptyOutput", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "npm"
		}).Respond(exec.RunResult{Stdout: ""})

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "test-pkg",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "npm",
					PackageId:      "test-pkg",
				},
			},
		}

		_, err := provider.GetLatestVersion(t.Context(), tool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "empty version")
	})

	t.Run("CommandFailure", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "npm"
		}).SetError(errors.New("npm not found"))

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "test-pkg",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "npm",
					PackageId:      "test-pkg",
				},
			},
		}

		_, err := provider.GetLatestVersion(t.Context(), tool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "npm view")
	})
}

// ---------------------------------------------------------------------------
// PackageManagerVersionProvider — winget
// ---------------------------------------------------------------------------

func TestPackageManagerVersionProvider_Winget(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "winget" &&
				len(args.Args) >= 3 &&
				args.Args[0] == "show"
		}).Respond(exec.RunResult{
			Stdout: "Found Azure CLI [Microsoft.AzureCLI]\n" +
				"Version: 2.65.0\n" +
				"Publisher: Microsoft\n",
		})

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "az",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "winget",
					PackageId:      "Microsoft.AzureCLI",
				},
			},
		}

		version, err := provider.GetLatestVersion(
			t.Context(), tool,
		)
		require.NoError(t, err)
		assert.Equal(t, "2.65.0", version)
	})

	t.Run("NoVersionField", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "winget"
		}).Respond(exec.RunResult{
			Stdout: "Found Azure CLI [Microsoft.AzureCLI]\n" +
				"Publisher: Microsoft\n",
		})

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "az",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "winget",
					PackageId:      "Microsoft.AzureCLI",
				},
			},
		}

		_, err := provider.GetLatestVersion(t.Context(), tool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "Version field")
	})
}

// ---------------------------------------------------------------------------
// PackageManagerVersionProvider — brew
// ---------------------------------------------------------------------------

func TestPackageManagerVersionProvider_Brew(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		brewJSON := brewInfoJSON{
			Formulae: []struct {
				Versions struct {
					Stable string `json:"stable"`
				} `json:"versions"`
			}{
				{Versions: struct {
					Stable string `json:"stable"`
				}{Stable: "2.65.0"}},
			},
		}
		data, _ := json.Marshal(brewJSON)

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "brew" &&
				len(args.Args) >= 3 &&
				args.Args[0] == "info"
		}).Respond(exec.RunResult{Stdout: string(data)})

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "az",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "brew",
					PackageId:      "azure-cli",
				},
			},
		}

		version, err := provider.GetLatestVersion(
			t.Context(), tool,
		)
		require.NoError(t, err)
		assert.Equal(t, "2.65.0", version)
	})

	t.Run("InvalidJSON", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "brew"
		}).Respond(exec.RunResult{Stdout: "not json"})

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "az",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "brew",
					PackageId:      "azure-cli",
				},
			},
		}

		_, err := provider.GetLatestVersion(t.Context(), tool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "parsing brew info")
	})
}

// ---------------------------------------------------------------------------
// PackageManagerVersionProvider — no strategy
// ---------------------------------------------------------------------------

func TestPackageManagerVersionProvider_NoStrategy(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	provider := NewPackageManagerVersionProvider(runner)

	tool := &ToolDefinition{
		Id:                "test",
		InstallStrategies: map[string]InstallStrategy{},
	}

	_, err := provider.GetLatestVersion(t.Context(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no install strategy")
}

func TestPackageManagerVersionProvider_UnsupportedManager(
	t *testing.T,
) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()
	provider := NewPackageManagerVersionProvider(runner)

	tool := &ToolDefinition{
		Id: "test",
		InstallStrategies: map[string]InstallStrategy{
			runtime.GOOS: {
				PackageManager: "snap",
				PackageId:      "some-pkg",
			},
		},
	}

	_, err := provider.GetLatestVersion(t.Context(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported package manager")
}

// ---------------------------------------------------------------------------
// PackageManagerVersionProvider — apt
// ---------------------------------------------------------------------------

func TestPackageManagerVersionProvider_Apt(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "apt-cache" &&
				len(args.Args) >= 2 &&
				args.Args[0] == "policy"
		}).Respond(exec.RunResult{
			Stdout: "azure-cli:\n" +
				"  Installed: 2.65.0-1~noble\n" +
				"  Candidate: 2.67.0-1~noble\n" +
				"  Version table:\n",
		})

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "az",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "apt",
					PackageId:      "azure-cli",
				},
			},
		}

		version, err := provider.GetLatestVersion(
			t.Context(), tool,
		)
		require.NoError(t, err)
		assert.Equal(t, "2.67.0", version)
	})

	t.Run("CandidateNone", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "apt-cache"
		}).Respond(exec.RunResult{
			Stdout: "unknown-pkg:\n" +
				"  Installed: (none)\n" +
				"  Candidate: (none)\n",
		})

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "unknown",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "apt",
					PackageId:      "unknown-pkg",
				},
			},
		}

		_, err := provider.GetLatestVersion(t.Context(), tool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no candidate version")
	})

	t.Run("CommandFailure", func(t *testing.T) {
		t.Parallel()

		runner := mockexec.NewMockCommandRunner()
		runner.When(func(args exec.RunArgs, _ string) bool {
			return args.Cmd == "apt-cache"
		}).SetError(errors.New("apt-cache not found"))

		provider := NewPackageManagerVersionProvider(runner)
		tool := &ToolDefinition{
			Id: "az",
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "apt",
					PackageId:      "azure-cli",
				},
			},
		}

		_, err := provider.GetLatestVersion(t.Context(), tool)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "apt-cache policy")
	})
}

// ---------------------------------------------------------------------------
// MarketplaceVersionProvider
// ---------------------------------------------------------------------------

func TestMarketplaceVersionProvider_Success(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			resp := vsMarketplaceResponse{
				Results: []struct {
					Extensions []struct {
						Versions []struct {
							Version string `json:"version"`
						} `json:"versions"`
					} `json:"extensions"`
				}{
					{
						Extensions: []struct {
							Versions []struct {
								Version string `json:"version"`
							} `json:"versions"`
						}{
							{
								Versions: []struct {
									Version string `json:"version"`
								}{
									{Version: "0.30.23"},
								},
							},
						},
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(resp)
		}),
	)
	defer server.Close()

	// Override the marketplace URL for testing.
	provider := &MarketplaceVersionProvider{
		httpClient: server.Client(),
		baseURL:    server.URL,
	}

	tool := &ToolDefinition{
		Id:       "ms-azuretools.vscode-bicep",
		Category: ToolCategoryExtension,
	}

	version, err := provider.GetLatestVersion(t.Context(), tool)
	require.NoError(t, err)
	assert.Equal(t, "0.30.23", version)
}

func TestMarketplaceVersionProvider_HTTPError(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
		}),
	)
	defer server.Close()

	provider := &MarketplaceVersionProvider{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	tool := &ToolDefinition{
		Id:       "ms-azuretools.vscode-bicep",
		Category: ToolCategoryExtension,
	}

	_, err := provider.GetLatestVersion(t.Context(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
}

func TestMarketplaceVersionProvider_EmptyResults(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"results":[]}`)
		}),
	)
	defer server.Close()

	provider := &MarketplaceVersionProvider{
		baseURL:    server.URL,
		httpClient: server.Client(),
	}

	tool := &ToolDefinition{
		Id:       "nonexistent.extension",
		Category: ToolCategoryExtension,
	}

	_, err := provider.GetLatestVersion(t.Context(), tool)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no versions found")
}

// ---------------------------------------------------------------------------
// SelectVersionProvider
// ---------------------------------------------------------------------------

func TestSelectVersionProvider(t *testing.T) {
	t.Parallel()

	runner := mockexec.NewMockCommandRunner()

	t.Run("LibraryWithRegistryReturnsExtensionProvider",
		func(t *testing.T) {
			t.Parallel()

			tool := &ToolDefinition{
				Id:       "azure.ai.agents",
				Category: ToolCategoryLibrary,
			}

			// We can't easily create a real RegistryCacheManager
			// in tests, but we can verify the selection logic by
			// checking nil vs non-nil return for nil cache manager.
			provider := SelectVersionProvider(
				tool, runner, nil, nil,
			)
			assert.Nil(t, provider,
				"should return nil when registry is nil")
		},
	)

	t.Run("ExtensionReturnsMarketplaceProvider", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Id:       "ms-azuretools.vscode-bicep",
			Category: ToolCategoryExtension,
		}

		provider := SelectVersionProvider(
			tool, runner, nil, http.DefaultClient,
		)
		assert.NotNil(t, provider)
		assert.IsType(t,
			&MarketplaceVersionProvider{}, provider,
		)
	})

	t.Run("ExtensionWithNilHTTPReturnsNil", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Id:       "ms-azuretools.vscode-bicep",
			Category: ToolCategoryExtension,
		}

		provider := SelectVersionProvider(
			tool, runner, nil, nil,
		)
		assert.Nil(t, provider)
	})

	t.Run("CLIWithPackageManagerReturnsProvider", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Id:       "az",
			Category: ToolCategoryCLI,
			InstallStrategies: map[string]InstallStrategy{
				runtime.GOOS: {
					PackageManager: "npm",
					PackageId:      "azure-cli",
				},
			},
		}

		provider := SelectVersionProvider(
			tool, runner, nil, nil,
		)
		assert.NotNil(t, provider)
		assert.IsType(t,
			&PackageManagerVersionProvider{}, provider,
		)
	})

	t.Run("CLIWithNoStrategyReturnsNil", func(t *testing.T) {
		t.Parallel()

		tool := &ToolDefinition{
			Id:                "custom",
			Category:          ToolCategoryCLI,
			InstallStrategies: map[string]InstallStrategy{},
		}

		provider := SelectVersionProvider(
			tool, runner, nil, nil,
		)
		assert.Nil(t, provider)
	})

	t.Run("CLIWithNonQueryableManagerReturnsNil",
		func(t *testing.T) {
			t.Parallel()

			tool := &ToolDefinition{
				Id:       "custom",
				Category: ToolCategoryCLI,
				InstallStrategies: map[string]InstallStrategy{
					runtime.GOOS: {
						PackageManager: "code",
						PackageId:      "some-ext",
					},
				},
			}

			provider := SelectVersionProvider(
				tool, runner, nil, nil,
			)
			assert.Nil(t, provider)
		},
	)
}

// ---------------------------------------------------------------------------
// parseWingetVersion
// ---------------------------------------------------------------------------

func TestParseWingetVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name: "StandardOutput",
			output: "Found Azure CLI [Microsoft.AzureCLI]\n" +
				"Version: 2.65.0\n" +
				"Publisher: Microsoft Corporation\n",
			want: "2.65.0",
		},
		{
			name:   "VersionWithPreRelease",
			output: "Version: 1.2.3-beta.1\n",
			want:   "1.2.3-beta.1",
		},
		{
			name:    "NoVersionField",
			output:  "Publisher: Microsoft\nName: Azure CLI\n",
			wantErr: true,
		},
		{
			name:    "EmptyOutput",
			output:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseWingetVersion(tt.output)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// parseAptCandidate
// ---------------------------------------------------------------------------

func TestParseAptCandidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		output  string
		want    string
		wantErr bool
	}{
		{
			name: "StandardOutput",
			output: "azure-cli:\n" +
				"  Installed: 2.65.0-1~noble\n" +
				"  Candidate: 2.67.0-1~noble\n" +
				"  Version table:\n",
			want: "2.67.0",
		},
		{
			name: "VersionWithoutSuffix",
			output: "some-pkg:\n" +
				"  Installed: (none)\n" +
				"  Candidate: 1.2.3\n",
			want: "1.2.3",
		},
		{
			name: "CandidateNone",
			output: "unknown-pkg:\n" +
				"  Installed: (none)\n" +
				"  Candidate: (none)\n",
			wantErr: true,
		},
		{
			name:    "NoCandidateField",
			output:  "azure-cli:\n  Installed: 2.65.0\n",
			wantErr: true,
		},
		{
			name:    "EmptyOutput",
			output:  "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := parseAptCandidate(tt.output)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.want, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// isNewerVersion (semver comparison)
// ---------------------------------------------------------------------------

func TestIsNewerVersion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		latest  string
		current string
		want    bool
	}{
		{
			name:    "NewerVersion",
			latest:  "2.0.0",
			current: "1.0.0",
			want:    true,
		},
		{
			name:    "SameVersion",
			latest:  "1.0.0",
			current: "1.0.0",
			want:    false,
		},
		{
			name:    "OlderVersion",
			latest:  "0.9.0",
			current: "1.0.0",
			want:    false,
		},
		{
			name:    "EmptyLatest",
			latest:  "",
			current: "1.0.0",
			want:    false,
		},
		{
			name:    "EmptyCurrent",
			latest:  "1.0.0",
			current: "",
			want:    false,
		},
		{
			name:    "BothEmpty",
			latest:  "",
			current: "",
			want:    false,
		},
		{
			name:    "PreReleaseNewer",
			latest:  "0.1.29-preview",
			current: "0.1.6-preview",
			want:    true,
		},
		{
			name:    "PreReleaseSame",
			latest:  "0.1.6-preview",
			current: "0.1.6-preview",
			want:    false,
		},
		{
			name:    "StableNewerThanPreRelease",
			latest:  "1.0.0",
			current: "1.0.0-beta.1",
			want:    true,
		},
		{
			name:    "InvalidLatest",
			latest:  "not-a-version",
			current: "1.0.0",
			want:    false,
		},
		{
			name:    "InvalidCurrent",
			latest:  "1.0.0",
			current: "not-a-version",
			want:    false,
		},
		{
			name:    "PatchUpdate",
			latest:  "2.65.1",
			current: "2.65.0",
			want:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want,
				isNewerVersion(tt.latest, tt.current))
		})
	}
}
