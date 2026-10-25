// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

func (p *FoundryProvisioningProvider) ownedBYOLifecycle() onboarding.Lifecycle {
	if p.byoLifecycle != nil {
		return p.byoLifecycle
	}
	return onboarding.NewLifecycle(p.credential, p.armClientOptions, environmentJournal{p})
}

func byoFoundrySpec(plan onboarding.Plan, source *templateSource, abac bool) (onboarding.FoundrySpec, error) {
	hash, err := standardBYOTemplateHash(source.armTemplate)
	if err != nil {
		return onboarding.FoundrySpec{}, err
	}
	digest, err := onboarding.TemplateDigest(source.armTemplate)
	if err != nil {
		return onboarding.FoundrySpec{}, err
	}
	account := onboarding.PlannedFoundryAccount(plan)
	project, ok := byoParameterValue(source, "foundryProjectName").(string)
	if !ok || project == "" {
		return onboarding.FoundrySpec{}, fmt.Errorf("effective Foundry project name is required for lifecycle ownership")
	}
	spec := onboarding.FoundrySpec{
		AccountID: account.ID, ProjectID: account.ID + "/projects/" + project,
		RegistryID: plan.Registry.ID, RegistryABAC: abac, Location: plan.Location,
		TemplateHash: hash, TemplateDigest: digest,
	}
	if raw := byoParameterValue(source, "deployments"); raw != nil {
		data, err := json.Marshal(raw)
		if err != nil {
			return spec, err
		}
		var deployments []struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(data, &deployments); err != nil {
			return spec, fmt.Errorf("read exact model deployment lifecycle targets: %w", err)
		}
		for _, deployment := range deployments {
			spec.ModelNames = append(spec.ModelNames, deployment.Name)
		}
	}
	return spec, nil
}

func standardBYOTemplateHash(template map[string]any) (string, error) {
	raw, err := synthesis.ARMTemplate()
	if err != nil {
		return "", err
	}
	var standard map[string]any
	if err := json.Unmarshal(raw, &standard); err != nil {
		return "", err
	}
	actual, err := onboarding.TemplateDigest(template)
	if err != nil {
		return "", err
	}
	expected, err := onboarding.TemplateDigest(standard)
	if err != nil {
		return "", err
	}
	if actual != expected {
		return "", fmt.Errorf("BYO-AKS requires the standard ownership-bound template; " +
			"restore/regenerate the Bicep files and express supported selections through configuration/parameters")
	}
	metadata, ok := template["metadata"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("BYO-AKS template lacks generated template-hash evidence")
	}
	generator, ok := metadata["_generator"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("BYO-AKS template lacks generator template-hash evidence")
	}
	hash, ok := generator["templateHash"].(string)
	if !ok || hash == "" {
		return "", fmt.Errorf("BYO-AKS template hash is unavailable")
	}
	return hash, nil
}

func (p *FoundryProvisioningProvider) byoARMOptions() *arm.ClientOptions {
	options := arm.ClientOptions{DisableRPRegistration: true}
	if p.armClientOptions != nil {
		options = *p.armClientOptions
		options.DisableRPRegistration = true
	}
	return &options
}

func (p *FoundryProvisioningProvider) deployBYO(
	ctx context.Context, progress grpcbroker.ProgressFunc, source *templateSource, deployment armresources.Deployment,
) (*azdext.ProvisioningDeployResult, error) {
	lifecycle := p.ownedBYOLifecycle()
	plan := *p.byoPlan
	client, err := armresources.NewDeploymentsClient(p.subID, p.credential, p.byoARMOptions())
	if err != nil {
		return nil, err
	}
	deployment.Tags["azd-byoaks-owner"] = new(plan.Owner)
	if err := ctx.Err(); err != nil {
		return nil, p.byoLifecycleError(err, exterrors.OpArmDeploymentCreate)
	}
	name := "azd-byo-" + uuid.NewString()
	id := "/subscriptions/" + p.subID + "/providers/Microsoft.Resources/deployments/" + name
	if err := lifecycle.Start(ctx, plan, p.byoSpec, id); err != nil {
		return nil, p.byoLifecycleError(err, exterrors.OpArmDeploymentCreate)
	}
	p.byoDeploymentID = id
	progress(fmt.Sprintf("Starting owned Foundry ARM deployment %q...", name))
	poller, err := client.BeginCreateOrUpdateAtSubscriptionScope(ctx, name, deployment, nil)
	if err != nil {
		return nil, p.deploymentFailure(ctx, err)
	}
	response, err := pollWithProgress(ctx, poller, progress, "Foundry deployment in progress")
	if err != nil {
		return nil, p.deploymentFailure(ctx, err)
	}
	if err := lifecycle.Observe(ctx, plan); err != nil {
		return nil, p.byoLifecycleError(err, exterrors.OpArmDeploymentCreate)
	}
	progress("Foundry deployment complete")
	return &azdext.ProvisioningDeployResult{
		Deployment: &azdext.ProvisioningDeployment{
			Parameters: armInputsToProto(source.parameters),
			Outputs:    p.normalizeOutputs(p.withTenantOutput(armOutputsToProto(deploymentOutputs(response.Properties)))),
		},
	}, nil
}

func (p *FoundryProvisioningProvider) byoLifecycleError(cause error, operation string) error {
	message := onboarding.RedactDiagnostic(cause.Error())
	var classified error
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		classified = exterrors.Cancelled("BYO-AKS deployment interrupted; retain its ownership record and resume the operation")
	} else if _, ok := errors.AsType[*azcore.ResponseError](cause); ok {
		classified = exterrors.ServiceFromAzure(cause, operation)
		if service, ok := errors.AsType[*azdext.ServiceError](classified); ok {
			copy := *service
			copy.Message = message
			copy.Suggestion = "Retain the deployment ownership record, resolve the reported permission/service failure, and retry."
			classified = &copy
		}
	} else {
		classified = exterrors.Validation(
			exterrors.CodeInvalidServiceConfig, message,
			"retain the deployment ownership record; resolve the ownership/deployment state before retrying",
		)
	}
	return &byoDeploymentFailure{classified: classified, cause: cause}
}
