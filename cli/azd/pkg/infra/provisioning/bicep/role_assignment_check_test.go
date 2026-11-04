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

type rolePermissionState int

const (
	rolePermissionMissing rolePermissionState = iota
	rolePermissionGranted
	rolePermissionConditional
)

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

func roleAssignmentSnapshotResource(scope string) armTemplateResource {
	resource := armTemplateResource{
		Type:       "Microsoft.Authorization/roleAssignments",
		Name:       "assignment",
		APIVersion: "2022-04-01",
	}
	if scope != "" {
		resource.ID = strings.TrimRight(scope, "/") +
			"/providers/Microsoft.Authorization/roleAssignments/assignment"
	}
	return resource
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
	t *testing.T, mockContext *mocks.MockContext, permission rolePermissionState,
) {
	t.Helper()
	prepareRoleWarningMocksWithPrincipal(
		t, mockContext, permission, http.StatusOK, &graphsdk.UserProfile{Id: "principal-123"})
}

func prepareRoleWarningMocksWithPrincipal(
	t *testing.T,
	mockContext *mocks.MockContext,
	permission rolePermissionState,
	principalStatus int,
	userProfile *graphsdk.UserProfile,
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
	mockgraphsdk.RegisterMeGetMock(mockContext, principalStatus, userProfile)

	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.HasSuffix(request.URL.Path, "/providers/Microsoft.Authorization/roleAssignments")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		if permission == rolePermissionMissing {
			return mocks.CreateHttpResponseWithBody(request, http.StatusOK,
				map[string]any{"value": []any{}})
		}
		properties := map[string]any{
			"roleDefinitionId": "/subscriptions/SUBSCRIPTION_ID/providers/" +
				"Microsoft.Authorization/roleDefinitions/role-id",
		}
		if permission == rolePermissionConditional {
			properties["condition"] = "test condition"
		}
		return mocks.CreateHttpResponseWithBody(request, http.StatusOK,
			map[string]any{
				"value": []any{
					map[string]any{
						"properties": properties,
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
	tests := []struct {
		name           string
		permission     rolePermissionState
		wantDiagnostic string
		wantCritical   bool
	}{
		{
			name: "missing", permission: rolePermissionMissing,
			wantDiagnostic: "role_assignment_missing", wantCritical: true,
		},
		{name: "granted", permission: rolePermissionGranted},
		{
			name: "conditional", permission: rolePermissionConditional,
			wantDiagnostic: "role_assignment_conditional",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			prepareRoleWarningMocks(t, mockContext, tt.permission)
			provider := newWarningBicepProvider(mockContext)

			results, err := provider.checkRoleAssignmentPermissions(t.Context(),
				&validationContext{
					Props: resourcesProperties{HasRoleAssignments: true},
					SnapshotResources: []armTemplateResource{
						roleAssignmentSnapshotResource("/subscriptions/SUBSCRIPTION_ID"),
					},
				})
			require.NoError(t, err)
			if tt.wantDiagnostic == "" {
				require.Empty(t, results)
				return
			}
			require.Len(t, results, 1)
			require.Equal(t, ProvisionValidationCheckWarning, results[0].Severity)
			require.Equal(t, tt.wantCritical, results[0].IsCritical)
			require.Equal(t, tt.wantDiagnostic, results[0].DiagnosticID)
			if tt.permission == rolePermissionMissing {
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

func TestProvisionValidationWarning_UsesBicepSnapshotResourceID(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	prepareRoleWarningMocks(t, mockContext, rolePermissionMissing)
	provider := newWarningBicepProvider(mockContext)

	var snapshot snapshotResult
	require.NoError(t, json.Unmarshal([]byte(`{
		"predictedResources": [{
			"id": "/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Authorization/roleAssignments/assignment",
			"type": "Microsoft.Authorization/roleAssignments",
			"name": "assignment",
			"apiVersion": "2022-04-01"
		}]
	}`), &snapshot))

	results, err := provider.checkRoleAssignmentPermissions(t.Context(), &validationContext{
		Props:             analyzeResources(snapshot.PredictedResources),
		SnapshotResources: snapshot.PredictedResources,
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "role_assignment_missing", results[0].DiagnosticID)
	require.True(t, results[0].IsCritical)
}

func TestProvisionValidationWarning_PreservesUnverifiedScopeWhenPrincipalResolutionFails(t *testing.T) {
	mockContext := mocks.NewMockContext(t.Context())
	prepareRoleWarningMocksWithPrincipal(
		t, mockContext, rolePermissionMissing, http.StatusInternalServerError, nil)
	provider := newWarningBicepProvider(mockContext)

	results, err := provider.checkRoleAssignmentPermissions(t.Context(), &validationContext{
		Props: resourcesProperties{HasRoleAssignments: true},
		SnapshotResources: []armTemplateResource{
			roleAssignmentSnapshotResource("/subscriptions/other-subscription"),
		},
	})
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "role_assignment_scope_unverified", results[0].DiagnosticID)
	require.False(t, results[0].IsCritical)
	require.Contains(t, results[0].Message, "current principal could not be determined")
	require.NotContains(t, results[0].Message, "Principal ID:")
	require.Contains(t, results[0].Message, "Environment subscription:")
	require.Contains(t, results[0].Message, "Required permission:")
}

func TestProvisionValidationWarning_Confirmation(t *testing.T) {
	tests := []struct {
		name        string
		critical    bool
		roleScope   *string
		regular     bool
		noPrompt    bool
		realConsole bool
		input       string
		answer      bool
		wantDefault bool
		wantCancel  bool
		wantError   bool
		wantEOF     bool
		wantSummary string
		wantOutcome string
		permission  rolePermissionState
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
			name: "cross-subscription automation", roleScope: new("/subscriptions/other-subscription"), noPrompt: true,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name:      "cross-subscription automation with environment permission",
			roleScope: new("/subscriptions/other-subscription"), noPrompt: true,
			permission:  rolePermissionGranted,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name:      "resource-group automation",
			roleScope: new("/subscriptions/SUBSCRIPTION_ID/resourceGroups/rg"), noPrompt: true,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name:      "resource automation",
			roleScope: new("/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Storage/storageAccounts/a"), noPrompt: true,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "unknown scope automation", roleScope: new(""), noPrompt: true,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "unresolved scope automation", roleScope: new("[subscription().id]"), noPrompt: true,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "cross-subscription interactive", roleScope: new("/subscriptions/other-subscription"), answer: true,
			wantDefault: true, wantSummary: "1 warning found.",
			wantOutcome: provisionValidationOutcomeWarningsAccepted,
		},
		{
			name: "mixed unverified automation", roleScope: new("/subscriptions/other-subscription"),
			regular: true, noPrompt: true,
			wantDefault: true, wantSummary: "2 warnings found.",
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
			prepareRoleWarningMocks(t, mockContext, tt.permission)
			provider := newWarningBicepProvider(mockContext)

			resources := make([]armTemplateResource, 0, 2)
			if tt.critical || tt.roleScope != nil {
				scope := "/subscriptions/SUBSCRIPTION_ID"
				if tt.roleScope != nil {
					scope = *tt.roleScope
				}
				resources = append(resources, roleAssignmentSnapshotResource(scope))
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
				if !tt.critical {
					require.NotContains(t, outputBuffer.String(), "Deployment will likely fail")
				}
			} else {
				require.True(t, prompted)
				require.Contains(t, strings.Join(mockContext.Console.Output(), "\n"), tt.wantSummary)
			}
		})
	}
}

func TestProvisionValidationWarning_RolePermissionScopes(t *testing.T) {
	tests := []struct {
		name            string
		scopes          []string
		permission      rolePermissionState
		wantDiagnostics []string
		wantCritical    []bool
	}{
		{
			name: "deployment subscription", scopes: []string{"/subscriptions/SUBSCRIPTION_ID"},
			wantDiagnostics: []string{"role_assignment_missing"}, wantCritical: []bool{true},
		},
		{
			name: "deployment subscription granted", scopes: []string{"/subscriptions/SUBSCRIPTION_ID"},
			permission: rolePermissionGranted,
		},
		{
			name: "case insensitive", scopes: []string{"/SUBSCRIPTIONS/subscription_id/"},
			wantDiagnostics: []string{"role_assignment_missing"}, wantCritical: []bool{true},
		},
		{
			name: "cross subscription", scopes: []string{"/subscriptions/other-subscription"},
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name:   "cross subscription with environment permission",
			scopes: []string{"/subscriptions/other-subscription"}, permission: rolePermissionGranted,
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name: "resource group", scopes: []string{"/subscriptions/SUBSCRIPTION_ID/resourceGroups/rg"},
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name:       "resource group with environment permission",
			scopes:     []string{"/subscriptions/SUBSCRIPTION_ID/resourceGroups/rg"},
			permission: rolePermissionGranted,
		},
		{
			name:       "resource group with conditional environment permission",
			scopes:     []string{"/subscriptions/SUBSCRIPTION_ID/resourceGroups/rg"},
			permission: rolePermissionConditional,
			wantDiagnostics: []string{
				"role_assignment_conditional",
			},
			wantCritical: []bool{false},
		},
		{
			name:            "resource",
			scopes:          []string{"/subscriptions/SUBSCRIPTION_ID/providers/Microsoft.Storage/storageAccounts/a"},
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name: "tenant", scopes: []string{"/"},
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name:            "management group",
			scopes:          []string{"/providers/Microsoft.Management/managementGroups/group"},
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name: "unknown", scopes: []string{""},
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name: "unresolved", scopes: []string{"[subscription().id]"},
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name:            "no snapshot",
			wantDiagnostics: []string{"role_assignment_scope_unverified"}, wantCritical: []bool{false},
		},
		{
			name:            "checked and unchecked scopes",
			scopes:          []string{"/subscriptions/other-subscription", "/subscriptions/SUBSCRIPTION_ID"},
			wantDiagnostics: []string{"role_assignment_missing", "role_assignment_scope_unverified"},
			wantCritical:    []bool{true, false},
		},
		{
			name:       "checked and unchecked scopes with environment permission",
			scopes:     []string{"/subscriptions/other-subscription", "/subscriptions/SUBSCRIPTION_ID"},
			permission: rolePermissionGranted,
			wantDiagnostics: []string{
				"role_assignment_scope_unverified",
			},
			wantCritical: []bool{false},
		},
		{
			name:       "checked and unchecked scopes with conditional permission",
			scopes:     []string{"/subscriptions/other-subscription", "/subscriptions/SUBSCRIPTION_ID"},
			permission: rolePermissionConditional,
			wantDiagnostics: []string{
				"role_assignment_conditional", "role_assignment_scope_unverified",
			},
			wantCritical: []bool{false, false},
		},
		{
			name:   "multiple unchecked scopes",
			scopes: []string{"/subscriptions/other-subscription", "/subscriptions/SUBSCRIPTION_ID/resourceGroups/rg"},
			wantDiagnostics: []string{
				"role_assignment_scope_unverified",
			},
			wantCritical: []bool{false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockContext := mocks.NewMockContext(t.Context())
			prepareRoleWarningMocks(t, mockContext, tt.permission)
			provider := newWarningBicepProvider(mockContext)
			valCtx := &validationContext{Props: resourcesProperties{HasRoleAssignments: true}}
			for _, scope := range tt.scopes {
				valCtx.SnapshotResources = append(
					valCtx.SnapshotResources, roleAssignmentSnapshotResource(scope))
			}
			results, err := provider.checkRoleAssignmentPermissions(t.Context(), valCtx)
			require.NoError(t, err)
			require.Len(t, results, len(tt.wantDiagnostics))
			for i, diagnostic := range tt.wantDiagnostics {
				require.Equal(t, ProvisionValidationCheckWarning, results[i].Severity)
				require.Equal(t, diagnostic, results[i].DiagnosticID)
				require.Equal(t, tt.wantCritical[i], results[i].IsCritical)
				switch diagnostic {
				case "role_assignment_missing":
					require.Contains(t, results[i].Message, "Deployment will likely fail")
				case "role_assignment_scope_unverified":
					require.NotContains(t, results[i].Message, "Deployment will likely fail")
					require.Contains(t, results[i].Message, "target scopes were not verified")
					require.Contains(t, results[i].Suggestion, "at the scopes of the role assignments")
				}
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
