// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/stretchr/testify/require"
)

type deploymentErrorFake struct {
	fakeDeployment
	deployErr error
}

func (f *deploymentErrorFake) Deploy(
	context.Context,
	azure.RawArmTemplate,
	azure.ArmParameters,
	map[string]*string,
	map[string]any,
) (*azapi.ResourceDeployment, error) {
	return nil, f.deployErr
}

func TestAnnotateDeploymentErrorResources_FromTarget(t *testing.T) {
	err := azapi.NewAzureDeploymentError(
		"Validation Error Details",
		`{"error":{"code":"DeploymentFailed","details":[`+
			`{"code":"InsufficientQuota","target":"ai-account",`+
			`"message":"Insufficient quota."}]}}`,
		azapi.DeploymentOperationValidate,
	)

	resources := []armTemplateResource{
		{
			Type: "Microsoft.CognitiveServices/accounts",
			Name: "ai-account",
		},
	}

	annotated := annotateDeploymentErrorLineForTest(err, resources)
	require.NotNil(t, annotated)

	deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](annotated)
	require.True(t, ok)
	require.Len(t, deploymentErr.Details.Inner[0].Inner, 1)
	require.Equal(
		t,
		"Microsoft.CognitiveServices/accounts",
		deploymentErr.Details.Inner[0].Inner[0].ResourceType,
	)
}

func TestAnnotateDeploymentErrorResources_FromMessage(t *testing.T) {
	err := azapi.NewAzureDeploymentError(
		"Validation Error Details",
		`{"error":{"code":"DeploymentFailed","details":[`+
			`{"code":"InsufficientQuota",`+
			`"message":"Cannot create/update/move resource 'ai-account'."}]}}`,
		azapi.DeploymentOperationValidate,
	)
	resources := []armTemplateResource{{
		Type: "Microsoft.CognitiveServices/accounts",
		Name: "ai-account",
	}}

	annotated := annotateDeploymentErrorLineForTest(err, resources)
	deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](annotated)
	require.True(t, ok)
	require.Len(t, deploymentErr.Details.Inner[0].Inner, 1)
	require.Equal(
		t,
		"Microsoft.CognitiveServices/accounts",
		deploymentErr.Details.Inner[0].Inner[0].ResourceType,
	)
}

func TestAnnotateDeploymentErrorResources_FromCompiledTemplate(t *testing.T) {
	err := azapi.NewAzureDeploymentError(
		"Validation Error Details",
		`{"error":{"code":"DeploymentFailed","details":[`+
			`{"code":"InsufficientQuota",`+
			`"message":"Cannot create/update/move resource 'ai-account'."}]}}`,
		azapi.DeploymentOperationValidate,
	)
	template := azure.RawArmTemplate(`{
		"$schema":"schema",
		"contentVersion":"1.0.0.0",
		"resources":[{
			"type":"Microsoft.CognitiveServices/accounts",
			"name":"ai-account"
		}]
	}`)

	annotated := annotateDeploymentErrorResources(err, nil, template)
	deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](annotated)
	require.True(t, ok)
	require.Len(t, deploymentErr.Details.Inner[0].Inner, 1)
	require.Equal(
		t,
		"Microsoft.CognitiveServices/accounts",
		deploymentErr.Details.Inner[0].Inner[0].ResourceType,
	)
}

func TestAnnotateDeploymentErrorResources_AmbiguousTargetFallsBack(t *testing.T) {
	err := azapi.NewAzureDeploymentError(
		"Validation Error Details",
		`{"error":{"code":"DeploymentFailed","details":[`+
			`{"code":"InsufficientQuota","target":"shared",`+
			`"message":"Insufficient quota."}]}}`,
		azapi.DeploymentOperationValidate,
	)
	resources := []armTemplateResource{
		{Type: "Microsoft.CognitiveServices/accounts", Name: "shared"},
		{Type: "Microsoft.Storage/storageAccounts", Name: "shared"},
	}

	annotated := annotateDeploymentErrorLineForTest(err, resources)
	deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](annotated)
	require.True(t, ok)
	require.Empty(t, deploymentErr.Details.Inner[0].Inner[0].ResourceType)
}

