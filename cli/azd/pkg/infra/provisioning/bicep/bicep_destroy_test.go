// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snapshotJSON builds a JSON byte string for a snapshotResult containing
// the given resource group names.
func snapshotJSON(rgNames ...string) []byte {
	type resource struct {
		Type string `json:"type"`
		Name string `json:"name"`
	}
	type snapshot struct {
		PredictedResources []resource `json:"predictedResources"`
	}
	s := snapshot{}
	for _, rg := range rgNames {
		s.PredictedResources = append(
			s.PredictedResources,
			resource{
				Type: "Microsoft.Resources/resourceGroups",
				Name: rg,
			},
		)
	}
	b, _ := json.Marshal(s)
	return b
}

// mockSnapshotCommand registers a mock command runner response for
// "bicep snapshot" that writes the provided data to a .snapshot.json
// file, simulating the real bicep CLI behavior.
func mockSnapshotCommand(
	mockContext *mocks.MockContext,
	snapshotData []byte,
) {
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") &&
			len(args.Args) > 0 && args.Args[0] == "snapshot"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		// The bicep CLI writes <file>.snapshot.json next to the input.
		inputFile := args.Args[1]
		snapshotFile := strings.TrimSuffix(
			inputFile, filepath.Ext(inputFile),
		) + ".snapshot.json"
		if writeErr := os.WriteFile(
			snapshotFile, snapshotData, 0600,
		); writeErr != nil {
			return exec.RunResult{ExitCode: 1}, writeErr
		}
		return exec.NewRunResult(0, "", ""), nil
	})
}

// mockBicepVersion registers a mock for "bicep --version".
func mockBicepVersion(mockContext *mocks.MockContext) {
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(args.Cmd, "bicep") &&
			len(args.Args) > 0 && args.Args[0] == "--version"
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(
			0,
			fmt.Sprintf(
				"Bicep CLI version %s (abcdef0123)",
				bicep.Version,
			),
			"",
		), nil
	})
}

// newTestBicepProvider builds a minimal *BicepProvider suitable for
// testing getSnapshotPredictedRGs. Only the fields accessed by that
// method are populated.
func newTestBicepProvider(
	mockContext *mocks.MockContext,
	mode bicepFileMode,
	path string,
	compileCache *compileBicepResult,
	envValues map[string]string,
) *BicepProvider {
	cli := bicep.NewCli(
		mockContext.Console, mockContext.CommandRunner,
	)
	env := environment.NewWithValues("test-env", envValues)
	return &BicepProvider{
		bicepCli:                cli,
		env:                     env,
		mode:                    mode,
		path:                    path,
		compileBicepMemoryCache: compileCache,
	}
}

