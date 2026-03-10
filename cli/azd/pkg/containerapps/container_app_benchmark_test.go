// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerapps

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appcontainers/armappcontainers/v3"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazsdk"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"
)

// mockContainerAppGetCounted sets up a mock GET with an atomic call counter.
func mockContainerAppGetCounted(
	mockContext *mocks.MockContext,
	subscriptionId, resourceGroup, appName string,
	containerApp *armappcontainers.ContainerApp,
	counter *atomic.Int32,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
			subscriptionId, resourceGroup, appName)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		counter.Add(1)
		response := armappcontainers.ContainerAppsClientGetResponse{
			ContainerApp: *containerApp,
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

// mockContainerAppSecretsListCounted sets up a mock POST listSecrets with a counter.
func mockContainerAppSecretsListCounted(
	mockContext *mocks.MockContext,
	subscriptionId, resourceGroup, appName string,
	secrets *armappcontainers.SecretsCollection,
	counter *atomic.Int32,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && request.URL.Path == fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s/listSecrets",
			subscriptionId, resourceGroup, appName)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		counter.Add(1)
		response := armappcontainers.ContainerAppsClientListSecretsResponse{
			SecretsCollection: *secrets,
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

// mockContainerAppUpdateCounted sets up a mock PATCH with a counter and captures the request.
func mockContainerAppUpdateCounted(
	mockContext *mocks.MockContext,
	subscriptionId, resourceGroup, appName string,
	counter *atomic.Int32,
) *http.Request {
	captured := &http.Request{}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPatch && request.URL.Path == fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s",
			subscriptionId, resourceGroup, appName)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		counter.Add(1)
		*captured = *request
		response := armappcontainers.ContainerAppsClientUpdateResponse{}
		return mocks.CreateHttpResponseWithBody(request, http.StatusAccepted, response)
	})
	return captured
}