func TestAnnotateDeploymentErrorResources_ParameterizedNameFallsBack(t *testing.T) {
	err := azapi.NewAzureDeploymentError(
		"Preview Error Details",
		`{"error":{"code":"DeploymentFailed","details":[`+
			`{"code":"InsufficientQuota","target":"ai-account",`+
			`"message":"Insufficient quota."}]}}`,
		azapi.DeploymentOperationPreview,
	)
	template := azure.RawArmTemplate(`{
		"$schema":"schema",
		"contentVersion":"1.0.0.0",
		"resources":[{
			"type":"Microsoft.CognitiveServices/accounts",
			"name":"[parameters('accountName')]"
		}]
	}`)

	annotated := annotateDeploymentErrorResources(err, nil, template)
	deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](annotated)
	require.True(t, ok)
	require.Empty(t, deploymentErr.Details.Inner[0].Inner[0].ResourceType)
}

func TestAnnotatePreviewDeploymentErrors(t *testing.T) {
	template := azure.RawArmTemplate(`{
		"$schema":"schema",
		"contentVersion":"1.0.0.0",
		"resources":[{
			"type":"Microsoft.CognitiveServices/accounts",
			"name":"ai-account"
		}]
	}`)
	target := "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/ai-account"

	t.Run("DeployPreview error", func(t *testing.T) {
		err := azapi.NewAzureDeploymentError(
			"Preview Error Details",
			`{"error":{"code":"DeploymentFailed","details":[`+
				`{"code":"InsufficientQuota","target":"`+target+`",`+
				`"message":"Insufficient quota."}]}}`,
			azapi.DeploymentOperationPreview,
		)

		annotated := annotateDeploymentErrorResources(
			fmt.Errorf("deploying to resource group: %w", err),
			nil,
			template,
		)
		deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](annotated)
		require.True(t, ok)
		require.Equal(
			t,
			"Microsoft.CognitiveServices/accounts",
			deploymentErr.Details.Inner[0].Inner[0].ResourceType,
		)
	})

	t.Run("what-if response error", func(t *testing.T) {
		errResp := &armresources.ErrorResponse{
			Code: new("DeploymentFailed"),
			Details: []*armresources.ErrorResponse{{
				Code:    new("InsufficientQuota"),
				Target:  new(target),
				Message: new("Insufficient quota."),
			}},
		}
		err := azapi.NewAzureDeploymentErrorFromResponse(
			errResp,
			azapi.DeploymentOperationPreview,
		)

		annotated := annotateDeploymentErrorResources(err, nil, template)
		deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](annotated)
		require.True(t, ok)
		require.Equal(
			t,
			"Microsoft.CognitiveServices/accounts",
			deploymentErr.Details.Inner[0].ResourceType,
		)
	})
}

func TestDeploymentResources_UsesSnapshotBeforeTemplate(t *testing.T) {
	valCtx := &validationContext{
		SnapshotResources: []armTemplateResource{{
			Type: "Microsoft.CognitiveServices/accounts",
			Name: "resolved-account",
		}},
	}
	template := azure.RawArmTemplate(`{
		"$schema":"schema",
		"contentVersion":"1.0.0.0",
		"resources":[{"type":"Microsoft.Storage/storageAccounts","name":"template-account"}]
	}`)

	resources := deploymentResources(valCtx, template)
	require.Len(t, resources, 1)
	require.Equal(t, "resolved-account", resources[0].Name)
}

func TestDeployModuleAnnotatesDeploymentErrors(t *testing.T) {
	deploymentErr := azapi.NewAzureDeploymentError(
		"Deployment Error Details",
		`{"error":{"code":"DeploymentFailed","details":[`+
			`{"code":"InsufficientQuota","target":"ai-account",`+
			`"message":"Insufficient quota."}]}}`,
		azapi.DeploymentOperationDeploy,
	)
	target := &deploymentErrorFake{
		deployErr: fmt.Errorf("deploying: %w", deploymentErr),
	}
	template := azure.RawArmTemplate(`{
		"$schema":"schema",
		"contentVersion":"1.0.0.0",
		"resources":[{
			"type":"Microsoft.CognitiveServices/accounts",
			"name":"ai-account"
		}]
	}`)

	_, err := (&BicepProvider{}).deployModule(
		t.Context(),
		target,
		template,
		nil,
		nil,
		nil,
		&validationContext{SnapshotResources: []armTemplateResource{{
			Type: "Microsoft.CognitiveServices/accounts",
			Name: "ai-account",
		}}},
	)

	deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](err)
	require.True(t, ok)
	require.Equal(
		t,
		"Microsoft.CognitiveServices/accounts",
		deploymentErr.Details.Inner[0].Inner[0].ResourceType,
	)
}

func annotateDeploymentErrorLineForTest(err error, resources []armTemplateResource) error {
	return annotateDeploymentErrorResources(
		err,
		&validationContext{SnapshotResources: resources},
		nil,
	)
}
