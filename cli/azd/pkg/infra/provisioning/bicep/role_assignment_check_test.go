// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/pkg/account"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/graphsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockgraphsdk"
)

type warningSubscriptionResolver struct{}

func newWarningBicepProvider(mockContext *mocks.MockContext) *BicepProvider {
	return &BicepProvider{
		env: environment.NewWithValues("test-env", map[string]string{
			environment.SubscriptionIdEnvVarName: "SUBSCRIPTION_ID",
			environment.LocationEnvVarName:       "westus2",
		}),
		console:             mockContext.Console,
		bicepCli:            bicep.NewCli(mockContext.Console, mockContext.CommandRunner),
		subscriptionManager: warningSubscriptionResolver{},
		serviceLocator:      mockContext.Container,
	}
}

func (warningSubscriptionResolver) GetSubscription(
	_ context.Context, subscriptionID string,
) (*account.Subscription, error) {
	return &account.Subscription{
		Id:       subscriptionID,
		TenantId: "tenant-id",
	}, nil
}

func prepareRoleWarningMocks(
	t *testing.T, mockContext *mocks.MockContext, conditional bool,
) {
	t.Helper()
	mockContext.Container.MustRegisterSingleton(func() *azapi.PermissionsService {
		return azapi.NewPermissionsService(
			mockContext.SubscriptionCredentialProvider, mockContext.ArmClientOptions)
	})
	mockContext.Container.MustRegisterSingleton(func() *azapi.UserProfileService {
		return azapi.NewUserProfileService(
			mockContext.MultiTenantCredentialProvider,
			mockContext.CoreClientOptions,
			cloud.AzurePublic(),
		)
	})
	mockgraphsdk.RegisterMeGetMock(mockContext, http.StatusOK,
		&graphsdk.UserProfile{Id: "principal-123"})

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(request.URL.Path, "/providers/Microsoft.Authorization/roleAssignments")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if !conditional {
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK,
				map[string]any{"value": []any{}})
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK,
			map[string]any{
				"value": []any{
					map[string]any{
						"properties": map[string]any{
							"roleDefinitionId": "/subscriptions/SUBSCRIPTION_ID/providers/" +
								"Microsoft.Authorization/roleDefinitions/role-id",
							"condition": "test condition",
						},
					},
				},
			})
	})
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(request.URL.Path,
				"/providers/Microsoft.Authorization/roleDefinitions/role-id")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK,
			map[string]any{
				"properties": map[string]any{
					"permissions": []any{
						map[string]any{
							"actions": []string{"Microsoft.Authorization/roleAssignments/write"},
						},
					},
				},
			})
	})
}

func TestProvisionValidationWarning_RolePermissions(t *testing.T) {
	for _, conditional := range []bool{false, true} {
		name := "missing"
		if conditional {
			name = "conditional"
		}
		t.Run(name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			prepareRoleWarningMocks(t, mockContext, conditional)
			provider := newWarningBicepProvider(mockContext)

			results, err := provider.checkRoleAssignmentPermissions(t.Context(),
				&validationContext{Props: resourcesProperties{HasRoleAssignments: true}})
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, ProvisionValidationCheckWarning, results[0].Severity)
			require.Equal(t, !conditional, results[0].IsCritical)
			if conditional {
				require.Equal(t, "role_assignment_conditional", results[0].DiagnosticID)
			} else {
				require.Equal(t, "role_assignment_missing", results[0].DiagnosticID)
				require.Contains(t, results[0].Message, "Principal ID:")
				require.Contains(t, results[0].Message, "principal-123")
				require.Contains(t, results[0].Message, "Subscription:")
				require.Contains(t, results[0].Message, "SUBSCRIPTION_ID")
				require.Contains(t, results[0].Message, "Required permission:")
				require.Contains(t, results[0].Message, "Microsoft.Authorization/roleAssignments/write")
			}
		})
	}
}