// mockContainerAppRevisionGetCounted sets up a mock GET revision with a counter.
func mockContainerAppRevisionGetCounted(
	mockContext *mocks.MockContext,
	subscriptionId, resourceGroup, appName, revisionName string,
	revision *armappcontainers.Revision,
	counter *atomic.Int32,
) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.Path == fmt.Sprintf(
			"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.App/containerApps/%s/revisions/%s",
			subscriptionId, resourceGroup, appName, revisionName)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		counter.Add(1)
		response := armappcontainers.ContainerAppsRevisionsClientGetRevisionResponse{
			Revision: *revision,
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

// Test_AddRevision_ARMCallCount verifies that AddRevision makes the minimum number of ARM API calls.
//
// OPTIMIZED call pattern (this branch):
//   - 1 GET  (container app)
//   - 1 POST (list secrets -- only when secrets exist)
//   - 1 PATCH (update -- includes traffic weights for multi-revision)
//   - 1 GET  (LRO poll completion -- inherent to ARM SDK)
//
// BEFORE optimization (main branch) the pattern was:
//   - 1 GET  (container app)
//   - 1 GET  (revision)              -- ELIMINATED
//   - 1 POST (list secrets)
//   - 1 PATCH (update template) + 1 GET (LRO poll)
//   - 1 PATCH (update traffic) + 1 GET (LRO poll)  -- MERGED into single PATCH for multi-rev
//
// Savings summary (including LRO polls):
//
//	Single-rev no secrets:   4 -> 3 calls (saved: revision GET)
//	Single-rev with secrets: 5 -> 4 calls (saved: revision GET)
//	Multi-rev with secrets:  7 -> 4 calls (saved: revision GET + traffic PATCH + LRO poll)
func Test_AddRevision_ARMCallCount(t *testing.T) {
	tests := []struct {
		name          string
		revisionMode  armappcontainers.ActiveRevisionsMode
		hasSecrets    bool
		expectedCalls int
		beforeCalls   int // call count on main branch for comparison
		description   string
	}{
		{
			name:          "SingleRevision_NoSecrets",
			revisionMode:  armappcontainers.ActiveRevisionsModeSingle,
			hasSecrets:    false,
			expectedCalls: 3, // GET app + PATCH + GET(LRO poll)
			beforeCalls:   4, // + GET revision
			description:   "GET + PATCH + GET(poll)",
		},
		{
			name:          "SingleRevision_WithSecrets",
			revisionMode:  armappcontainers.ActiveRevisionsModeSingle,
			hasSecrets:    true,
			expectedCalls: 4, // GET app + POST secrets + PATCH + GET(LRO poll)
			beforeCalls:   5, // + GET revision
			description:   "GET + POST secrets + PATCH + GET(poll)",
		},
		{
			name:          "MultipleRevision_WithSecrets",
			revisionMode:  armappcontainers.ActiveRevisionsModeMultiple,
			hasSecrets:    true,
			expectedCalls: 4, // GET app + POST secrets + PATCH(combined) + GET(LRO poll)
			beforeCalls:   7, // + GET revision + PATCH(traffic) + GET(poll)
			description:   "GET + POST secrets + PATCH(combined) + GET(poll)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var getCalls, secretsCalls, updateCalls, revisionGetCalls atomic.Int32

			subscriptionId := "SUB"
			resourceGroup := "RG"
			appName := "APP"

			var secrets []*armappcontainers.Secret
			if tt.hasSecrets {
				secrets = []*armappcontainers.Secret{
					{Name: to.Ptr("secret"), Value: nil},
				}
			}

			containerApp := &armappcontainers.ContainerApp{
				Location: to.Ptr("eastus2"),
				Name:     &appName,
				Properties: &armappcontainers.ContainerAppProperties{
					LatestRevisionName: to.Ptr("rev-1"),
					Configuration: &armappcontainers.Configuration{
						ActiveRevisionsMode: to.Ptr(tt.revisionMode),
						Secrets:             secrets,
						Ingress: &armappcontainers.Ingress{
							Fqdn: to.Ptr("app.azurecontainerapps.io"),
						},
					},
					Template: &armappcontainers.Template{
						Containers: []*armappcontainers.Container{
							{Image: to.Ptr("old-image")},
						},
					},
				},
			}

			mockContext := mocks.NewMockContext(context.Background())

			// Register counted mocks
			mockContainerAppGetCounted(mockContext, subscriptionId, resourceGroup, appName,
				containerApp, &getCalls)

			// Register revision GET mock (should NOT be called in optimized code)
			mockContainerAppRevisionGetCounted(mockContext, subscriptionId, resourceGroup,
				appName, "rev-1",
				&armappcontainers.Revision{
					Properties: &armappcontainers.RevisionProperties{
						Template: containerApp.Properties.Template,
					},
				}, &revisionGetCalls)

			if tt.hasSecrets {
				mockContainerAppSecretsListCounted(mockContext, subscriptionId, resourceGroup,
					appName,
					&armappcontainers.SecretsCollection{
						Value: []*armappcontainers.ContainerAppSecret{
							{Name: to.Ptr("secret"), Value: to.Ptr("value")},
						},
					}, &secretsCalls)
			}

			mockContainerAppUpdateCounted(mockContext, subscriptionId, resourceGroup,
				appName, &updateCalls)

			cas := NewContainerAppService(
				mockContext.SubscriptionCredentialProvider,
				clock.NewMock(),
				mockContext.ArmClientOptions,
				mockContext.AlphaFeaturesManager,
			)

			err := cas.AddRevision(
				*mockContext.Context, subscriptionId, resourceGroup, appName,
				"new-image", nil, nil)
			require.NoError(t, err)

			totalCalls := int(getCalls.Load() + secretsCalls.Load() +
				updateCalls.Load() + revisionGetCalls.Load())

			t.Logf("ARM call breakdown:")
			t.Logf("  GET container app:   %d", getCalls.Load())
			t.Logf("  GET revision:        %d (eliminated)", revisionGetCalls.Load())
			t.Logf("  POST list secrets:   %d", secretsCalls.Load())
			t.Logf("  PATCH update:        %d", updateCalls.Load())
			t.Logf("  TOTAL:               %d (was %d on main, saved %d calls)",
				totalCalls, tt.beforeCalls, tt.beforeCalls-totalCalls)

			require.Zero(t, revisionGetCalls.Load(),
				"Revision GET should be eliminated -- we use the container app template directly")
			require.Equal(t, int32(1), updateCalls.Load(),
				"Should have exactly 1 PATCH call (even for multi-revision)")
			require.Equal(t, tt.expectedCalls, totalCalls, tt.description)
		})
	}
}