func TestGetSnapshotPredictedRGs(t *testing.T) {
	t.Parallel()

	envValues := map[string]string{
		environment.SubscriptionIdEnvVarName: "sub-123",
		environment.LocationEnvVarName:       "westus2",
	}

	t.Run("nil compileBicep cache returns nil", func(t *testing.T) {
		t.Parallel()
		mockCtx := mocks.NewMockContext(t.Context())
		p := newTestBicepProvider(
			mockCtx, bicepparamMode, "main.bicepparam",
			nil, envValues,
		)
		result := p.getSnapshotPredictedRGs(t.Context())
		assert.Nil(t, result)
	})

	t.Run("bicepparam mode returns predicted RGs", func(t *testing.T) {
		t.Parallel()
		mockCtx := mocks.NewMockContext(t.Context())

		// Create a temp .bicepparam file (Snapshot reads its path).
		dir := t.TempDir()
		paramFile := filepath.Join(dir, "main.bicepparam")
		require.NoError(t, os.WriteFile(
			paramFile, []byte("using 'main.bicep'"), 0600,
		))

		mockBicepVersion(mockCtx)
		mockSnapshotCommand(
			mockCtx,
			snapshotJSON("rg-app", "rg-data"),
		)

		p := newTestBicepProvider(
			mockCtx, bicepparamMode, paramFile,
			&compileBicepResult{},
			envValues,
		)
		result := p.getSnapshotPredictedRGs(t.Context())

		require.NotNil(t, result)
		assert.True(t, result["rg-app"])
		assert.True(t, result["rg-data"])
		assert.Len(t, result, 2)
	})

	t.Run("non-bicepparam with params generates temp file",
		func(t *testing.T) {
			t.Parallel()
			mockCtx := mocks.NewMockContext(t.Context())

			// The .bicep file needs to exist in a writable directory
			// because getSnapshotPredictedRGs creates a temp file
			// next to it.
			dir := t.TempDir()
			bicepFile := filepath.Join(dir, "main.bicep")
			require.NoError(t, os.WriteFile(
				bicepFile, []byte("// bicep"), 0600,
			))

			mockBicepVersion(mockCtx)
			mockSnapshotCommand(
				mockCtx,
				snapshotJSON("rg-infra"),
			)

			cache := &compileBicepResult{
				Parameters: azure.ArmParameters{
					"location": {Value: "westus2"},
				},
			}
			p := newTestBicepProvider(
				mockCtx, bicepMode, bicepFile,
				cache,
				envValues,
			)
			result := p.getSnapshotPredictedRGs(t.Context())

			require.NotNil(t, result)
			assert.True(t, result["rg-infra"])
			assert.Len(t, result, 1)
		})

	t.Run("non-bicepparam without params returns nil",
		func(t *testing.T) {
			t.Parallel()
			mockCtx := mocks.NewMockContext(t.Context())

			p := newTestBicepProvider(
				mockCtx, bicepMode, "main.bicep",
				&compileBicepResult{Parameters: nil},
				envValues,
			)
			result := p.getSnapshotPredictedRGs(t.Context())
			assert.Nil(t, result)
		})

	t.Run("snapshot CLI error returns nil", func(t *testing.T) {
		t.Parallel()
		mockCtx := mocks.NewMockContext(t.Context())

		dir := t.TempDir()
		paramFile := filepath.Join(dir, "main.bicepparam")
		require.NoError(t, os.WriteFile(
			paramFile, []byte("using 'main.bicep'"), 0600,
		))

		mockBicepVersion(mockCtx)
		// Mock snapshot to return an error.
		mockCtx.CommandRunner.When(func(
			args exec.RunArgs, command string,
		) bool {
			return strings.Contains(args.Cmd, "bicep") &&
				len(args.Args) > 0 &&
				args.Args[0] == "snapshot"
		}).RespondFn(func(
			args exec.RunArgs,
		) (exec.RunResult, error) {
			return exec.RunResult{ExitCode: 1},
				errors.New("bicep snapshot not supported")
		})

		p := newTestBicepProvider(
			mockCtx, bicepparamMode, paramFile,
			&compileBicepResult{},
			envValues,
		)
		result := p.getSnapshotPredictedRGs(t.Context())
		assert.Nil(t, result)
	})

	t.Run("JSON parse error returns nil", func(t *testing.T) {
		t.Parallel()
		mockCtx := mocks.NewMockContext(t.Context())

		dir := t.TempDir()
		paramFile := filepath.Join(dir, "main.bicepparam")
		require.NoError(t, os.WriteFile(
			paramFile, []byte("using 'main.bicep'"), 0600,
		))

		mockBicepVersion(mockCtx)
		// Return invalid JSON from the snapshot command.
		mockSnapshotCommand(mockCtx, []byte("not-json{{{"))

		p := newTestBicepProvider(
			mockCtx, bicepparamMode, paramFile,
			&compileBicepResult{},
			envValues,
		)
		result := p.getSnapshotPredictedRGs(t.Context())
		assert.Nil(t, result)
	})

	t.Run("zero RGs in predicted resources returns nil",
		func(t *testing.T) {
			t.Parallel()
			mockCtx := mocks.NewMockContext(t.Context())

			dir := t.TempDir()
			paramFile := filepath.Join(dir, "main.bicepparam")
			require.NoError(t, os.WriteFile(
				paramFile, []byte("using 'main.bicep'"), 0600,
			))

			mockBicepVersion(mockCtx)
			// Return a valid snapshot with only non-RG resources.
			noRGSnapshot, _ := json.Marshal(map[string]any{
				"predictedResources": []map[string]string{
					{
						"type": "Microsoft.Storage/storageAccounts",
						"name": "mystorageacct",
					},
				},
			})
			mockSnapshotCommand(mockCtx, noRGSnapshot)

			p := newTestBicepProvider(
				mockCtx, bicepparamMode, paramFile,
				&compileBicepResult{},
				envValues,
			)
			result := p.getSnapshotPredictedRGs(t.Context())
			assert.Nil(t, result)
		})

	t.Run("RG names are lowercased in result", func(t *testing.T) {
		t.Parallel()
		mockCtx := mocks.NewMockContext(t.Context())

		dir := t.TempDir()
		paramFile := filepath.Join(dir, "main.bicepparam")
		require.NoError(t, os.WriteFile(
			paramFile, []byte("using 'main.bicep'"), 0600,
		))

		mockBicepVersion(mockCtx)
		mockSnapshotCommand(
			mockCtx,
			snapshotJSON("RG-MyApp", "RG-DATA"),
		)

		p := newTestBicepProvider(
			mockCtx, bicepparamMode, paramFile,
			&compileBicepResult{},
			envValues,
		)
		result := p.getSnapshotPredictedRGs(t.Context())

		require.NotNil(t, result)
		assert.True(t, result["rg-myapp"])
		assert.True(t, result["rg-data"])
		assert.False(t, result["RG-MyApp"],
			"keys should be lowercased")
	})

	t.Run("env resource group passed to snapshot options",
		func(t *testing.T) {
			t.Parallel()
			mockCtx := mocks.NewMockContext(t.Context())

			dir := t.TempDir()
			paramFile := filepath.Join(dir, "main.bicepparam")
			require.NoError(t, os.WriteFile(
				paramFile, []byte("using 'main.bicep'"), 0600,
			))

			mockBicepVersion(mockCtx)

			// Capture snapshot args to verify options.
			var capturedArgs []string
			mockCtx.CommandRunner.When(func(
				args exec.RunArgs, command string,
			) bool {
				return strings.Contains(args.Cmd, "bicep") &&
					len(args.Args) > 0 &&
					args.Args[0] == "snapshot"
			}).RespondFn(func(
				args exec.RunArgs,
			) (exec.RunResult, error) {
				capturedArgs = args.Args
				inputFile := args.Args[1]
				sf := strings.TrimSuffix(
					inputFile, filepath.Ext(inputFile),
				) + ".snapshot.json"
				data := snapshotJSON("rg-test")
				_ = os.WriteFile(sf, data, 0600)
				return exec.NewRunResult(0, "", ""), nil
			})

			vals := map[string]string{
				environment.SubscriptionIdEnvVarName: "sub-123",
				environment.LocationEnvVarName:       "westus2",
				environment.ResourceGroupEnvVarName:  "my-rg",
			}
			p := newTestBicepProvider(
				mockCtx, bicepparamMode, paramFile,
				&compileBicepResult{},
				vals,
			)
			result := p.getSnapshotPredictedRGs(t.Context())

			require.NotNil(t, result)
			// Verify --resource-group was passed.
			assert.Contains(t, capturedArgs, "--resource-group")
			assert.Contains(t, capturedArgs, "my-rg")
			// Verify --subscription-id was passed.
			assert.Contains(t, capturedArgs, "--subscription-id")
			assert.Contains(t, capturedArgs, "sub-123")
			// Verify --location was passed.
			assert.Contains(t, capturedArgs, "--location")
			assert.Contains(t, capturedArgs, "westus2")
		})
}

