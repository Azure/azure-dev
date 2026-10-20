// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"

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

type byoLifecycleChecker struct {
	checker   onboarding.ResourceChecker
	lifecycle onboarding.Lifecycle
	spec      func(onboarding.Plan, onboarding.Facts) (onboarding.FoundrySpec, error)
}

func (c byoLifecycleChecker) Check(ctx context.Context, plan onboarding.Plan) (
	onboarding.Facts, onboarding.Report, error,
) {
	if c.checker == nil {
		return onboarding.Facts{}, nil, fmt.Errorf("BYO-AKS resource checker is required")
	}
	facts, report, err := c.checker.Check(ctx, plan)
	if err != nil || report.Err() != nil {
		return facts, report, err
	}
	spec, err := c.spec(plan, facts)
	if err != nil {
		return facts, report, err
	}
	lifecycleReport, err := c.lifecycle.Check(ctx, plan, spec)
	return facts, append(report, lifecycleReport...), err
}

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
	account := onboarding.PlannedFoundryAccount(plan)
	project, ok := byoParameterValue(source, "foundryProjectName").(string)
	if !ok || project == "" {
		return onboarding.FoundrySpec{}, fmt.Errorf("effective Foundry project name is required for lifecycle ownership")
	}
	spec := onboarding.FoundrySpec{
		AccountID: account.ID, ProjectID: account.ID + "/projects/" + project,
		RegistryID: plan.Registry.ID, RegistryABAC: abac, Location: plan.Location, TemplateHash: hash,
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
	if !reflect.DeepEqual(templateWithoutMetadata(template), templateWithoutMetadata(standard)) {
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

func templateWithoutMetadata(value any) any {
	switch v := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(v))
		for key, item := range v {
			if key != "metadata" {
				result[key] = templateWithoutMetadata(item)
			}
		}
		return result
	case []any:
		result := make([]any, len(v))
		for index, item := range v {
			result[index] = templateWithoutMetadata(item)
		}
		return result
	default:
		return value
	}
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
	parameters := maps.Clone(source.parameters)
	deployment.Tags["azd-byoaks-owner"] = new(plan.Owner)
	var properties *armresources.DeploymentPropertiesExtended
	for _, registryReady := range []bool{false, true} {
		if err := ctx.Err(); err != nil {
			return nil, p.byoLifecycleError(err, exterrors.OpArmDeploymentCreate)
		}
		name := "azd-byo-" + uuid.NewString()
		id := "/subscriptions/" + p.subID + "/providers/Microsoft.Resources/deployments/" + name
		if err := lifecycle.Start(ctx, plan, p.byoSpec, id); err != nil {
			return nil, p.byoLifecycleError(err, exterrors.OpArmDeploymentCreate)
		}
		p.byoDeploymentID = id
		parameters["byoRegistryAccessReady"] = map[string]any{"value": registryReady}
		deployment.Properties.Parameters = parameters
		progress(fmt.Sprintf("Starting owned Foundry ARM deployment %q...", name))
		poller, err := client.BeginCreateOrUpdateAtSubscriptionScope(ctx, name, deployment, nil)
		if err != nil {
			return nil, p.deploymentFailure(ctx, err)
		}
		response, err := pollWithProgress(ctx, poller, progress, "Foundry deployment in progress")
		if err != nil {
			return nil, p.deploymentFailure(ctx, err)
		}
		if err := ctx.Err(); err != nil {
			return nil, p.byoLifecycleError(err, exterrors.OpArmDeploymentCreate)
		}
		if err := lifecycle.Observe(ctx, plan); err != nil {
			return nil, p.byoLifecycleError(err, exterrors.OpArmDeploymentCreate)
		}
		properties = response.Properties
		if !registryReady {
			progress("Preparing the actual Foundry project identity registry grant...")
			if err := lifecycle.PrepareProjectAccess(ctx, plan); err != nil {
				return nil, p.byoLifecycleError(err, exterrors.OpBYOProjectRegistryAccess)
			}
		}
	}
	p.synthResult.Parameters["byoRegistryAccessReady"] = true
	progress("Foundry deployment complete")
	return &azdext.ProvisioningDeployResult{
		Deployment: &azdext.ProvisioningDeployment{
			Parameters: armInputsToProto(parameters),
			Outputs:    p.normalizeOutputs(p.withTenantOutput(armOutputsToProto(deploymentOutputs(properties)))),
		},
	}, nil
}

func (p *FoundryProvisioningProvider) byoLifecycleError(cause error, operation string) error {
	message := onboarding.RedactDiagnostic(cause.Error())
	var classified error
	if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
		classified = exterrors.Cancelled("BYO-AKS lifecycle interrupted; retain the journal and resume the operation")
	} else if _, ok := errors.AsType[*azcore.ResponseError](cause); ok {
		classified = exterrors.ServiceFromAzure(cause, operation)
		if service, ok := errors.AsType[*azdext.ServiceError](classified); ok {
			copy := *service
			copy.Message = message
			copy.Suggestion = "Retain the ownership journal, resolve the reported permission/service failure, and retry."
			classified = &copy
		}
	} else {
		classified = exterrors.Validation(
			exterrors.CodeInvalidServiceConfig, message,
			"retain the ownership journal; resolve the ownership/deployment state before retrying",
		)
	}
	return &byoDeploymentFailure{classified: classified, cause: cause}
}
