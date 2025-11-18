// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/azure/azure-dev/cli/azd/internal/tracing"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/project"
)

func getResourceGroupFollowUp(
	ctx context.Context,
	formatter output.Formatter,
	portalUrlBase string,
	projectConfig *project.ProjectConfig,
	resourceManager project.ResourceManager,
	env *environment.Environment,
	whatIf bool,
) (followUp string) {
	if formatter.Kind() == output.JsonFormat {
		return followUp
	}

	subscriptionId := env.GetSubscriptionId()
	resourceGroupName, err := resourceManager.GetResourceGroupName(
		ctx,
		subscriptionId,
		projectConfig.ResourceGroupName,
	)
	if err == nil {
		suffix := ":\n" + AzurePortalLink(portalUrlBase, subscriptionId, resourceGroupName)

		if v, err := strconv.ParseBool(os.Getenv("AZD_DEMO_MODE")); err == nil && v {
			suffix = "."
		}

		defaultFollowUpText := fmt.Sprintf(
			"You can view the resources created under the resource group %s in Azure Portal", resourceGroupName)
		if whatIf {
			defaultFollowUpText = fmt.Sprintf(
				"You can view the current resources under the resource group %s in Azure Portal", resourceGroupName)
		}
		followUp = defaultFollowUpText + suffix
	}

	return followUp
}

func AzurePortalLink(portalUrlBase, subscriptionId, resourceGroupName string) string {
	if subscriptionId == "" || resourceGroupName == "" {
		return ""
	}
	return output.WithLinkFormat(fmt.Sprintf(
		"%s/#@/resource/subscriptions/%s/resourceGroups/%s/overview",
		portalUrlBase,
		subscriptionId,
		resourceGroupName))
}

func getTargetServiceName(
	ctx context.Context,
	projectManager project.ProjectManager,
	importManager *project.ImportManager,
	projectConfig *project.ProjectConfig,
	commandName string,
	targetServiceName string,
	allFlagValue bool,
) (string, error) {
	if allFlagValue && targetServiceName != "" {
		return "", fmt.Errorf("cannot specify both --all and <service>")
	}

	if !allFlagValue && targetServiceName == "" {
		targetService, err := projectManager.DefaultServiceFromWd(ctx, projectConfig)
		if errors.Is(err, project.ErrNoDefaultService) {
			return "", fmt.Errorf(
				"current working directory is not a project or service directory. Specify a service name to %s a service, "+
					"or specify --all to %s all services",
				commandName,
				commandName,
			)
		} else if err != nil {
			return "", err
		}

		if targetService != nil {
			targetServiceName = targetService.Name
		}
	}

	if targetServiceName != "" {
		if has, err := importManager.HasService(ctx, projectConfig, targetServiceName); err != nil {
			return "", err
		} else if !has {
			return "", fmt.Errorf("service name '%s' doesn't exist", targetServiceName)
		}
	}

	return targetServiceName, nil
}

// Calculate the total time since t, excluding user interaction time.
func since(t time.Time) time.Duration {
	userInteractTime := tracing.InteractTimeMs.Load()
	return time.Since(t) - time.Duration(userInteractTime)*time.Millisecond
}
