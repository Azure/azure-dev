package cmd

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
	"sort"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
)

func Test_promptEnvironmentName(t *testing.T) {
	t.Run("valid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).SetError(errors.New("prompt should not be called for valid environment name"))

		environmentName := "hello"

		err := ensureValidEnvironmentName(*mockContext.Context, &environmentName, mockContext.Console)

		require.NoError(t, err)
	})

	t.Run("empty name gets prompted", func(t *testing.T) {
		environmentName := ""

		mockContext := mocks.NewMockContext(context.Background())
		mockContext.Console.WhenPrompt(func(options input.ConsoleOptions) bool {
			return true
		}).Respond("someEnv")

		err := ensureValidEnvironmentName(*mockContext.Context, &environmentName, mockContext.Console)

		require.NoError(t, err)
		require.Equal(t, "someEnv", environmentName)
	})

	t.Run("duplicate resource groups ignored", func(t *testing.T) {
		mockDeployment := armresources.DeploymentExtended{
			Properties: &armresources.DeploymentPropertiesExtended{
				Dependencies: []*armresources.Dependency{
					{
						DependsOn: []*armresources.BasicDependency{
							{
								ResourceName: convert.RefOf("groupA"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
							{
								ResourceName: convert.RefOf("groupB"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
							{
								ResourceName: convert.RefOf("ignoredForWrongType"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeStorageAccount)),
							},
						},
					},
					{
						DependsOn: []*armresources.BasicDependency{
							{
								ResourceName: convert.RefOf("groupA"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
							{
								ResourceName: convert.RefOf("groupB"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
							{
								ResourceName: convert.RefOf("groupC"),
								ResourceType: convert.RefOf(string(infra.AzureResourceTypeResourceGroup)),
							},
						},
					},
				},
			},
		}

		mockContext := mocks.NewMockContext(context.Background())

		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				"/subscriptions/sub-id/providers/Microsoft.Resources/deployments",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			subscriptionsListBytes, _ := json.Marshal(mockDeployment)

			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBuffer(subscriptionsListBytes)),
			}, nil
		})

		azCli := azcli.NewAzCli(mockContext.Credentials, azcli.NewAzCliArgs{
			HttpClient: mockContext.HttpClient,
		})

		resourceManager := infra.NewAzureResourceManager(azCli)
		groups, err := resourceManager.GetResourceGroupsForDeployment(*mockContext.Context, "sub-id", "deployment-name")
		require.NoError(t, err)

		sort.Strings(groups)
		require.Equal(t, []string{"groupA", "groupB", "groupC"}, groups)
	})
}

func Test_getSubscriptionOptions(t *testing.T) {
	t.Run("no default config set", func(t *testing.T) {
		mockAccount := &mockaccount.MockAccountManager{
			Subscriptions: []account.Subscription{
				{
					Id:                 "1",
					Name:               "sub1",
					TenantId:           "",
					UserAccessTenantId: "",
					IsDefault:          false,
				},
			},
		}
		subList, result, err := getSubscriptionOptions(context.Background(), mockAccount)

		require.Nil(t, err)
		require.EqualValues(t, 2, len(subList))
		require.EqualValues(t, nil, result)
	})

	t.Run("default value set", func(t *testing.T) {
		// mocked configk
		defaultSubId := "SUBSCRIPTION_DEFAULT"
		ctx := context.Background()
		mockAccount := &mockaccount.MockAccountManager{
			DefaultLocation:     "location",
			DefaultSubscription: defaultSubId,
			Subscriptions: []account.Subscription{
				{
					Id:                 defaultSubId,
					Name:               "DISPLAY DEFAULT",
					TenantId:           "TENANT",
					UserAccessTenantId: "USER_TENANT",
					IsDefault:          true,
				},
				{
					Id:                 "SUBSCRIPTION_OTHER",
					Name:               "DISPLAY OTHER",
					TenantId:           "TENANT",
					UserAccessTenantId: "USER_TENANT",
					IsDefault:          false,
				},
			},
			Locations: []azcli.AzCliLocation{},
		}

		subList, result, err := getSubscriptionOptions(ctx, mockAccount)

		require.Nil(t, err)
		require.EqualValues(t, 3, len(subList))
		require.NotNil(t, result)
		defSub, ok := result.(string)
		require.True(t, ok)
		require.EqualValues(t, " 1. DISPLAY DEFAULT (SUBSCRIPTION_DEFAULT)", defSub)
	})
}

func Test_createAndInitEnvironment(t *testing.T) {
	t.Run("invalid name", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)
		invalidEnvName := "*!33"
		_, err := createAndInitEnvironment(
			*mockContext.Context,
			&environmentSpec{
				environmentName: invalidEnvName,
			},
			azdContext,
			mockContext.Console,
			&mockaccount.MockAccountManager{},
			&azcli.UserProfileService{},
			&account.SubscriptionsManager{},
		)
		require.ErrorContains(
			t,
			err,
			fmt.Sprintf("environment name '%s' is invalid (it should contain only alphanumeric characters and hyphens)\n",
				invalidEnvName))
	})

	t.Run("env already exists", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		validName := "azdEnv"
		err := os.MkdirAll(filepath.Join(tempDir, ".azure", validName), 0755)
		require.NoError(t, err)
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)

		_, err = createAndInitEnvironment(
			*mockContext.Context,
			&environmentSpec{
				environmentName: validName,
			},
			azdContext,
			mockContext.Console,
			&mockaccount.MockAccountManager{},
			&azcli.UserProfileService{},
			&account.SubscriptionsManager{},
		)
		require.ErrorContains(
			t,
			err,
			fmt.Sprintf("environment '%s' already exists",
				validName))
	})
	t.Run("ensure Initialized", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		tempDir := t.TempDir()
		validName := "azdEnv"
		azdContext := azdcontext.NewAzdContextWithDirectory(tempDir)

		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Please select an Azure Subscription to use")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			// Select the first from the list
			return 0, nil
		})
		mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
			return strings.Contains(options.Message, "Please select an Azure location")
		}).RespondFn(func(options input.ConsoleOptions) (any, error) {
			// Select the first from the list
			return 0, nil
		})

		expectedPrincipalId := "oid"
		mockContext.HttpClient.When(func(request *http.Request) bool {
			return request.Method == http.MethodGet && strings.Contains(
				request.URL.Path,
				"/me",
			)
		}).RespondFn(func(request *http.Request) (*http.Response, error) {
			return mocks.CreateHttpResponseWithBody(request, 200, graphsdk.UserProfile{
				Id:                expectedPrincipalId,
				GivenName:         "John",
				Surname:           "Doe",
				JobTitle:          "Software Engineer",
				DisplayName:       "John Doe",
				UserPrincipalName: "john.doe@contoso.com",
			})
		})

		expectedSub := "00000000-0000-0000-0000-000000000000"
		expectedLocation := "location"
		createdEnv, err := createAndInitEnvironment(
			*mockContext.Context,
			&environmentSpec{
				environmentName: validName,
			},
			azdContext,
			mockContext.Console,
			&mockaccount.MockAccountManager{
				Subscriptions: []account.Subscription{{Id: expectedSub}},
				Locations:     []azcli.AzCliLocation{{DisplayName: "west", Name: expectedLocation}},
			},
			azcli.NewUserProfileService(
				&mocks.MockMultiTenantCredentialProvider{},
				mockContext.HttpClient,
			),
			&mockSubscriptionTenantResolver{},
		)
		require.NoError(t, err)

		require.Equal(t, createdEnv.GetEnvName(), validName)
		require.Equal(t, createdEnv.GetPrincipalId(), expectedPrincipalId)
		require.Equal(t, createdEnv.GetSubscriptionId(), expectedSub)
		require.Equal(t, createdEnv.GetLocation(), expectedLocation)
	})
}

type mockSubscriptionTenantResolver struct {
}

func (m *mockSubscriptionTenantResolver) LookupTenant(
	ctx context.Context, subscriptionId string) (tenantId string, err error) {
	return "00000000-0000-0000-0000-000000000000", nil
}
