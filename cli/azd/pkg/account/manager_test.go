package account

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/subscription/armsubscription"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/stretchr/testify/require"
)

func Test_GetAccountDefaults(t *testing.T) {
	t.Run("FromAzdConfig", func(t *testing.T) {
		expectedConfig := config.Config{
			DefaultSubscription: &config.Subscription{
				Id:   "SUBSCRIPTION_01",
				Name: "Subscription 1",
			},
			DefaultLocation: &config.Location{
				Name:        "westus",
				DisplayName: "West US",
			},
		}

		mockContext := mocks.NewMockContext(context.Background())
		ctx := config.WithConfig(*mockContext.Context, &expectedConfig)

		manager := NewManager(ctx)
		actualConfig, err := manager.GetAccountDefaults(ctx)

		require.NoError(t, err)
		require.Equal(t, expectedConfig, *actualConfig)
	})

	t.Run("FromAzConfig", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		setupAccountMocks(mockContext)

		manager := NewManager(*mockContext.Context)
		actualConfig, err := manager.GetAccountDefaults(*mockContext.Context)

		expectedConfig := config.Config{
			DefaultSubscription: &config.Subscription{
				Id:   "SUBSCRIPTION_02",
				Name: "Subscription 2",
			},
			DefaultLocation: &config.Location{
				Name:        "westus2",
				DisplayName: "West US 2",
			},
		}

		require.NoError(t, err)
		require.Equal(t, expectedConfig, *actualConfig)
	})

	t.Run("FromConfig", func(t *testing.T) {

	})
}

func Test_GetSubscriptions(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupAccountMocks(mockContext)

	manager := NewManager(*mockContext.Context)
	subscriptions, err := manager.GetSubscriptions(*mockContext.Context)

	require.NoError(t, err)
	require.Len(t, subscriptions, 3)
}

func Test_GetLocations(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	setupAccountMocks(mockContext)

	manager := NewManager(*mockContext.Context)
	locations, err := manager.GetLocations(*mockContext.Context)

	require.NoError(t, err)
	require.Len(t, locations, 4)
}

func Test_SetDefaultSubscription(t *testing.T) {

}

func Test_SetDefaultLocation(t *testing.T) {

}

func Test_Clear(t *testing.T) {

}

func setupAccountMocks(mockContext *mocks.MockContext) {
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/subscriptions")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := armsubscription.SubscriptionsClientListResponse{
			ListResult: armsubscription.ListResult{
				Value: []*armsubscription.Subscription{
					{
						ID:             convert.RefOf("SUBSCRIPTION_01"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_01"),
						DisplayName:    convert.RefOf("Subscription 1"),
					},
					{
						ID:             convert.RefOf("SUBSCRIPTION_02"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_02"),
						DisplayName:    convert.RefOf("Subscription 2"),
					},
					{
						ID:             convert.RefOf("SUBSCRIPTION_03"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_03"),
						DisplayName:    convert.RefOf("Subscription 3"),
					},
				},
			},
		}

		jsonBytes, _ := json.Marshal(res)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(request.URL.Path, "/locations")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		res := armsubscription.SubscriptionsClientListLocationsResponse{
			LocationListResult: armsubscription.LocationListResult{
				Value: []*armsubscription.Location{
					{
						ID:             convert.RefOf("westus"),
						Name:           convert.RefOf("westus"),
						DisplayName:    convert.RefOf("West US"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_ID"),
					},
					{
						ID:             convert.RefOf("westus2"),
						Name:           convert.RefOf("westus2"),
						DisplayName:    convert.RefOf("West US 2"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_ID"),
					},
					{
						ID:             convert.RefOf("eastus"),
						Name:           convert.RefOf("eastus"),
						DisplayName:    convert.RefOf("East US"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_ID"),
					},
					{
						ID:             convert.RefOf("eastus2"),
						Name:           convert.RefOf("eastus2"),
						DisplayName:    convert.RefOf("East US 2"),
						SubscriptionID: convert.RefOf("SUBSCRIPTION_ID"),
					},
				},
			},
		}

		jsonBytes, _ := json.Marshal(res)

		return &http.Response{
			Request:    request,
			StatusCode: http.StatusOK,
			Header:     http.Header{},
			Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
		}, nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az account show")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		showRes := map[string]any{
			"id":        "SUBSCRIPTION_02",
			"name":      "Subscription 2",
			"isDefault": false,
		}

		jsonBytes, _ := json.Marshal(showRes)

		return exec.NewRunResult(0, string(jsonBytes), ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "az config get defaults.location")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		configRes := map[string]any{
			"name":   "location",
			"source": ".azure/config",
			"value":  "westus2",
		}

		jsonBytes, _ := json.Marshal(configRes)

		return exec.NewRunResult(0, string(jsonBytes), ""), nil
	})

}
