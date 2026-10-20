// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type byoDeploymentFailure struct {
	classified, cause, diagnostics error
}

func deploymentDiagnosticString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (e *byoDeploymentFailure) Error() string { return e.classified.Error() }

func (e *byoDeploymentFailure) Unwrap() []error {
	causes := []error{e.classified, e.cause}
	if e.diagnostics != nil {
		causes = append(causes, e.diagnostics)
	}
	return causes
}

func (p *FoundryProvisioningProvider) deploymentFailure(ctx context.Context, cause error) error {
	classified := exterrors.ServiceFromAzure(cause, exterrors.OpArmDeploymentCreate)
	if p.synthResult == nil || p.synthResult.Hosting == nil {
		return classified
	}
	result := &byoDeploymentFailure{classified: classified, cause: cause}
	service, ok := errors.AsType[*azdext.ServiceError](classified)
	if !ok {
		return result
	}
	var details strings.Builder
	details.WriteString("F01/F02 Foundry ARM service stage: ")
	details.WriteString(service.Message)
	if response, ok := errors.AsType[*azcore.ResponseError](cause); ok && response.RawResponse != nil {
		for _, header := range []string{"x-ms-correlation-request-id", "x-ms-request-id", "x-ms-client-request-id"} {
			if value := response.RawResponse.Header.Get(header); value != "" {
				fmt.Fprintf(&details, "\n%s: %s", header, value)
			}
		}
	}
	if ctx.Err() == nil {
		diagnosticCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		id := "/subscriptions/" + p.subID + "/providers/Microsoft.Resources/deployments/" + p.deploymentName()
		if p.byoDeploymentID != "" {
			id = p.byoDeploymentID
		}
		result.diagnostics = p.failedDeploymentDetails(diagnosticCtx, id, &details, map[string]bool{})
		if result.diagnostics != nil {
			fmt.Fprintf(&details, "\nAdditional deployment-operation evidence unavailable: %s", result.diagnostics)
		}
	}
	copy := *service
	copy.Message = onboarding.RedactDiagnostic(details.String())
	copy.Suggestion = service.Suggestion
	if copy.Suggestion != "" {
		copy.Suggestion += " "
	}
	copy.Suggestion += "Inspect the reported service stage and correlation IDs. " +
		"If the service identifies a tenant Blueprint/FIC policy rejection, ask the tenant/policy administrator; " +
		"azd does not modify tenant policy or grant global Microsoft Graph access."
	result.classified = &copy
	return result
}

func (p *FoundryProvisioningProvider) failedDeploymentDetails(
	ctx context.Context, deploymentID string, details *strings.Builder, visited map[string]bool,
) error {
	key := strings.ToLower(deploymentID)
	if visited[key] {
		return nil
	}
	if len(visited) >= 16 {
		return fmt.Errorf("nested deployment diagnostic limit reached; inspect remaining operations with the correlation IDs")
	}
	visited[key] = true
	if !strings.HasPrefix(key, "/subscriptions/") {
		return fmt.Errorf("nested deployment has no supported ARM resource ID")
	}
	id, err := arm.ParseResourceID(deploymentID)
	if err != nil || !strings.EqualFold(id.ResourceType.String(), "Microsoft.Resources/deployments") {
		return fmt.Errorf("nested deployment has an invalid ARM deployment ID")
	}
	client, err := armresources.NewDeploymentOperationsClient(id.SubscriptionID, p.credential, p.byoARMOptions())
	if err != nil {
		return err
	}
	var operations []*armresources.DeploymentOperation
	if id.ResourceGroupName == "" {
		pager := client.NewListAtSubscriptionScopePager(id.Name, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return err
			}
			operations = append(operations, page.Value...)
			if len(operations) > 128 {
				return fmt.Errorf("deployment operation diagnostic limit reached")
			}
		}
	} else {
		pager := client.NewListPager(id.ResourceGroupName, id.Name, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return err
			}
			operations = append(operations, page.Value...)
			if len(operations) > 128 {
				return fmt.Errorf("deployment operation diagnostic limit reached")
			}
		}
	}
	for _, operation := range operations {
		if operation == nil || operation.Properties == nil {
			continue
		}
		properties := operation.Properties
		if properties.StatusMessage == nil || properties.StatusMessage.Error == nil {
			continue
		}
		data, err := json.Marshal(properties.StatusMessage)
		if err != nil {
			return fmt.Errorf("encode failed deployment-operation evidence: %w", err)
		}
		fmt.Fprintf(details, "\nDeployment %s operation %v serviceRequestId=%v status=%v: %s",
			deploymentID, deploymentDiagnosticString(operation.ID), deploymentDiagnosticString(properties.ServiceRequestID),
			deploymentDiagnosticString(properties.StatusCode), data)
		target := properties.TargetResource
		if target != nil && target.ID != nil && target.ResourceType != nil &&
			strings.EqualFold(*target.ResourceType, "Microsoft.Resources/deployments") {
			if err := p.failedDeploymentDetails(ctx, *target.ID, details, visited); err != nil {
				return err
			}
		}
	}
	return nil
}
