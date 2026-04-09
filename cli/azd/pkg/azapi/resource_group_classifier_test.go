// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azapi

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// makeOperation builds a minimal DeploymentOperation for testing.
func makeOperation(provisioningOp, resourceType, resourceName string) *armresources.DeploymentOperation {
	po := armresources.ProvisioningOperation(provisioningOp)
	return &armresources.DeploymentOperation{
		Properties: &armresources.DeploymentOperationProperties{
			ProvisioningOperation: &po,
			TargetResource: &armresources.TargetResource{
				ResourceType: &resourceType,
				ResourceName: &resourceName,
			},
		},
	}
}

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

func TestClassifyResourceGroups(t *testing.T) {
	t.Parallel()

	const (
		rgA     = "rg-alpha"
		rgB     = "rg-beta"
		rgC     = "rg-gamma"
		envName = "myenv"
	)

	rgOp := "Microsoft.Resources/resourceGroups"

	t.Run("empty RG list returns empty result", func(t *testing.T) {
		t.Parallel()
		res, err := ClassifyResourceGroups(t.Context(), nil, nil, noopOpts(envName))
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	t.Run("Tier1 owned — Create operation", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Create", rgOp, rgA),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	t.Run("Tier1 external — Read operation", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Read", rgOp, rgA),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Equal(t, rgA, res.Skipped[0].Name)
		assert.Contains(t, res.Skipped[0].Reason, "Tier 1")
	})

	t.Run("Tier1 external — EvaluateDeploymentOutput operation", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("EvaluateDeploymentOutput", rgOp, rgA),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Equal(t, rgA, res.Skipped[0].Name)
	})

	t.Run("Tier1 unknown — no matching operations falls to Tier2 then Tier3 non-interactive skip", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				// Only one tag — not dual-tagged → unknown
				return map[string]*string{cAzdEnvNameTag: strPtr(envName)}, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "Tier 3")
	})

	t.Run("Tier1 nil safety — operations with nil properties ignored", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			nil,
			{Properties: nil},
			{Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: nil,
			}},
			{Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: func() *armresources.ProvisioningOperation {
					p := armresources.ProvisioningOperation("Create")
					return &p
				}(),
				TargetResource: nil,
			}},
			{Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: func() *armresources.ProvisioningOperation {
					p := armresources.ProvisioningOperation("Create")
					return &p
				}(),
				TargetResource: &armresources.TargetResource{
					ResourceType: nil,
					ResourceName: nil,
				},
			}},
			// This one is valid and should be picked up.
			makeOperation("Create", rgOp, rgA),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
	})

	t.Run("Tier1 case-insensitive provisioning operation", func(t *testing.T) {
		t.Parallel()
		for _, op := range []string{"create", "CREATE", "Create", "cReAtE"} {
			t.Run(op, func(t *testing.T) {
				t.Parallel()
				ops := []*armresources.DeploymentOperation{makeOperation(op, rgOp, rgA)}
				res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
				require.NoError(t, err)
				assert.Equal(t, []string{rgA}, res.Owned)
			})
		}
	})

	t.Run("Tier2 owned — both tags match env name", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return map[string]*string{
					cAzdEnvNameTag:       strPtr(envName),
					cAzdProvisionHashTag: strPtr("abc123"),
				}, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
	})

	t.Run("Tier2 unknown — only one tag present", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return map[string]*string{cAzdEnvNameTag: strPtr(envName)}, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Equal(t, rgA, res.Skipped[0].Name)
	})

	t.Run("Tier2 unknown — both tags present but wrong env name", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return map[string]*string{
					cAzdEnvNameTag:       strPtr("different-env"),
					cAzdProvisionHashTag: strPtr("abc123"),
				}, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "Tier 3")
	})

	t.Run("Tier2 tag fetch 404 — already deleted skip", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return nil, makeResponseError(http.StatusNotFound)
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "already deleted")
	})

	t.Run("Tier2 tag fetch 403 — falls to Tier3 non-interactive skip", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return nil, makeResponseError(http.StatusForbidden)
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "Tier 3")
	})

	t.Run("Tier4 lock veto — CanNotDelete lock", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupLocks: func(_ context.Context, _ string) ([]*ManagementLock, error) {
				return []*ManagementLock{{Name: "no-delete", LockType: cLockCanNotDelete}}, nil
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "management lock")
	})

	t.Run("Tier4 lock check 403 — no veto, still owned", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupLocks: func(_ context.Context, _ string) ([]*ManagementLock, error) {
				return nil, makeResponseError(http.StatusForbidden)
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
	})

	t.Run("Tier4 extra resources hard veto (CI/non-interactive)", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
			ListResourceGroupResources: func(_ context.Context, _ string) ([]*ResourceWithTags, error) {
				return []*ResourceWithTags{
					{Name: "foreign-vm", Tags: map[string]*string{
						cAzdEnvNameTag: strPtr("other-env"),
					}},
				}, nil
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "foreign resource")
	})

	t.Run("Tier4 extra resources soft veto (interactive, user says no)", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			ListResourceGroupResources: func(_ context.Context, _ string) ([]*ResourceWithTags, error) {
				return []*ResourceWithTags{
					{Name: "shared-sa", Tags: nil},
				}, nil
			},
			Prompter: func(_, _ string) (bool, error) { return false, nil },
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "foreign resource")
	})

	t.Run("Tier4 no extra resources — owned", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupResources: func(_ context.Context, _ string) ([]*ResourceWithTags, error) {
				return []*ResourceWithTags{
					{Name: "my-vm", Tags: map[string]*string{
						cAzdEnvNameTag: strPtr(envName),
					}},
				}, nil
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
		assert.Empty(t, res.Skipped)
	})

	t.Run("Tier3 interactive accept — user says yes", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return nil, nil // no tags → unknown
			},
			Prompter: func(_, _ string) (bool, error) { return true, nil },
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
	})

	t.Run("Tier3 interactive deny — user says no", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return nil, nil
			},
			Prompter: func(_, _ string) (bool, error) { return false, nil },
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "Tier 3")
	})

	t.Run("Tier3 non-interactive — unknown skipped without prompt", func(t *testing.T) {
		t.Parallel()
		prompted := false
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return nil, nil
			},
			Prompter: func(_, _ string) (bool, error) {
				prompted = true
				return true, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.False(t, prompted, "prompter should not be called in non-interactive mode")
	})

	t.Run("multiple RGs — mix of owned, external, unknown", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Create", rgOp, rgA),
			makeOperation("Read", rgOp, rgB),
			// rgC has no operation → unknown
		}
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: false,
			GetResourceGroupTags: func(_ context.Context, rg string) (map[string]*string, error) {
				if rg == rgC {
					return nil, nil // no tags → unknown
				}
				return nil, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA, rgB, rgC}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
		skippedNames := make([]string, len(res.Skipped))
		for i, s := range res.Skipped {
			skippedNames[i] = s.Name
		}
		assert.Contains(t, skippedNames, rgB)
		assert.Contains(t, skippedNames, rgC)
	})

	t.Run("empty operations list — all RGs fall to Tier2", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return map[string]*string{
					cAzdEnvNameTag:       strPtr(envName),
					cAzdProvisionHashTag: strPtr("hash1"),
				}, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), []*armresources.DeploymentOperation{}, []string{rgA, rgB}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
		assert.Contains(t, res.Owned, rgB)
	})

	t.Run("already deleted — 404 on tag fetch gracefully skipped", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return nil, makeResponseError(http.StatusNotFound)
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "already deleted")
		assert.Equal(t, rgA, res.Skipped[0].Name)
	})

	t.Run("Tier4 ReadOnly lock — veto", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupLocks: func(_ context.Context, _ string) ([]*ManagementLock, error) {
				return []*ManagementLock{{Name: "ro-lock", LockType: cLockReadOnly}}, nil
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "management lock")
	})

	t.Run("Tier4 extra resources soft veto (interactive, user accepts)", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			ListResourceGroupResources: func(_ context.Context, _ string) ([]*ResourceWithTags, error) {
				return []*ResourceWithTags{
					{Name: "shared", Tags: nil},
				}, nil
			},
			Prompter: func(_, _ string) (bool, error) { return true, nil },
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
	})

	t.Run("operationTargetsRG nil checks", func(t *testing.T) {
		t.Parallel()
		_, ok := operationTargetsRG(nil, "Create")
		assert.False(t, ok)

		_, ok = operationTargetsRG(&armresources.DeploymentOperation{Properties: nil}, "Create")
		assert.False(t, ok)

		_, ok = operationTargetsRG(&armresources.DeploymentOperation{
			Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: nil,
			},
		}, "Create")
		assert.False(t, ok)

		_, ok = operationTargetsRG(&armresources.DeploymentOperation{
			Properties: &armresources.DeploymentOperationProperties{
				ProvisioningOperation: func() *armresources.ProvisioningOperation {
					p := armresources.ProvisioningOperation("Create")
					return &p
				}(),
				TargetResource: &armresources.TargetResource{
					ResourceType: nil,
					ResourceName: nil,
				},
			},
		}, "Create")
		assert.False(t, ok)
	})

	t.Run("Tier4 lock 404 — no veto", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupLocks: func(_ context.Context, _ string) ([]*ManagementLock, error) {
				return nil, makeResponseError(http.StatusNotFound)
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
	})

	t.Run("Tier2 tag fetch error (non-403/404) propagated", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return nil, fmt.Errorf("unexpected internal error")
			},
		}
		_, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "classify rg=")
	})

	t.Run("Tier3 accepted RG goes through Tier4 veto (lock)", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return nil, nil // no tags → unknown → Tier 3
			},
			Prompter: func(_, _ string) (bool, error) { return true, nil }, // user accepts
			ListResourceGroupLocks: func(_ context.Context, _ string) ([]*ManagementLock, error) {
				return []*ManagementLock{{Name: "no-delete", LockType: cLockCanNotDelete}}, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		// Even though user accepted at Tier 3, Tier 4 lock veto should prevent deletion.
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "management lock")
	})

	t.Run("Tier4 foreign resources sequential prompt (not concurrent)", func(t *testing.T) {
		t.Parallel()
		rgOp := "Microsoft.Resources/resourceGroups"
		promptCount := 0
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			ListResourceGroupResources: func(_ context.Context, _ string) ([]*ResourceWithTags, error) {
				return []*ResourceWithTags{
					{Name: "foreign", Tags: nil},
				}, nil
			},
			Prompter: func(_, _ string) (bool, error) {
				promptCount++
				return false, nil // deny all
			},
		}
		ops := []*armresources.DeploymentOperation{
			makeOperation("Create", rgOp, rgA),
			makeOperation("Create", rgOp, rgB),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA, rgB}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		assert.Equal(t, 2, promptCount, "both RGs should be prompted sequentially")
	})

	t.Run("Tier4 500 error treated as veto (fail-safe)", func(t *testing.T) {
		t.Parallel()
		rgOp := "Microsoft.Resources/resourceGroups"
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupLocks: func(_ context.Context, _ string) ([]*ManagementLock, error) {
				return nil, &azcore.ResponseError{StatusCode: http.StatusInternalServerError}
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err, "500 error should not propagate — treated as veto")
		assert.Empty(t, res.Owned, "RG should be vetoed on 500 error")
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "error during safety check")
	})

	t.Run("Tier4 429 throttling error treated as veto (fail-safe)", func(t *testing.T) {
		t.Parallel()
		rgOp := "Microsoft.Resources/resourceGroups"
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupResources: func(_ context.Context, _ string) ([]*ResourceWithTags, error) {
				return nil, &azcore.ResponseError{StatusCode: http.StatusTooManyRequests}
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err, "429 error should not propagate — treated as veto")
		assert.Empty(t, res.Owned, "RG should be vetoed on 429 throttle")
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "error during safety check")
	})

	t.Run("Tier1 external reason includes operation name — Read", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Read", rgOp, rgA),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
		require.NoError(t, err)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "Read operation found")
	})

	t.Run("Tier1 external reason includes operation name — EvaluateDeploymentOutput", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("EvaluateDeploymentOutput", rgOp, rgA),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
		require.NoError(t, err)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "EvaluateDeploymentOutput operation found")
	})

	t.Run("Tier2 hash match — owned when ExpectedProvisionParamHash matches", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:                    envName,
			ExpectedProvisionParamHash: "abc123",
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return map[string]*string{
					cAzdEnvNameTag:       strPtr(envName),
					cAzdProvisionHashTag: strPtr("abc123"),
				}, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Contains(t, res.Owned, rgA)
	})

	t.Run("Tier2 hash mismatch — falls to Tier3 non-interactive skip", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName:                    envName,
			Interactive:                false,
			ExpectedProvisionParamHash: "expected-hash",
			GetResourceGroupTags: func(_ context.Context, _ string) (map[string]*string, error) {
				return map[string]*string{
					cAzdEnvNameTag:       strPtr(envName),
					cAzdProvisionHashTag: strPtr("different-hash"),
				}, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), nil, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "Tier 3",
			"hash mismatch should fall through to Tier 3")
	})

	t.Run("Tier4 resource listing 403 — veto (cannot enumerate)", func(t *testing.T) {
		t.Parallel()
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupResources: func(_ context.Context, _ string) ([]*ResourceWithTags, error) {
				return nil, makeResponseError(http.StatusForbidden)
			},
		}
		ops := []*armresources.DeploymentOperation{makeOperation("Create", rgOp, rgA)}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned, "RG should be vetoed when resource listing returns 403")
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "authorization failure")
	})

	t.Run("Context cancellation returns error", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel() // cancel immediately

		opts := ClassifyOptions{
			EnvName: envName,
			GetResourceGroupTags: func(ctx context.Context, _ string) (map[string]*string, error) {
				return nil, ctx.Err()
			},
		}
		// RG with no deployment ops → goes to Tier 2 → calls GetResourceGroupTags → gets ctx.Err()
		ops := []*armresources.DeploymentOperation{}
		_, err := ClassifyResourceGroups(ctx, ops, []string{rgA}, opts)
		require.Error(t, err, "context cancellation should propagate as an error")
	})

	t.Run("Tier1 Create overrides preceding Read for same RG", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Read", rgOp, rgA),
			makeOperation("Create", rgOp, rgA),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	t.Run("Tier1 Create overrides following Read for same RG", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Create", rgOp, rgA),
			makeOperation("Read", rgOp, rgA),
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, noopOpts(envName))
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	t.Run("Tier1 RG name match is case-insensitive — Create", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Create", rgOp, "RG-ALPHA"),
		}
		res, err := ClassifyResourceGroups(
			t.Context(), ops, []string{"rg-alpha"}, noopOpts(envName),
		)
		require.NoError(t, err)
		assert.Equal(t, []string{"rg-alpha"}, res.Owned)
		assert.Empty(t, res.Skipped)
	})

	t.Run("Tier1 RG name match is case-insensitive — Read", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Read", rgOp, "RG-Alpha"),
		}
		res, err := ClassifyResourceGroups(
			t.Context(), ops, []string{"rg-alpha"}, noopOpts(envName),
		)
		require.NoError(t, err)
		assert.Empty(t, res.Owned)
		require.Len(t, res.Skipped, 1)
		assert.Equal(t, "rg-alpha", res.Skipped[0].Name)
		assert.Contains(t, res.Skipped[0].Reason, "Read")
	})

	t.Run("Tier4 empty EnvName vetoes deletion", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Create", rgOp, rgA),
		}
		opts := ClassifyOptions{
			EnvName: "", // empty env name
			ListResourceGroupResources: func(
				_ context.Context, _ string,
			) ([]*ResourceWithTags, error) {
				t.Fatal("should not be called when EnvName is empty")
				return nil, nil
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Empty(t, res.Owned, "empty EnvName should veto all owned RGs")
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "without environment name")
	})

	t.Run("Tier3 prompter error propagated", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{} // no ops → Tier 2
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			Prompter: func(_, _ string) (bool, error) {
				return false, fmt.Errorf("prompt failure")
			},
		}
		_, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tier=3 prompt")
		assert.Contains(t, err.Error(), "prompt failure")
	})

	t.Run("Tier4 prompter error propagated", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Create", rgOp, rgA),
		}
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			ListResourceGroupResources: func(
				_ context.Context, _ string,
			) ([]*ResourceWithTags, error) {
				return []*ResourceWithTags{
					{Name: "foreign-res", Tags: nil},
				}, nil
			},
			Prompter: func(_, _ string) (bool, error) {
				return false, fmt.Errorf("tier4 prompt failure")
			},
		}
		_, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "tier=4 prompt")
		assert.Contains(t, err.Error(), "tier4 prompt failure")
	})

	t.Run("Tier4 resource listing 404 — no veto", func(t *testing.T) {
		t.Parallel()
		ops := []*armresources.DeploymentOperation{
			makeOperation("Create", rgOp, rgA),
		}
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupResources: func(
				_ context.Context, _ string,
			) ([]*ResourceWithTags, error) {
				return nil, makeResponseError(404)
			},
		}
		res, err := ClassifyResourceGroups(t.Context(), ops, []string{rgA}, opts)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned, "404 in Tier 4 should not veto")
		assert.Empty(t, res.Skipped)
	})

	t.Run("Tier4 semaphore respects context cancellation", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())

		// Create more RGs than semaphore capacity to exercise the select.
		manyRGs := make([]string, cTier4Parallelism+3)
		ops := make([]*armresources.DeploymentOperation, len(manyRGs))
		for i := range manyRGs {
			manyRGs[i] = fmt.Sprintf("rg-%d", i)
			ops[i] = makeOperation("Create", rgOp, manyRGs[i])
		}

		callCount := 0
		opts := ClassifyOptions{
			EnvName: envName,
			ListResourceGroupLocks: func(
				_ context.Context, _ string,
			) ([]*ManagementLock, error) {
				callCount++
				if callCount >= 2 {
					cancel() // cancel after 2 lock checks
				}
				return nil, nil
			},
		}
		res, err := ClassifyResourceGroups(ctx, ops, manyRGs, opts)
		require.NoError(t, err)
		// Some RGs should be vetoed due to context cancellation.
		assert.NotEmpty(t, res.Skipped, "cancelled context should veto remaining RGs")
	})

	t.Run("Tier2 nil TagReader falls through to Tier3", func(t *testing.T) {
		t.Parallel()
		// No operations → Tier 1 classifies RG as "unknown", Tier 2 has nil
		// GetResourceGroupTags → falls through, Tier 3 interactive prompt decides.
		opts := ClassifyOptions{
			EnvName:              envName,
			Interactive:          true,
			GetResourceGroupTags: nil,
			Prompter: func(rgName, _ string) (bool, error) {
				return true, nil
			},
		}
		res, err := ClassifyResourceGroups(
			t.Context(), nil, []string{rgA}, opts,
		)
		require.NoError(t, err)
		assert.Equal(t, []string{rgA}, res.Owned)
	})

	t.Run("Tier3 nil Prompter skips unknown RGs", func(t *testing.T) {
		t.Parallel()
		// Unknown RG, interactive mode, but nil prompter → skip (no crash).
		opts := ClassifyOptions{
			EnvName:     envName,
			Interactive: true,
			Prompter:    nil,
		}
		res, err := ClassifyResourceGroups(
			t.Context(), nil, []string{rgA}, opts,
		)
		require.NoError(t, err)
		assert.Empty(t, res.Owned, "nil prompter should not classify as owned")
		require.Len(t, res.Skipped, 1)
		assert.Contains(t, res.Skipped[0].Reason, "unknown")
	})
}