// Test_AddRevision_MultiRevision_CombinedPatch verifies that for multi-revision mode,
// both template update AND traffic weight update happen in a single PATCH call.
func Test_AddRevision_MultiRevision_CombinedPatch(t *testing.T) {
	var updateCalls atomic.Int32

	subscriptionId := "SUB"
	resourceGroup := "RG"
	appName := "APP"

	containerApp := &armappcontainers.ContainerApp{
		Location: to.Ptr("eastus2"),
		Name:     &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			LatestRevisionName: to.Ptr("rev-1"),
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeMultiple),
				Ingress: &armappcontainers.Ingress{
					Fqdn: to.Ptr("app.azurecontainerapps.io"),
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{Image: to.Ptr("old-image")},
				},
			},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, containerApp)
	updateReq := mockContainerAppUpdateCounted(mockContext, subscriptionId, resourceGroup,
		appName, &updateCalls)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	err := cas.AddRevision(
		*mockContext.Context, subscriptionId, resourceGroup, appName,
		"new-image", nil, nil)
	require.NoError(t, err)

	// Exactly 1 PATCH call
	require.Equal(t, int32(1), updateCalls.Load(),
		"Multi-revision should use exactly 1 PATCH (template + traffic combined)")

	// Verify the PATCH body contains both template AND traffic
	var updatedApp armappcontainers.ContainerApp
	err = json.NewDecoder(updateReq.Body).Decode(&updatedApp)
	require.NoError(t, err)

	// Template updated
	require.Equal(t, "new-image", *updatedApp.Properties.Template.Containers[0].Image)
	require.Contains(t, *updatedApp.Properties.Template.RevisionSuffix, "azd-")

	// Traffic weights included in same request
	require.NotNil(t, updatedApp.Properties.Configuration.Ingress.Traffic,
		"Traffic weights must be in the same PATCH for multi-revision")
	require.Len(t, updatedApp.Properties.Configuration.Ingress.Traffic, 1)
	require.Equal(t, int32(100), *updatedApp.Properties.Configuration.Ingress.Traffic[0].Weight)

	expectedRevName := fmt.Sprintf("%s--azd-0", appName)
	require.Equal(t, expectedRevName, *updatedApp.Properties.Configuration.Ingress.Traffic[0].RevisionName)

	t.Log("PASS: Multi-revision deploy verified -- template + traffic in single PATCH")
}

// Benchmark_AddRevision measures AddRevision overhead with mocked ARM calls.
// Run with: go test ./pkg/containerapps/... -bench=Benchmark_AddRevision -benchmem
func Benchmark_AddRevision(b *testing.B) {
	subscriptionId := "SUB"
	resourceGroup := "RG"
	appName := "APP"

	containerApp := &armappcontainers.ContainerApp{
		Location: to.Ptr("eastus2"),
		Name:     &appName,
		Properties: &armappcontainers.ContainerAppProperties{
			LatestRevisionName: to.Ptr("rev-1"),
			Configuration: &armappcontainers.Configuration{
				ActiveRevisionsMode: to.Ptr(armappcontainers.ActiveRevisionsModeSingle),
				Secrets: []*armappcontainers.Secret{
					{Name: to.Ptr("secret"), Value: nil},
				},
			},
			Template: &armappcontainers.Template{
				Containers: []*armappcontainers.Container{
					{Image: to.Ptr("old-image")},
				},
			},
		},
	}

	secrets := &armappcontainers.SecretsCollection{
		Value: []*armappcontainers.ContainerAppSecret{
			{Name: to.Ptr("secret"), Value: to.Ptr("value")},
		},
	}

	mockContext := mocks.NewMockContext(context.Background())
	mockazsdk.MockContainerAppGet(mockContext, subscriptionId, resourceGroup, appName, containerApp)
	mockazsdk.MockContainerAppSecretsList(mockContext, subscriptionId, resourceGroup, appName, secrets)
	mockazsdk.MockContainerAppUpdate(mockContext, subscriptionId, resourceGroup, appName, containerApp)

	cas := NewContainerAppService(
		mockContext.SubscriptionCredentialProvider,
		clock.NewMock(),
		mockContext.ArmClientOptions,
		mockContext.AlphaFeaturesManager,
	)

	b.ResetTimer()
	for range b.N {
		err := cas.AddRevision(
			*mockContext.Context, subscriptionId, resourceGroup, appName,
			"new-image", nil, nil)
		if err != nil {
			b.Fatal(err)
		}
	}
}
