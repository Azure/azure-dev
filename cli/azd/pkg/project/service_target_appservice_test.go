// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/stretchr/testify/require"
)

type serviceTargetValidationTest struct {
	targetResource *environment.TargetResource
	expectError    bool
}

func TestNewAppServiceTargetTypeValidation(t *testing.T) {
	t.Parallel()

	tests := map[string]*serviceTargetValidationTest{
		"ValidateTypeSuccess": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", string(azapi.AzureResourceTypeWebSite)),
			expectError:    false,
		},
		"ValidateTypeLowerCaseSuccess": {
			targetResource: environment.NewTargetResource(
				"SUB_ID",
				"RG_ID",
				"res",
				strings.ToLower(string(azapi.AzureResourceTypeWebSite)),
			),
			expectError: false,
		},
		"ValidateTypeFail": {
			targetResource: environment.NewTargetResource("SUB_ID", "RG_ID", "res", "BadType"),
			expectError:    true,
		},
	}

	for test, data := range tests {
		t.Run(test, func(t *testing.T) {
			serviceTarget := &appServiceTarget{}

			err := serviceTarget.validateTargetResource(data.targetResource)
			if data.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestSlotEnvVarNameForService(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		serviceName string
		expected    string
	}{
		"SimpleService": {
			serviceName: "api",
			expected:    "AZD_DEPLOY_API_SLOT_NAME",
		},
		"HyphenatedService": {
			serviceName: "my-web-app",
			expected:    "AZD_DEPLOY_MY_WEB_APP_SLOT_NAME",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result := slotEnvVarNameForService(tc.serviceName)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestIgnoreSlotsEnvVarNameForService(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		serviceName string
		expected    string
	}{
		"SimpleService": {
			serviceName: "api",
			expected:    "AZD_DEPLOY_API_IGNORE_SLOTS",
		},
		"HyphenatedService": {
			serviceName: "my-web-app",
			expected:    "AZD_DEPLOY_MY_WEB_APP_IGNORE_SLOTS",
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			result := ignoreSlotsEnvVarNameForService(tc.serviceName)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestDetermineDeploymentTargets_IgnoreSlots(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		envVars        map[string]string
		hasDeployments bool
		slots          []string
		expectedSlots  []string // empty string = main app
	}{
		"IgnoreSlots_True_NoSlots": {
			envVars:        map[string]string{"AZD_DEPLOY_API_IGNORE_SLOTS": "true"},
			hasDeployments: true,
			slots:          []string{},
			expectedSlots:  []string{""},
		},
		"IgnoreSlots_True_OneSlot": {
			envVars:        map[string]string{"AZD_DEPLOY_API_IGNORE_SLOTS": "true"},
			hasDeployments: true,
			slots:          []string{"staging"},
			expectedSlots:  []string{""},
		},
		"IgnoreSlots_True_MultipleSlots": {
			envVars:        map[string]string{"AZD_DEPLOY_API_IGNORE_SLOTS": "true"},
			hasDeployments: true,
			slots:          []string{"staging", "preview"},
			expectedSlots:  []string{""},
		},
		"IgnoreSlots_True_FirstDeployment_WithSlots": {
			envVars:        map[string]string{"AZD_DEPLOY_API_IGNORE_SLOTS": "true"},
			hasDeployments: false,
			slots:          []string{"staging"},
			expectedSlots:  []string{""},
		},
		"IgnoreSlots_True_OverridesSlotName": {
			envVars: map[string]string{
				"AZD_DEPLOY_API_IGNORE_SLOTS": "true",
				"AZD_DEPLOY_API_SLOT_NAME":    "staging",
			},
			hasDeployments: true,
			slots:          []string{"staging", "preview"},
			expectedSlots:  []string{""},
		},
		"IgnoreSlots_False_OneSlot_SubsequentDeploy": {
			envVars:        map[string]string{"AZD_DEPLOY_API_IGNORE_SLOTS": "false"},
			hasDeployments: true,
			slots:          []string{"staging"},
			expectedSlots:  []string{"staging"},
		},
		"IgnoreSlots_Unset_OneSlot_SubsequentDeploy": {
			envVars:        map[string]string{},
			hasDeployments: true,
			slots:          []string{"staging"},
			expectedSlots:  []string{"staging"},
		},
		"IgnoreSlots_Unset_NoSlots_SubsequentDeploy": {
			envVars:        map[string]string{},
			hasDeployments: true,
			slots:          []string{},
			expectedSlots:  []string{""},
		},
		"IgnoreSlots_Unset_FirstDeploy_WithSlots": {
			envVars:        map[string]string{},
			hasDeployments: false,
			slots:          []string{"staging"},
			expectedSlots:  []string{"", "staging"},
		},
		"IgnoreSlots_TrueNumeric": {
			envVars:        map[string]string{"AZD_DEPLOY_API_IGNORE_SLOTS": "1"},
			hasDeployments: true,
			slots:          []string{"staging"},
			expectedSlots:  []string{""},
		},
		"IgnoreSlots_TrueUppercase": {
			envVars:        map[string]string{"AZD_DEPLOY_API_IGNORE_SLOTS": "TRUE"},
			hasDeployments: true,
			slots:          []string{"staging"},
			expectedSlots:  []string{""},
		},
		"IgnoreSlots_InvalidValue_FallsBackToSlotLogic": {
			envVars:        map[string]string{"AZD_DEPLOY_API_IGNORE_SLOTS": "notabool"},
			hasDeployments: true,
			slots:          []string{"staging"},
			expectedSlots:  []string{"staging"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			azCli := azapi.NewAzureClient(
				mockaccount.SubscriptionCredentialProviderFunc(
					func(_ context.Context, _ string) (azcore.TokenCredential, error) {
						return mockContext.Credentials, nil
					},
				),
				mockContext.ArmClientOptions,
			)

			// Mock deployment history
			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == http.MethodGet &&
					strings.Contains(request.URL.Path, "/deployments")
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				var deployments []*armappservice.Deployment
				if tc.hasDeployments {
					deployments = []*armappservice.Deployment{
						{ID: new("dep-1"), Name: new("dep-1")},
					}
				}
				response := armappservice.WebAppsClientListDeploymentsResponse{
					DeploymentCollection: armappservice.DeploymentCollection{
						Value: deployments,
					},
				}
				return mocks.CreateHttpResponseWithBody(
					request, http.StatusOK, response)
			})

			// Mock slots
			mockContext.HttpClient.When(func(request *http.Request) bool {
				return request.Method == http.MethodGet &&
					strings.Contains(request.URL.Path, "/slots")
			}).RespondFn(func(request *http.Request) (*http.Response, error) {
				sites := make([]*armappservice.Site, len(tc.slots))
				for i, slot := range tc.slots {
					fullName := "WEB_APP_NAME/" + slot
					sites[i] = &armappservice.Site{Name: &fullName}
				}
				response := armappservice.WebAppsClientListSlotsResponse{
					WebAppCollection: armappservice.WebAppCollection{
						Value: sites,
					},
				}
				return mocks.CreateHttpResponseWithBody(
					request, http.StatusOK, response)
			})

			env := environment.NewWithValues("test", tc.envVars)
			target := &appServiceTarget{
				env:     env,
				cli:     azCli,
				console: mockContext.Console,
			}

			serviceConfig := &ServiceConfig{Name: "api"}
			targetResource := environment.NewTargetResource(
				"SUB_ID", "RG_ID", "WEB_APP_NAME",
				string(azapi.AzureResourceTypeWebSite),
			)
			progress := async.NewNoopProgress[ServiceProgress]()

			targets, err := target.determineDeploymentTargets(
				*mockContext.Context,
				serviceConfig,
				targetResource,
				progress,
			)

			require.NoError(t, err)
			require.Len(t, targets, len(tc.expectedSlots))
			for i, expected := range tc.expectedSlots {
				require.Equal(t, expected, targets[i].SlotName)
			}
		})
	}
}