func TestProvisionValidationWarning_Confirmation(t *testing.T) {
	tests := []struct {
		name        string
		critical    bool
		regular     bool
		noPrompt    bool
		answer      bool
		wantDefault bool
		wantCancel  bool
		wantSummary string
	}{
		{
			name: "critical automation", critical: true, noPrompt: true,
			wantCancel: true, wantSummary: "1 warning found (1 critical).",
		},
		{
			name: "regular automation", regular: true, noPrompt: true,
			wantDefault: true, wantSummary: "1 warning found.",
		},
		{
			name: "mixed automation", critical: true, regular: true, noPrompt: true,
			wantCancel: true, wantSummary: "2 warnings found (1 critical).",
		},
		{
			name: "critical explicit yes", critical: true, answer: true,
			wantSummary: "1 warning found (1 critical).",
		},
		{
			name: "critical explicit no", critical: true,
			wantCancel: true, wantSummary: "1 warning found (1 critical).",
		},
		{
			name: "regular explicit no", regular: true, wantDefault: true,
			wantCancel: true, wantSummary: "1 warning found.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("AZD_CONFIG_DIR", t.TempDir())
			t.Setenv("AZD_BICEP_TOOL_PATH", "mock-bicep")
			mockContext := mocks.NewMockContext(t.Context())
			prepareRoleWarningMocks(t, mockContext, false)
			provider := newWarningBicepProvider(mockContext)

			resources := make([]armTemplateResource, 0, 2)
			if tt.critical {
				resources = append(resources, armTemplateResource{
					Type: "Microsoft.Authorization/roleAssignments",
					Name: "assignment", APIVersion: "2022-04-01",
				})
			}
			if tt.regular {
				resources = append(resources, armTemplateResource{
					Type: "Microsoft.Web/sites",
					Name: "login-site", APIVersion: "2022-03-01",
				})
			}
			snapshotBytes, err := json.Marshal(snapshotResult{PredictedResources: resources})
			require.NoError(t, err)
			mockContext.CommandRunner.When(func(args exec.RunArgs, _ string) bool {
				return len(args.Args) > 1 && args.Args[0] == "snapshot"
			}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
				file := strings.TrimSuffix(args.Args[1], filepath.Ext(args.Args[1])) + ".snapshot.json"
				return exec.NewRunResult(0, "", ""), os.WriteFile(file, snapshotBytes, 0o600)
			})

			var outputBuffer strings.Builder
			prompted := false
			if tt.noPrompt {
				provider.console = input.NewConsole(true, false,
					input.Writers{Output: &outputBuffer},
					input.ConsoleHandles{
						Stdin: strings.NewReader(""), Stdout: &outputBuffer, Stderr: &outputBuffer,
					},
					&output.NoneFormatter{}, nil)
			} else {
				mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
					return true
				}).RespondFn(func(options input.ConsoleOptions) (any, error) {
					prompted = true
					require.Equal(t, "Proceed with deployment anyway?", options.Message)
					require.Equal(t, tt.wantDefault, options.DefaultValue)
					return tt.answer, nil
				})
			}

			raw := azure.RawArmTemplate(
				`{"$schema":"x","contentVersion":"1.0","resources":[{"type":"Microsoft.Web/sites"}]}`)
			_, canceled, err := provider.traceLocalProvisionValidation(
				t.Context(), nil, filepath.Join(t.TempDir(), "main.bicepparam"),
				raw, azure.ArmParameters{}, false)
			require.NoError(t, err)
			require.Equal(t, tt.wantCancel, canceled)
			if tt.noPrompt {
				require.Contains(t, outputBuffer.String(), tt.wantSummary)
			} else {
				require.True(t, prompted)
				require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), tt.wantSummary)
			}
		})
	}
}

func TestProvisionValidationCheckFn_SkipsWhenNoRoleAssignments(t *testing.T) {
	called := false
	checkFn := ProvisionValidationCheckFn(func(
		ctx context.Context,
		valCtx *validationContext,
	) ([]ProvisionValidationCheckResult, error) {
		called = true
		if !valCtx.Props.HasRoleAssignments {
			return nil, nil
		}
		return []ProvisionValidationCheckResult{{
			Severity: ProvisionValidationCheckError,
			Message:  "missing permissions",
		}}, nil
	})

	valCtx := &validationContext{
		Props: resourcesProperties{HasRoleAssignments: false},
	}

	result, err := checkFn(t.Context(), valCtx)
	require.NoError(t, err)
	require.True(t, called)
	require.Nil(t, result)
}

func TestProvisionValidationCheckFn_ReportsErrorWhenRoleAssignments(t *testing.T) {
	checkFn := ProvisionValidationCheckFn(func(
		ctx context.Context,
		valCtx *validationContext,
	) ([]ProvisionValidationCheckResult, error) {
		if !valCtx.Props.HasRoleAssignments {
			return nil, nil
		}
		return []ProvisionValidationCheckResult{{
			Severity: ProvisionValidationCheckError,
			Message:  "missing role assignment permissions",
		}}, nil
	})

	valCtx := &validationContext{
		Props: resourcesProperties{HasRoleAssignments: true},
	}

	results, err := checkFn(t.Context(), valCtx)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, ProvisionValidationCheckError, results[0].Severity)
	require.Contains(t, results[0].Message, "missing role assignment permissions")
}
