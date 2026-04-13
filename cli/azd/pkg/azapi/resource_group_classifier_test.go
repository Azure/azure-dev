// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeResponseError builds an *azcore.ResponseError with the given HTTP status code.
func makeResponseError(statusCode int) error {
	return &azcore.ResponseError{StatusCode: statusCode}
}

// strPtr returns a pointer to the given string.
func strPtr(s string) *string { return new(s) }

// noopOpts returns a ClassifyOptions wired to a specific env name.
func noopOpts(envName string) ClassifyOptions {
	return ClassifyOptions{EnvName: envName}
}

// snapshotOwned returns a ClassifyOptions with SnapshotPredictedRGs set to
// own the given resource group names (lowercased).
func snapshotOwned(envName string, rgs ...string) ClassifyOptions {
	m := make(map[string]bool, len(rgs))
	for _, rg := range rgs {
		m[rg] = true
	}
	return ClassifyOptions{
		EnvName:              envName,
		SnapshotPredictedRGs: m,
	}
}

func TestClassifyResourceGroups(t *testing.T) {
	t.Parallel()

	const (
		rgA     = "rg-alpha"
		rgB     = "rg-beta"
		rgC     = "rg-gamma"
		envName = "myenv"
	)

	t.Run("empty RG list returns empty result", func(t *testing.T) {
		t.Parallel()
		res, err := ClassifyResourceGroups(
			t.Context(), nil, noopOpts(envName))
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	// --- Snapshot unavailable guard ---

	t.Run("snapshot unavailable non-interactive skips all", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA, rgB}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 2)
		assert.Contains(t, res.Skipped[0].Reason, "snapshot unavailable")
		assert.Contains(t, res.Skipped[1].Reason, "snapshot unavailable")
	})

	t.Run("snapshot unavailable interactive prompts user", func(t *testing.T) {
		t.Parallel()
		var prompted []string
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			Prompter: func(rg, reason string) (bool, error) {
				prompted = append(prompted, rg)
				return rg == rgA, nil // accept A, decline B
			},
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA, rgB}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Equal(t, rgB, res.Skipped[0].Name)
		assert.Contains(t, res.Skipped[0].Reason, "user declined")
		assert.Equal(t, []string{rgA, rgB}, prompted)
	})

	t.Run("snapshot unavailable interactive prompt error", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			Prompter: func(_, _ string) (bool, error) {
				return false, fmt.Errorf("terminal closed")
			},
		}
		_, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "terminal closed")
	})

	// --- Snapshot-based classification ---

	t.Run("snapshot owned goes through Tier4", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return []*ResourceWithTags{
				{
					Name: "vm1",
					Type: "Microsoft.Compute/virtualMachines",
					Tags: map[string]*string{
						cAzdEnvNameTag: strPtr(envName),
					},
				},
			}, nil
		}
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	t.Run("snapshot external skips RG", func(t *testing.T) {
		t.Parallel()
		// snapshot contains rgA but not rgB
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return nil, nil
		}
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA, rgB}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Equal(t, rgB, res.Skipped[0].Name)
		assert.Contains(t, res.Skipped[0].Reason, "snapshot")
	})

	// --- Tier 4: Lock veto ---

	t.Run("Tier4 lock CanNotDelete vetoes owned RG", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return []*ManagementLock{
				{Name: "my-lock", LockType: cLockCanNotDelete},
			}, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "lock")
	})

	t.Run("Tier4 lock ReadOnly vetoes owned RG", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return []*ManagementLock{
				{Name: "ro-lock", LockType: cLockReadOnly},
			}, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "lock")
	})

	t.Run("Tier4 lock check 403 does not veto", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, makeResponseError(http.StatusForbidden)
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return nil, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
	})

	t.Run("Tier4 lock check 404 does not veto", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, makeResponseError(http.StatusNotFound)
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return nil, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
	})

	t.Run("Tier4 lock check 500 vetoes as safety", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, makeResponseError(http.StatusInternalServerError)
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "error during safety check")
	})

	// --- Tier 4: Foreign resource veto ---

	t.Run("Tier4 foreign resources vetoes non-interactive", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.Interactive = false
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return []*ResourceWithTags{
				{
					Name: "alien-vm",
					Type: "Microsoft.Compute/virtualMachines",
					Tags: map[string]*string{
						cAzdEnvNameTag: strPtr("other-env"),
					},
				},
			}, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "foreign")
	})

	t.Run("Tier4 foreign resources prompts interactive", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.Interactive = true
		opts.Prompter = func(_, _ string) (bool, error) {
			return true, nil
		}
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return []*ResourceWithTags{
				{
					Name: "alien-vm",
					Type: "Microsoft.Compute/virtualMachines",
					Tags: map[string]*string{
						cAzdEnvNameTag: strPtr("other-env"),
					},
				},
			}, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	t.Run(
		"Tier4 foreign resource prompt declined vetoes",
		func(t *testing.T) {
			t.Parallel()
			opts := snapshotOwned(envName, rgA)
			opts.Interactive = true
			opts.Prompter = func(_, _ string) (bool, error) {
				return false, nil
			}
			opts.ListResourceGroupLocks = func(
				_ context.Context, _ string,
			) ([]*ManagementLock, error) {
				return nil, nil
			}
			opts.ListResourceGroupResources = func(
				_ context.Context, _ string,
			) ([]*ResourceWithTags, error) {
				return []*ResourceWithTags{
					{
						Name: "alien-vm",
						Type: "Microsoft.Compute/virtualMachines",
						Tags: map[string]*string{
							cAzdEnvNameTag: strPtr("other-env"),
						},
					},
				}, nil
			}
			res, err := ClassifyResourceGroups(
				t.Context(), []string{rgA}, opts)
			require.NoError(t, err)
			assert.Empty(t, res.Owned)
			require.Len(t, res.Skipped, 1)
			assert.Contains(t, res.Skipped[0].Reason, "foreign")
		},
	)

	t.Run("Tier4 resource list 404 does not veto", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return nil, makeResponseError(http.StatusNotFound)
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
	})

	t.Run("Tier4 resource list 403 vetoes as safety", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return nil, makeResponseError(http.StatusForbidden)
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "authorization")
	})

	t.Run("Tier4 resource list 500 vetoes as safety", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return nil, makeResponseError(http.StatusInternalServerError)
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "error during safety check")
	})

	t.Run("Tier4 empty envName vetoes for safety", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned("", rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return nil, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "cannot verify")
	})

	t.Run("Tier4 extension resources are skipped", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return []*ResourceWithTags{
				{
					Name: "role-assignment",
					Type: "Microsoft.Authorization/roleAssignments",
					// No azd-env-name tag — should be skipped, not treated as foreign
				},
			}, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	// --- Tag case insensitivity ---

	t.Run("tag matching is case insensitive", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return []*ResourceWithTags{
				{
					Name: "vm1",
					Type: "Microsoft.Compute/virtualMachines",
					Tags: map[string]*string{
						"AZD-ENV-NAME": strPtr("MYENV"),
					},
				},
			}, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	// --- Multi-RG parallelism ---

	t.Run("multiple RGs classified in parallel", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA, rgB, rgC)
		var lockCalls atomic.Int32
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			lockCalls.Add(1)
			return nil, nil
		}
		var resCalls atomic.Int32
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			resCalls.Add(1)
			return []*ResourceWithTags{
				{
					Name: "vm",
					Type: "Microsoft.Compute/virtualMachines",
					Tags: map[string]*string{
						cAzdEnvNameTag: strPtr(envName),
					},
				},
			}, nil
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA, rgB, rgC}, opts)
		require.NoError(t, err)
		assert.Len(t, res.Owned, 3)
		assert.Empty(t, res.Skipped)
		assert.Equal(t, int32(3), lockCalls.Load())
		assert.Equal(t, int32(3), resCalls.Load())
	})

	t.Run("cancelled context vetoes remaining RGs", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // cancel immediately
		opts := snapshotOwned(envName, rgA)
		res, err := ClassifyResourceGroups(ctx, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "error during safety check")
	})
}

