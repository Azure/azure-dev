// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/appservice/armappservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazapi"
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

// mockSlotsResponse registers a mock HTTP response for the GetAppServiceSlots call.
func mockSlotsResponse(mockContext *mocks.MockContext, slotNames []string) {
	sites := make([]*armappservice.Site, len(slotNames))
	for i, name := range slotNames {
		sites[i] = &armappservice.Site{
			Name: new("WEB_APP_NAME/" + name),
		}
	}
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "/slots")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		response := armappservice.WebAppsClientListSlotsResponse{
			WebAppCollection: armappservice.WebAppCollection{
				Value: sites,
			},
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, response)
	})
}

func TestDetermineDeploymentTargets(t *testing.T) {
	t.Parallel()

	serviceConfig := &ServiceConfig{Name: "my-api"}
	targetResource := environment.NewTargetResource(
		"SUB_ID", "RG_ID", "WEB_APP_NAME", string(azapi.AzureResourceTypeWebSite),
	)

	type testCase struct {
		name           string
		slotNames      []string
		envVars        map[string]string
		noPrompt       bool
		selectIndex    int // for interactive prompt mock (-1 = no prompt expected)
		expectError    bool
		expectErrorMsg string
		expectTargets  []string // empty string = main app
	}

	tests := []testCase{
		{
			name:          "SlotName_Production_DeploysToMainApp",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "production"},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{""},
		},
		{
			name:          "SlotName_Production_CaseInsensitive",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "Production"},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{""},
		},
		{
			name:          "SlotName_Staging_DeploysToSlot",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "staging"},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{"staging"},
		},
		{
			name:           "SlotName_Invalid_ReturnsError",
			slotNames:      []string{"staging", "canary"},
			envVars:        map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "nonexistent"},
			noPrompt:       true,
			selectIndex:    -1,
			expectError:    true,
			expectErrorMsg: "not found",
		},
		{
			name:          "NoSlots_DeploysToMainApp",
			slotNames:     []string{},
			envVars:       map[string]string{},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{""},
		},
		{
			name:           "SlotsExist_NoPrompt_NoSlotName_FailsWithError",
			slotNames:      []string{"staging"},
			envVars:        map[string]string{},
			noPrompt:       true,
			selectIndex:    -1,
			expectError:    true,
			expectErrorMsg: "no target specified",
		},
		{
			name:           "MultipleSlots_NoPrompt_NoSlotName_FailsWithError",
			slotNames:      []string{"staging", "canary"},
			envVars:        map[string]string{},
			noPrompt:       true,
			selectIndex:    -1,
			expectError:    true,
			expectErrorMsg: "no target specified",
		},
		{
			name:          "SlotsExist_Interactive_SelectMainApp",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{},
			noPrompt:      false,
			selectIndex:   0, // Index 0 = "production (main app)"
			expectTargets: []string{""},
		},
		{
			name:          "SlotsExist_Interactive_SelectSlot",
			slotNames:     []string{"staging"},
			envVars:       map[string]string{},
			noPrompt:      false,
			selectIndex:   1, // Index 1 = "staging"
			expectTargets: []string{"staging"},
		},
		{
			name:          "MultipleSlots_Interactive_SelectSecondSlot",
			slotNames:     []string{"staging", "canary"},
			envVars:       map[string]string{},
			noPrompt:      false,
			selectIndex:   2, // Index 0=production, 1=staging, 2=canary
			expectTargets: []string{"canary"},
		},
		{
			name:          "SlotName_Production_NoSlotsExist_StillWorks",
			slotNames:     []string{},
			envVars:       map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "production"},
			noPrompt:      true,
			selectIndex:   -1,
			expectTargets: []string{""},
		},
		{
			name:           "SlotName_Invalid_ErrorIncludesProductionHint",
			slotNames:      []string{"staging"},
			envVars:        map[string]string{"AZD_DEPLOY_MY_API_SLOT_NAME": "bad"},
			noPrompt:       true,
			selectIndex:    -1,
			expectError:    true,
			expectErrorMsg: "production",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			mockContext := mocks.NewMockContext(t.Context())
			azCli := mockazapi.NewAzureClientFromMockContext(mockContext)

			// Set up env vars
			env := environment.New("test")
			for k, v := range tc.envVars {
				env.DotenvSet(k, v)
			}

			// Mock slots response
			mockSlotsResponse(mockContext, tc.slotNames)

			// Set no-prompt mode
			mockContext.Console.SetNoPromptMode(tc.noPrompt)

			// Mock interactive select if needed
			if tc.selectIndex >= 0 {
				mockContext.Console.WhenSelect(func(options input.ConsoleOptions) bool {
					return true
				}).Respond(tc.selectIndex)
			}

			target := &appServiceTarget{
				env:     env,
				cli:     azCli,
				console: mockContext.Console,
			}

			progress := async.NewNoopProgress[ServiceProgress]()
			targets, err := target.determineDeploymentTargets(
				*mockContext.Context,
				serviceConfig,
				targetResource,
				progress,
			)

			if tc.expectError {
				require.Error(t, err)
				if tc.expectErrorMsg != "" {
					require.Contains(t, err.Error(), tc.expectErrorMsg)
				}
			} else {
				require.NoError(t, err)
				require.Len(t, targets, len(tc.expectTargets))
				for i, expected := range tc.expectTargets {
					require.Equal(t, expected, targets[i].SlotName)
				}
			}
		})
	}
}
