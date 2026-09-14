// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// authorFoundryProject delegates project-service authoring to the
// projects extension. Agents select the target and wire the
// resulting service.
func authorFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	target *FoundryProjectInfo,
) error {
	args := []string{
		"ai",
		"project",
		"add",
		"--no-prompt",
		"--output",
		"none",
	}
	if target != nil {
		if resourceID := strings.TrimSpace(target.ResourceId); resourceID != "" {
			args = append(args, "--project-id", resourceID)
		} else if endpoint := strings.TrimSpace(target.Endpoint()); endpoint != "" {
			args = append(args, "--project-endpoint", endpoint)
		}
	}
	return runProjectWorkflow(
		ctx,
		azdClient,
		args,
		"authoring the Foundry project",
		exterrors.CodeProjectAuthoringFailed,
	)
}

// authorFoundryDeployments delegates managed deployment authoring to
// the projects extension. Existing deployments are not passed here.
func authorFoundryDeployments(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	deployments []project.Deployment,
) error {
	for _, deployment := range deployments {
		args := []string{
			"ai",
			"project",
			"deployment",
			"add",
			"--no-prompt",
			"--output",
			"none",
			"--model",
			deployment.Model.Name,
		}
		if deployment.Name != "" {
			args = append(args, "--name", deployment.Name)
		}
		if deployment.Model.Version != "" {
			args = append(args, "--version", deployment.Model.Version)
		}
		if deployment.Sku.Name != "" {
			args = append(args, "--sku", deployment.Sku.Name)
		}
		if deployment.Sku.Capacity > 0 {
			args = append(
				args,
				"--capacity",
				strconv.Itoa(deployment.Sku.Capacity),
			)
		}
		if err := runProjectWorkflow(
			ctx,
			azdClient,
			args,
			fmt.Sprintf("authoring model deployment %q", deployment.Name),
			exterrors.CodeDeploymentAuthoringFailed,
		); err != nil {
			return err
		}
	}
	return nil
}

func authorFoundryDeploymentsPreservingDefault(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	deployments []project.Deployment,
) error {
	if len(deployments) < 2 {
		return authorFoundryDeployments(ctx, azdClient, deployments)
	}
	defaultName, err := getEnvValue(
		ctx,
		azdClient,
		envName,
		"AZURE_AI_MODEL_DEPLOYMENT_NAME",
	)
	if err != nil {
		return err
	}
	defaultName = strings.TrimSpace(defaultName)
	restoreDefault := func() error {
		if defaultName == "" {
			return nil
		}
		return setEnvValue(
			ctx,
			azdClient,
			envName,
			"AZURE_AI_MODEL_DEPLOYMENT_NAME",
			defaultName,
		)
	}
	if err := authorFoundryDeployments(ctx, azdClient, deployments); err != nil {
		if restoreErr := restoreDefault(); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	return restoreDefault()
}

func runProjectWorkflow(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	args []string,
	operation string,
	errorCode string,
) error {
	workflow := &azdext.Workflow{
		Name: operation,
		Steps: []*azdext.WorkflowStep{
			{Command: &azdext.WorkflowCommand{Args: args}},
		},
	}
	if _, err := azdClient.Workflow().Run(
		ctx,
		&azdext.RunWorkflowRequest{Workflow: workflow},
	); err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled(operation + " was cancelled")
		}
		return exterrors.Dependency(
			errorCode,
			fmt.Sprintf("%s failed: %s", operation, err),
			"retry the command after resolving the projects extension error",
		)
	}
	return nil
}
