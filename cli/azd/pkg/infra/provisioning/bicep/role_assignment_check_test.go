// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/tracing"
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
				&validationContext{
					Props: resourcesProperties{HasRoleAssignments: true},
					SnapshotResources: []armTemplateResource{{
						Type: "Microsoft.Authorization/roleAssignments", Scope: "/subscriptions/SUBSCRIPTION_ID",
					}},
				})
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
				require.Equal(t,
					"Ensure you have the 'Role Based Access Control Administrator', "+
						"'User Access Administrator', 'Owner', or a custom role "+
						"with 'Microsoft.Authorization/roleAssignments/write' assigned to your account.",
					results[0].Suggestion)
			}
		})
	}
}

func TestProvisionValidationWarning_Confirmation(t *testing.T) {
	tests := []struct {
		name              string
		critical          bool
		crossSubscription bool
		regular           bool
		noPrompt          bool
		realConsole       bool
		input             string
		answer            bool
		wantDefault       bool
		wantCancel        bool
		wantError         bool
		wantEOF           bool
		wantSummary       string
		wantOutcome       string
	}{
		{
			name: "critical automation", critical: true, noPrompt: true,
			wantError: true, wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeCanceledNoPrompt,
		},
		{
			name: "regular automation", regular: true, noPrompt: true,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "cross-subscription automation", crossSubscription: true, noPrompt: true,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "mixed automation", critical: true, regular: true, noPrompt: true,
			wantError: true, wantSummary: "2 warnings found (1 critical).",
			wantOutcome: provisionValidationOutcomeCanceledNoPrompt,
		},
		{
			name: "critical explicit yes", critical: true, answer: true,
			wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "critical explicit no", critical: true,
			wantCancel: true, wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeCanceledByUser,
		},
		{
			name: "regular explicit no", regular: true, wantDefault: true,
			wantCancel: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeCanceledByUser,
		},
		{
			name: "critical exhausted stdin", critical: true, realConsole: true,
			wantEOF: true, wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeError,
		},
		{
			name: "critical invalid exhausted stdin", critical: true, realConsole: true, input: "invalid",
			wantEOF: true, wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeError,
		},
		{
			name: "critical piped no", critical: true, realConsole: true, input: "n",
			wantCancel: true, wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeCanceledByUser,
		},
		{
			name: "critical piped no line", critical: true, realConsole: true, input: "n\n",
			wantCancel: true, wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeCanceledByUser,
		},
		{
			name: "critical entered default", critical: true, realConsole: true, input: "\n",
			wantCancel: true, wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeCanceledByUser,
		},
		{
			name: "critical piped yes", critical: true, realConsole: true, input: "y\n",
			wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "critical piped yes token", critical: true, realConsole: true, input: "y",
			wantSummary: "1 critical warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "regular exhausted stdin", regular: true, realConsole: true,
			wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
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
			if tt.critical || tt.crossSubscription {
				scope := "/subscriptions/SUBSCRIPTION_ID"
				if tt.crossSubscription {
					scope = "/subscriptions/other-subscription"
				}
				resources = append(resources, armTemplateResource{
					Type: "Microsoft.Authorization/roleAssignments",
					Name: "assignment", APIVersion: "2022-04-01",
					Scope: scope,
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
			if tt.noPrompt || tt.realConsole {
				provider.console = input.NewConsole(tt.noPrompt, false,
					input.Writers{Output: &outputBuffer},
					input.ConsoleHandles{
						Stdin: strings.NewReader(tt.input), Stdout: &outputBuffer, Stderr: &outputBuffer,
					},
					&output.NoneFormatter{}, nil)
			} else {
				mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
					return true
				}).RespondFn(func(options input.ConsoleOptions) (any, error) {
					prompted = true
					wantMessage := "Proceed with deployment anyway?"
					if tt.critical {
						wantMessage = "Deployment will likely fail. Proceed anyway?"
					}
					require.Equal(t, wantMessage, options.Message)
					require.Equal(t, tt.critical, options.IsWarning)
					require.Equal(t, tt.critical, options.RequireResponse)
					require.Equal(t, tt.wantDefault, options.DefaultValue)
					return tt.answer, nil
				})
			}

			raw := azure.RawArmTemplate(
				`{"$schema":"x","contentVersion":"1.0","resources":[{"type":"Microsoft.Web/sites"}]}`)
			_, canceled, err := provider.traceLocalProvisionValidation(
				t.Context(), nil, filepath.Join(t.TempDir(), "main.bicepparam"),
				raw, azure.ArmParameters{}, false)
			if tt.wantEOF {
				require.ErrorIs(t, err, io.EOF)
				require.NotErrorIs(t, err, internal.ErrAbortedByUser)
			} else if tt.wantError {
				require.ErrorIs(t, err, internal.ErrValidationFailed)
				require.NotErrorIs(t, err, internal.ErrAbortedByUser)
				suggestion, ok := errors.AsType[*internal.ErrorWithSuggestion](err)
				require.True(t, ok)
				require.Contains(t, suggestion.Message, "Provisioning was canceled")
				require.Contains(t, suggestion.Message, "critical warnings")
				require.Contains(t, suggestion.Suggestion, "without --no-prompt")
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, tt.wantCancel, canceled)
			outcome := findSpanAttribute(tracing.GetUsageAttributes(), "validation.provision.outcome")
			require.NotNil(t, outcome)
			require.Equal(t, tt.wantOutcome, outcome.Value.AsString())
			if tt.noPrompt || tt.realConsole {
				require.Contains(t, outputBuffer.String(), tt.wantSummary)
			} else {
				require.True(t, prompted)
				require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), tt.wantSummary)
			}
		})
	}
}

func TestProvisionValidationWarning_RolePermissionScopes(t *testing.T) {
	tests := []struct {
		name     string
		scopes   []string
		critical bool
	}{
		{name: "checked subscription", scopes: []string{"/subscriptions/SUBSCRIPTION_ID"}, critical: true},
		{name: "case insensitive", scopes: []string{"/SUBSCRIPTIONS/subscription_id/"}, critical: true},
		{name: "cross subscription", scopes: []string{"/subscriptions/other-subscription"}},
		{name: "resource group", scopes: []string{"/subscriptions/SUBSCRIPTION_ID/resourceGroups/rg"}},
		{name: "resource", scopes: []string{"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Storage/storageAccounts/a"}},
		{name: "unknown", scopes: []string{""}},
		{name: "unresolved", scopes: []string{"[subscription().id]"}},
		{name: "no snapshot"},
		{
			name:     "checked and unchecked assignments",
			scopes:   []string{"/subscriptions/other-subscription", "/subscriptions/SUBSCRIPTION_ID"},
			critical: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			prepareRoleWarningMocks(t, mockContext, false)
			provider := newWarningBicepProvider(mockContext)
			valCtx := &validationContext{Props: resourcesProperties{HasRoleAssignments: true}}
			for _, scope := range tt.scopes {
				valCtx.SnapshotResources = append(valCtx.SnapshotResources, armTemplateResource{
					Type: "Microsoft.Authorization/roleAssignments", Scope: scope,
				})
			}
			results, err := provider.checkRoleAssignmentPermissions(t.Context(), valCtx)
			require.NoError(t, err)
			require.Len(t, results, 1)
			require.Equal(t, tt.critical, results[0].IsCritical)
			if !tt.critical {
				require.NotContains(t, results[0].Message, "Deployment will likely fail")
				require.Contains(t, results[0].Message, "scopes were not verified")
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
