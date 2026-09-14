// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
)

var deploymentResourceNamePattern = regexp.MustCompile(
	`(?i)\bresource\s+['"]([^'"]+)['"]`,
)

// annotateDeploymentErrorResources adds ARM resource types to error lines.
// It prefers full ARM targets and falls back to unique template matches.
// The original error is returned unchanged when the target is ambiguous.
func annotateDeploymentErrorResources(
	err error,
	valCtx *validationContext,
	armTemplate azure.RawArmTemplate,
) error {
	if err == nil {
		return nil
	}

	deploymentErr, ok := errors.AsType[*azapi.AzureDeploymentError](err)
	if !ok {
		return err
	}

	resources := deploymentResources(valCtx, armTemplate)
	annotateDeploymentErrorLine(deploymentErr.Details, resources, "")
	return err
}

func deploymentResources(
	valCtx *validationContext,
	rawArmTemplate azure.RawArmTemplate,
) []armTemplateResource {
	if valCtx != nil && len(valCtx.SnapshotResources) > 0 {
		return valCtx.SnapshotResources
	}

	var template armTemplate
	if jsonErr := json.Unmarshal(rawArmTemplate, &template); jsonErr != nil {
		return nil
	}

	return flattenDeploymentResources(template.Resources)
}

func annotateDeploymentErrorLine(
	line *azapi.DeploymentErrorLine,
	resources []armTemplateResource,
	inheritedTarget string,
) {
	if line == nil {
		return
	}

	target := strings.TrimSpace(line.Target)
	if target == "" {
		target = resourceNameFromErrorMessage(line.Message)
	}
	if target == "" {
		target = inheritedTarget
	}
	if line.Target == "" && target != "" {
		line.Target = target
	}

	if line.ResourceType == "" {
		line.ResourceType = resourceTypeFromTarget(target)
		if line.ResourceType == "" {
			if resource, ok := uniqueDeploymentResource(target, resources); ok {
				line.ResourceType = resource.Type
			}
		}
	}

	for _, inner := range line.Inner {
		annotateDeploymentErrorLine(inner, resources, target)
	}
}

func resourceNameFromErrorMessage(message string) string {
	match := deploymentResourceNamePattern.FindStringSubmatch(message)
	if len(match) == 2 {
		return strings.TrimSpace(match[1])
	}
	return ""
}

func resourceTypeFromTarget(target string) string {
	target = strings.Trim(strings.TrimSpace(target), "'\"")
	if !strings.Contains(strings.ToLower(target), "/providers/") {
		return ""
	}

	resourceType, err := arm.ParseResourceType(target)
	if err != nil {
		return ""
	}

	return resourceType.String()
}

func uniqueDeploymentResource(
	target string,
	resources []armTemplateResource,
) (armTemplateResource, bool) {
	var match armTemplateResource
	matchCount := 0
	for _, resource := range resources {
		if deploymentResourceMatchesTarget(resource, target) {
			match = resource
			matchCount++
		}
	}

	return match, matchCount == 1
}

func deploymentResourceMatchesTarget(resource armTemplateResource, target string) bool {
	target = strings.Trim(strings.TrimSpace(target), "'\"")
	if target == "" {
		return false
	}

	if resourceType := resourceTypeFromTarget(target); resourceType != "" {
		return strings.EqualFold(resource.Type, resourceType)
	}

	if resource.Name == "" || strings.Contains(resource.Name, "[") {
		return false
	}

	if strings.EqualFold(target, resource.Name) {
		return true
	}

	expected := strings.Trim(
		strings.TrimSpace(resource.Type+"/"+resource.Name),
		"/",
	)
	target = strings.Trim(strings.TrimSpace(target), "/")
	if _, after, ok := strings.Cut(target, "/providers/"); ok {
		target = after
	}

	return strings.EqualFold(target, expected)
}

func flattenDeploymentResources(resources []armTemplateResource) []armTemplateResource {
	flattened := make([]armTemplateResource, 0, len(resources))
	for _, resource := range resources {
		flattened = append(flattened, resource)
		flattened = append(flattened, flattenDeploymentResources(resource.Resources)...)
	}
	return flattened
}
