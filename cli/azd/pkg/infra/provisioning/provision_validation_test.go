// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning_test

import (
	"context"
	"errors"
	"testing"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/stretchr/testify/require"
)

// fakeValidationDispatcher is a stand-in for the extension validation
// dispatcher that returns a fixed set of results, capturing the dispatched
// check type and context for assertions.
type fakeValidationDispatcher struct {
	results     []*azdext.ValidationCheckResult
	ruleIDs     []string
	err         error
	gotType     string
	gotContext  map[string][]byte
	invocations int
}

func (f *fakeValidationDispatcher) DispatchChecks(
	_ context.Context,
	checkType string,
	contextData map[string][]byte,
) ([]*azdext.ValidationCheckResult, []string, error) {
	f.invocations++
	f.gotType = checkType
	f.gotContext = contextData
	return f.results, f.ruleIDs, f.err
}

func newProvisionValidationManager(
	t *testing.T,
	mockContext *mocks.MockContext,
	env *environment.Environment,
	dispatcher provisioning.ValidationCheckDispatcher,
) *provisioning.Manager {
	t.Helper()

	registerContainerDependencies(mockContext, env)
	mockContext.Container.MustRegisterSingleton(func() provisioning.ValidationCheckDispatcher {
		return dispatcher
	})

	mgr := provisioning.NewManager(
		mockContext.Container,
		defaultProvider,
		&mockenv.MockEnvManager{},
		env,
		mockContext.Console,
		mockContext.AlphaFeaturesManager,
		nil,
		cloud.AzurePublic(),
	)
	err := mgr.Initialize(*mockContext.Context, "", provisioning.Options{Provider: "test"})
	require.NoError(t, err)

	return mgr
}

func newProvisionValidationEnv() *environment.Environment {
	return environment.NewWithValues("test-env", map[string]string{
		"AZURE_SUBSCRIPTION_ID": "SUBSCRIPTION_ID",
		"AZURE_LOCATION":        "eastus2",
		"AZURE_RESOURCE_GROUP":  "rg-test",
	})
}

func TestManagerDeployRunsProvisionValidation_Warning(t *testing.T) {
	env := newProvisionValidationEnv()
	mockContext := mocks.NewMockContext(t.Context())

	var confirmed bool
	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).RespondFn(func(options input.ConsoleOptions) (any, error) {
		confirmed = true
		return true, nil
	})

	dispatcher := &fakeValidationDispatcher{
		results: []*azdext.ValidationCheckResult{
			{
				Severity:     azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_WARNING,
				DiagnosticId: "demo_warning",
				Message:      "a warning from an extension",
			},
		},
		ruleIDs: []string{"demo_warning"},
	}

	mgr := newProvisionValidationManager(t, mockContext, env, dispatcher)

	deployResult, err := mgr.Deploy(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	// Warning accepted → provisioning proceeds (not aborted).
	require.NotEqual(t, provisioning.PreflightAbortedSkipped, deployResult.SkippedReason)
	require.True(t, confirmed, "expected a confirmation prompt for the warning")

	// The provider-agnostic check type and lean context were dispatched.
	require.Equal(t, 1, dispatcher.invocations)
	require.Equal(t, azdext.ValidationCheckTypeProvision, dispatcher.gotType)
	require.Equal(t, "test-env", string(dispatcher.gotContext[azdext.ValidationContextEnvName]))
	require.Equal(t, "SUBSCRIPTION_ID", string(dispatcher.gotContext[azdext.ValidationContextSubscriptionID]))
	require.Equal(t, "eastus2", string(dispatcher.gotContext[azdext.ValidationContextEnvLocation]))
	require.Equal(t, "rg-test", string(dispatcher.gotContext[azdext.ValidationContextResourceGroup]))
	require.Equal(t, "resourceGroup", string(dispatcher.gotContext[azdext.ValidationContextTargetScope]))
}

func TestManagerDeployRunsProvisionValidation_WarningDeclined(t *testing.T) {
	env := newProvisionValidationEnv()
	mockContext := mocks.NewMockContext(t.Context())

	mockContext.Console.WhenConfirm(func(options input.ConsoleOptions) bool {
		return true
	}).Respond(false)

	dispatcher := &fakeValidationDispatcher{
		results: []*azdext.ValidationCheckResult{
			{
				Severity: azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_WARNING,
				Message:  "a warning from an extension",
			},
		},
	}

	mgr := newProvisionValidationManager(t, mockContext, env, dispatcher)

	deployResult, err := mgr.Deploy(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	// User declined → provisioning aborted via the preflight-aborted sentinel.
	require.Equal(t, provisioning.PreflightAbortedSkipped, deployResult.SkippedReason)
}

func TestManagerDeployRunsProvisionValidation_Error(t *testing.T) {
	env := newProvisionValidationEnv()
	mockContext := mocks.NewMockContext(t.Context())

	dispatcher := &fakeValidationDispatcher{
		results: []*azdext.ValidationCheckResult{
			{
				Severity:     azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR,
				DiagnosticId: "location_mismatch",
				Message:      "an error from an extension",
			},
		},
	}

	mgr := newProvisionValidationManager(t, mockContext, env, dispatcher)

	deployResult, err := mgr.Deploy(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	// Error severity → provisioning aborted without prompting.
	require.Equal(t, provisioning.PreflightAbortedSkipped, deployResult.SkippedReason)
}

func TestManagerPreviewRunsProvisionValidation_Error(t *testing.T) {
	env := newProvisionValidationEnv()
	mockContext := mocks.NewMockContext(t.Context())

	dispatcher := &fakeValidationDispatcher{
		results: []*azdext.ValidationCheckResult{
			{
				Severity: azdext.ValidationCheckSeverity_VALIDATION_CHECK_SEVERITY_ERROR,
				Message:  "an error from an extension",
			},
		},
	}

	mgr := newProvisionValidationManager(t, mockContext, env, dispatcher)

	previewResult, err := mgr.Preview(*mockContext.Context)

	require.Nil(t, previewResult)
	require.ErrorIs(t, err, provisioning.ErrProvisionValidationAborted)
}

func TestManagerDeployProvisionValidation_NoResultsProceeds(t *testing.T) {
	env := newProvisionValidationEnv()
	mockContext := mocks.NewMockContext(t.Context())

	dispatcher := &fakeValidationDispatcher{}

	mgr := newProvisionValidationManager(t, mockContext, env, dispatcher)

	deployResult, err := mgr.Deploy(*mockContext.Context)

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	require.NotEqual(t, provisioning.PreflightAbortedSkipped, deployResult.SkippedReason)
	require.Equal(t, 1, dispatcher.invocations)
}

func TestManagerDeployProvisionValidation_DispatchErrorIsNonFatal(t *testing.T) {
	env := newProvisionValidationEnv()
	mockContext := mocks.NewMockContext(t.Context())

	dispatcher := &fakeValidationDispatcher{
		err: errors.New("extension unreachable"),
	}

	mgr := newProvisionValidationManager(t, mockContext, env, dispatcher)

	deployResult, err := mgr.Deploy(*mockContext.Context)

	// A dispatch failure is logged and skipped; provisioning proceeds.
	require.NoError(t, err)
	require.NotNil(t, deployResult)
	require.NotEqual(t, provisioning.PreflightAbortedSkipped, deployResult.SkippedReason)
}