func TestIsExtensionResourceType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		resType  string
		expected bool
	}{
		{
			name:     "role assignment",
			resType:  "Microsoft.Authorization/roleAssignments",
			expected: true,
		},
		{
			name:     "role definition",
			resType:  "Microsoft.Authorization/roleDefinitions",
			expected: true,
		},
		{
			name:     "diagnostic setting",
			resType:  "Microsoft.Insights/diagnosticSettings",
			expected: true,
		},
		{
			name:     "resource link",
			resType:  "Microsoft.Resources/links",
			expected: true,
		},
		{
			name:     "case insensitive",
			resType:  "MICROSOFT.AUTHORIZATION/ROLEASSIGNMENTS",
			expected: true,
		},
		{
			name:     "compute VM is not extension",
			resType:  "Microsoft.Compute/virtualMachines",
			expected: false,
		},
		{
			name:     "storage account is not extension",
			resType:  "Microsoft.Storage/storageAccounts",
			expected: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, isExtensionResourceType(tt.resType))
		})
	}
}

func TestClassifyResourceGroups_ForceMode(t *testing.T) {
	t.Parallel()

	const (
		rgA     = "rg-alpha"
		rgB     = "rg-beta"
		envName = "myenv"
	)

	t.Run("without snapshot treats all as owned", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:   envName,
			ForceMode: true,
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA, rgB}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA, rgB}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	t.Run("without snapshot skips all callbacks", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:   envName,
			ForceMode: true,
			ListResourceGroupLocks: func(
				_ context.Context, _ string,
			) ([]*ManagementLock, error) {
				t.Fatal("should not be called")
				return nil, nil
			},
			ListResourceGroupResources: func(
				_ context.Context, _ string,
			) ([]*ResourceWithTags, error) {
				t.Fatal("should not be called")
				return nil, nil
			},
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
	})

	t.Run("with snapshot uses deterministic classification", func(t *testing.T) {
		t.Parallel()
		opts := snapshotOwned(envName, rgA)
		opts.ForceMode = true
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA, rgB}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Equal(t, rgB, res.Skipped[0].Name)
	})

	t.Run(
		"with snapshot skips Tier4 callbacks",
		func(t *testing.T) {
			t.Parallel()
			opts := snapshotOwned(envName, rgA)
			opts.ForceMode = true
			opts.ListResourceGroupLocks = func(
				_ context.Context, _ string,
			) ([]*ManagementLock, error) {
				t.Fatal("should not be called")
				return nil, nil
			}
			opts.ListResourceGroupResources = func(
				_ context.Context, _ string,
			) ([]*ResourceWithTags, error) {
				t.Fatal("should not be called")
				return nil, nil
			}
			res, err := ClassifyResourceGroups(
				t.Context(), []string{rgA}, opts)
			require.NoError(t, err)
			assert.Equal(t, []string{rgA}, res.Owned)
		},
	)
}