// prepareForceModeDestroyMocks registers all HTTP mocks needed for
// force-mode destroy tests: deployment GET/list, per-RG resources/tags,
// operations (500), RG deletion tracking, locks, LRO polling, and void
// state PUT. Returns a map of per-RG delete counters.
func prepareForceModeDestroyMocks(
	t *testing.T,
	mockContext *mocks.MockContext,
	rgNames []string,
) map[string]*atomic.Int32 {
	t.Helper()

	// Register SubscriptionCredentialProvider + ARM client options
	// so Tier 4 helpers can resolve credentials.
	mockContext.Container.MustRegisterSingleton(
		func() account.SubscriptionCredentialProvider {
			return mockaccount.SubscriptionCredentialProviderFunc(
				func(
					_ context.Context, _ string,
				) (azcore.TokenCredential, error) {
					return mockContext.Credentials, nil
				},
			)
		},
	)
	mockContext.Container.MustRegisterSingleton(
		func() *arm.ClientOptions {
			return mockContext.ArmClientOptions
		},
	)

	// Build a deployment referencing all RGs.
	outputResources := make(
		[]*armresources.ResourceReference, len(rgNames),
	)
	for i, rg := range rgNames {
		outputResources[i] = &armresources.ResourceReference{
			ID: new(fmt.Sprintf(
				"/subscriptions/SUBSCRIPTION_ID/"+
					"resourceGroups/%s", rg,
			)),
		}
	}

	deployment := armresources.DeploymentExtended{
		ID:       new("DEPLOYMENT_ID"),
		Name:     new("test-env"),
		Location: new("eastus2"),
		Tags: map[string]*string{
			"azd-env-name": new("test-env"),
		},
		Type: new("Microsoft.Resources/deployments"),
		Properties: &armresources.DeploymentPropertiesExtended{
			Outputs: map[string]any{
				"WEBSITE_URL": map[string]any{
					"value": "http://myapp.azurewebsites.net",
					"type":  "string",
				},
			},
			OutputResources: outputResources,
			ProvisioningState: new(
				armresources.ProvisioningStateSucceeded,
			),
			Timestamp: new(time.Now()),
		},
	}
	deployBytes, _ := json.Marshal(deployment)

	// GET single deployment
	mockContext.HttpClient.When(func(r *http.Request) bool {
		return r.Method == http.MethodGet && strings.HasSuffix(
			r.URL.Path,
			"/subscriptions/SUBSCRIPTION_ID/providers/"+
				"Microsoft.Resources/deployments/test-env",
		)
	}).RespondFn(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(
				bytes.NewBuffer(deployBytes),
			),
		}, nil
	})

	// GET list deployments
	page := &armresources.DeploymentListResult{
		Value: []*armresources.DeploymentExtended{&deployment},
	}
	pageBytes, _ := json.Marshal(page)
	mockContext.HttpClient.When(func(r *http.Request) bool {
		return r.Method == http.MethodGet && strings.HasSuffix(
			r.URL.Path,
			"/SUBSCRIPTION_ID/providers/"+
				"Microsoft.Resources/deployments/",
		)
	}).RespondFn(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body: io.NopCloser(
				bytes.NewBuffer(pageBytes),
			),
		}, nil
	})

	// Per-RG resource listing (empty resources).
	for _, rgName := range rgNames {
		resList := armresources.ResourceListResult{
			Value: []*armresources.GenericResourceExpanded{},
		}
		mockContext.HttpClient.When(func(r *http.Request) bool {
			return r.Method == http.MethodGet &&
				strings.Contains(
					r.URL.Path,
					fmt.Sprintf(
						"resourceGroups/%s/resources",
						rgName,
					),
				)
		}).RespondFn(
			func(r *http.Request) (*http.Response, error) {
				return mocks.CreateHttpResponseWithBody(
					r, http.StatusOK, resList,
				)
			})
	}

	// Per-RG tags (empty tags).
	for _, rgName := range rgNames {
		rgResp := armresources.ResourceGroup{
			ID: new(fmt.Sprintf(
				"/subscriptions/SUBSCRIPTION_ID/"+
					"resourceGroups/%s", rgName,
			)),
			Name:     new(rgName),
			Location: new("eastus2"),
			Tags:     map[string]*string{},
		}
		mockContext.HttpClient.When(func(r *http.Request) bool {
			return r.Method == http.MethodGet &&
				strings.HasSuffix(
					r.URL.Path,
					fmt.Sprintf(
						"subscriptions/SUBSCRIPTION_ID/"+
							"resourcegroups/%s", rgName,
					),
				)
		}).RespondFn(
			func(r *http.Request) (*http.Response, error) {
				return mocks.CreateHttpResponseWithBody(
					r, http.StatusOK, rgResp,
				)
			})
	}

	// KEY: Deployment operations return 500 (unavailable).
	mockContext.HttpClient.When(func(r *http.Request) bool {
		return r.Method == http.MethodGet &&
			strings.HasSuffix(
				r.URL.Path,
				"/deployments/test-env/operations",
			)
	}).RespondFn(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			Request:    r,
			StatusCode: http.StatusInternalServerError,
			Body: io.NopCloser(bytes.NewBufferString(
				`{"error":{"code":"InternalServerError"}}`,
			)),
		}, nil
	})

	// RG deletion mocks (tracked).
	deleteCounters := map[string]*atomic.Int32{}
	for _, rgName := range rgNames {
		deleteCounters[rgName] = &atomic.Int32{}
		counter := deleteCounters[rgName]
		mockContext.HttpClient.When(func(r *http.Request) bool {
			return r.Method == http.MethodDelete &&
				strings.HasSuffix(
					r.URL.Path,
					fmt.Sprintf(
						"subscriptions/SUBSCRIPTION_ID/"+
							"resourcegroups/%s", rgName,
					),
				)
		}).RespondFn(
			func(r *http.Request) (*http.Response, error) {
				counter.Add(1)
				return httpRespondFn(r)
			})
	}

	// Lock listing (empty).
	for _, rgName := range rgNames {
		mockContext.HttpClient.When(func(r *http.Request) bool {
			return r.Method == http.MethodGet &&
				strings.Contains(
					r.URL.Path,
					fmt.Sprintf(
						"resourceGroups/%s/providers/"+
							"Microsoft.Authorization/locks",
						rgName,
					),
				)
		}).RespondFn(
			func(r *http.Request) (*http.Response, error) {
				return mocks.CreateHttpResponseWithBody(
					r, http.StatusOK,
					azure.ArmTemplate{},
				)
			})
	}

	// LRO polling endpoint.
	mockContext.HttpClient.When(func(r *http.Request) bool {
		return r.Method == http.MethodGet &&
			strings.Contains(
				r.URL.String(), "url-to-poll.net",
			)
	}).RespondFn(func(r *http.Request) (*http.Response, error) {
		return mocks.CreateEmptyHttpResponse(r, 204)
	})

	// Void state PUT.
	mockContext.HttpClient.When(func(r *http.Request) bool {
		return r.Method == http.MethodPut &&
			strings.Contains(
				r.URL.Path,
				"/subscriptions/SUBSCRIPTION_ID/providers/"+
					"Microsoft.Resources/deployments/",
			)
	}).RespondFn(func(r *http.Request) (*http.Response, error) {
		result := &armresources.DeploymentsClientCreateOrUpdateAtSubscriptionScopeResponse{
			DeploymentExtended: armresources.DeploymentExtended{
				ID:       new("DEPLOYMENT_ID"),
				Name:     new("test-env"),
				Location: new("eastus2"),
				Tags: map[string]*string{
					"azd-env-name": new("test-env"),
				},
				Type: new(
					"Microsoft.Resources/deployments",
				),
				Properties: &armresources.DeploymentPropertiesExtended{
					ProvisioningState: new(
						armresources.ProvisioningStateSucceeded,
					),
					Timestamp: new(time.Now()),
				},
			},
		}
		return mocks.CreateHttpResponseWithBody(
			r, http.StatusOK, result,
		)
	})

	return deleteCounters
}

// TestForceWithOperationsFetchFailure verifies that when --force is
// set and deployment.Operations() returns an error, all resource groups
// are treated as owned (backward compatibility). This is the
// integration path in BicepProvider.classifyResourceGroups.
func TestForceWithOperationsFetchFailure(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	prepareBicepMocks(mockContext)

	rgNames := []string{"rg-one", "rg-two"}
	deleteCounters := prepareForceModeDestroyMocks(
		t, mockContext, rgNames,
	)

	infraProvider := createBicepProvider(t, mockContext)
	destroyOptions := provisioning.NewDestroyOptions(true, false)
	result, err := infraProvider.Destroy(
		*mockContext.Context, destroyOptions,
	)

	require.NoError(t, err)
	require.NotNil(t, result)

	// Both RGs deleted — force + operations failure = all owned.
	assert.Equal(t, int32(1), deleteCounters["rg-one"].Load(),
		"rg-one should be deleted (force+ops failure → all owned)")
	assert.Equal(t, int32(1), deleteCounters["rg-two"].Load(),
		"rg-two should be deleted (force+ops failure → all owned)")
}
