// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"fmt"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

type monitoringTargetBinding struct {
	projectID string
	owned     bool
	legacy    bool
	outputs   any
	plan      *armresources.WhatIfOperationResult
	retry     bool
}

// A deployment record is a binding only while all inputs that can select a
// different native project still agree. Cached environment IDs are not bindings.
func previousMonitoringTarget(
	parameters map[string]any,
	previous *armresources.DeploymentPropertiesExtended,
	subscriptionID string,
) *monitoringTargetBinding {
	if previous == nil || previous.ProvisioningState == nil ||
		*previous.ProvisioningState != armresources.ProvisioningStateSucceeded {
		return nil
	}
	if !monitoringIdentityMatches(parameters, previous.Parameters) {
		return nil
	}
	oldParameters := previous.Parameters.(map[string]any)
	existingID, existing := armStringParameter(parameters, "projectResourceId")
	outputs := armOutputsToProto(previous.Outputs)
	project := outputs["AZURE_AI_PROJECT_ID"]
	if project == nil {
		return nil
	}
	group, _ := armStringParameter(parameters, "resourceGroupName")
	name, _ := armStringParameter(parameters, "foundryProjectName")
	if existing {
		if !strings.EqualFold(existingID, project.Value) {
			return nil
		}
		if _, err := monitoringResourceID(existingID, "Microsoft.CognitiveServices/accounts/projects"); err != nil {
			return nil
		}
	} else if !monitoringProjectMatches(project.Value, subscriptionID, group, name) {
		return nil
	}
	mode, present := armStringParameter(oldParameters, "monitoringMode")
	if !present {
		if output := outputs["AZD_FOUNDRY_MONITORING_MODE"]; output != nil {
			mode, present = output.Value, true
		}
	}
	return &monitoringTargetBinding{
		projectID: project.Value,
		owned:     mode == "create",
		legacy:    !present || mode == "none",
		outputs:   previous.Outputs,
	}
}

func monitoringIdentityMatches(parameters map[string]any, previous any) bool {
	oldParameters, ok := previous.(map[string]any)
	if !ok {
		return false
	}
	identityParameters := []string{"location", "resourceGroupName", "resourceTokenSalt"}
	if _, existing := armStringParameter(parameters, "projectResourceId"); existing {
		identityParameters = append(identityParameters, "projectResourceId")
	} else {
		identityParameters = append(identityParameters, "foundryProjectName")
	}
	for _, name := range identityParameters {
		current, currentOK := armStringParameter(parameters, name)
		old, oldOK := armStringParameter(oldParameters, name)
		if !currentOK || !oldOK || current != old {
			return false
		}
	}
	return true
}

// Use ARM's resolved resource IDs, not a second implementation of uniqueString.
// What-If also returns unrelated resources as Ignore; they are never candidates.
func monitoringProjectFromWhatIf(
	result armresources.WhatIfOperationResult,
	subscriptionID, resourceGroup, projectName string,
) (string, error) {
	if err := whatIfFailure(result); err != nil {
		return "", err
	}
	projectID := ""
	if result.Properties != nil {
		for _, change := range result.Properties.Changes {
			if change == nil || change.ResourceID == nil || change.ChangeType == nil ||
				*change.ChangeType == armresources.ChangeTypeIgnore {
				continue
			}
			if !monitoringProjectMatches(*change.ResourceID, subscriptionID, resourceGroup, projectName) {
				continue
			}
			if *change.ChangeType == armresources.ChangeTypeUnsupported {
				return "", fmt.Errorf("ARM What-If could not resolve the selected Foundry project; no monitoring writes were authorized")
			}
			switch *change.ChangeType {
			case armresources.ChangeTypeCreate, armresources.ChangeTypeModify,
				armresources.ChangeTypeNoChange, armresources.ChangeTypeDeploy:
			default:
				return "", fmt.Errorf("ARM What-If returned an unsafe project change; no monitoring writes were authorized")
			}
			if projectID != "" && !strings.EqualFold(projectID, *change.ResourceID) {
				return "", fmt.Errorf("ARM What-If resolved multiple Foundry project targets; no monitoring writes were authorized")
			}
			projectID = *change.ResourceID
		}
	}
	if projectID == "" {
		return "", fmt.Errorf("ARM What-If did not resolve the selected Foundry project; no monitoring writes were authorized")
	}
	return projectID, nil
}

func monitoringProjectMatches(id, subscriptionID, resourceGroup, projectName string) bool {
	resource, err := arm.ParseResourceID(id)
	return err == nil && resource.Parent != nil &&
		strings.EqualFold(resource.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") &&
		strings.EqualFold(resource.SubscriptionID, subscriptionID) &&
		strings.EqualFold(resource.ResourceGroupName, resourceGroup) &&
		strings.EqualFold(resource.Name, projectName)
}

func armStringParameter(parameters map[string]any, name string) (string, bool) {
	envelope, ok := parameters[name].(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := envelope["value"].(string)
	return value, ok
}