func TestClassifyResourceGroups_Snapshot(t *testing.T) {
	t.Parallel()

	const (
		rgA     = "rg-alpha"
		rgB     = "rg-beta"
		envName = "myenv"
	)

	t.Run("nil snapshot falls back to guard", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
			// No SnapshotPredictedRGs
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "snapshot unavailable")
	})

	t.Run("empty snapshot map classifies all as external", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:              envName,
			SnapshotPredictedRGs: map[string]bool{},
			ListResourceGroupResources: func(
				_ context.Context, _ string,
			) ([]*ResourceWithTags, error) {
				return nil, nil
			},
			ListResourceGroupLocks: func(
				_ context.Context, _ string,
			) ([]*ManagementLock, error) {
				return nil, nil
			},
		}
		res, err := ClassifyResourceGroups(
			t.Context(), []string{rgA, rgB}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 2)
		assert.Contains(t, res.Skipped[0].Reason, "snapshot")
	})

	t.Run("snapshot case-insensitive lookup", func(t *testing.T) {
		t.Parallel()
		// predictedRGs has lowercase "rg-alpha"
		opts := snapshotOwned(envName, "rg-alpha")
		opts.ListResourceGroupResources = func(
			_ context.Context, _ string,
		) ([]*ResourceWithTags, error) {
			return nil, nil
		}
		opts.ListResourceGroupLocks = func(
			_ context.Context, _ string,
		) ([]*ManagementLock, error) {
			return nil, nil
		}
		// Query with "rg-alpha" — should match
		res, err := ClassifyResourceGroups(
			t.Context(), []string{"rg-alpha"}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{"rg-alpha"}, res.Owned)
	})

	t.Run(
		"snapshot mixed owned and external",
		func(t *testing.T) {
			t.Parallel()
			opts := snapshotOwned(envName, rgA) // only rgA is owned
			opts.ListResourceGroupResources = func(
				_ context.Context, _ string,
			) ([]*ResourceWithTags, error) {
				return nil, nil
			}
			opts.ListResourceGroupLocks = func(
				_ context.Context, _ string,
			) ([]*ManagementLock, error) {
				return nil, nil
			}
			res, err := ClassifyResourceGroups(
				t.Context(), []string{rgA, rgB}, opts)
			require.NoError(t, err)
			assert.Equal(t, []string{rgA}, res.Owned)
			require.Len(t, res.Skipped, 1)
			assert.Equal(t, rgB, res.Skipped[0].Name)
			assert.Contains(t, res.Skipped[0].Reason,
				"not in predictedResources")
		},
	)
}
