// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/project"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const projectWorkflowRollbackTimeout = 30 * time.Second

type projectAuthoringMode string

const (
	projectAuthoringCurrent  projectAuthoringMode = "current"
	projectAuthoringExisting projectAuthoringMode = "existing"
	projectAuthoringNew      projectAuthoringMode = "new"
)

// authorFoundryProject delegates project-service authoring to the
// projects extension. Agents select the target and wire the
// resulting service.
func authorFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	target *FoundryProjectInfo,
	projectRoot string,
	mode projectAuthoringMode,
	noPrompt bool,
) error {
	if mode != projectAuthoringCurrent &&
		mode != projectAuthoringExisting &&
		mode != projectAuthoringNew {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unknown Foundry project authoring mode %q", mode),
			"retry the agent initialization command",
		)
	}
	if mode == projectAuthoringExisting && target == nil {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"an existing Foundry project target is required",
			"select an existing Foundry project and retry",
		)
	}
	if mode != projectAuthoringExisting && target != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"an existing Foundry project target cannot be used in this mode",
			"select an existing project or create a new one",
		)
	}
	args := []string{
		"ai",
		"project",
		"add",
	}
	if noPrompt {
		args = append(args, "--no-prompt")
	}
	args = append(args, "--output", "none")
	if mode == projectAuthoringNew {
		args = append(args, "--new-project")
	}
	if mode == projectAuthoringExisting {
		if resourceID := strings.TrimSpace(target.ResourceId); resourceID != "" {
			args = append(args, "--project-id", resourceID, "--force")
		} else if endpoint := strings.TrimSpace(target.Endpoint()); endpoint != "" {
			args = append(args, "--project-endpoint", endpoint, "--force")
		}
	}
	return runProjectWorkflow(
		ctx,
		azdClient,
		args,
		projectRoot,
		"authoring the Foundry project",
		exterrors.CodeProjectAuthoringFailed,
	)
}

func authorSelectedFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	target *FoundryProjectInfo,
	projectRoot string,
	mode projectAuthoringMode,
	noPrompt bool,
) error {
	if mode == projectAuthoringNew || mode == projectAuthoringCurrent {
		return authorFoundryProjectPreservingEnvironment(
			ctx, azdClient, envName, projectRoot, mode, noPrompt,
		)
	}
	return authorFoundryProject(ctx, azdClient, target, projectRoot, mode, noPrompt)
}

var newProjectEnvironmentKeys = []string{
	"AZURE_AI_PROJECT_NAME",
	"AZURE_RESOURCE_GROUP",
	"AZURE_AI_ACCOUNT_NAME",
	"AZURE_LOCATION",
	"AZURE_AI_DEPLOYMENTS_LOCATION",
}

func authorNewFoundryProject(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	projectRoot string,
) error {
	return authorFoundryProjectPreservingEnvironment(
		ctx, azdClient, envName, projectRoot, projectAuthoringNew, true,
	)
}

func authorFoundryProjectPreservingEnvironment(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	projectRoot string,
	mode projectAuthoringMode,
	noPrompt bool,
) error {
	response, err := azdClient.Environment().GetValues(
		ctx,
		&azdext.GetEnvironmentRequest{Name: envName},
	)
	if err != nil {
		return fmt.Errorf("reading new Foundry project environment: %w", err)
	}

	values := make(map[string]string, len(newProjectEnvironmentKeys))
	for _, key := range newProjectEnvironmentKeys {
		for _, value := range response.GetKeyValues() {
			if value.GetKey() == key {
				values[key] = value.GetValue()
				break
			}
		}
	}

	authorErr := authorFoundryProject(
		ctx, azdClient, nil, projectRoot, mode, noPrompt,
	)
	rollbackCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		projectWorkflowRollbackTimeout,
	)
	defer cancel()
	var restoreErrs []error
	for _, key := range newProjectEnvironmentKeys {
		value := strings.TrimSpace(values[key])
		if value == "" {
			continue
		}
		if err := setEnvValue(rollbackCtx, azdClient, envName, key, value); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
	}
	if authorErr != nil {
		restoreErrs = append([]error{authorErr}, restoreErrs...)
	}
	return errors.Join(restoreErrs...)
}

// authorFoundryDeployments delegates managed deployment authoring to
// the projects extension. Existing deployments are not passed here.
func authorFoundryDeployments(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectRoot string,
	deployments []project.Deployment,
) error {
	for _, deployment := range deployments {
		model := deployment.Model.Name
		if format := strings.TrimSpace(deployment.Model.Format); format != "" {
			model = format + "/" + model
		}
		args := []string{
			"ai",
			"project",
			"deployment",
			"add",
			"--no-prompt",
			"--output",
			"none",
			"--model",
			model,
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
			projectRoot,
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
	projectRoot string,
	deployments []project.Deployment,
) error {
	if len(deployments) < 2 {
		return authorFoundryDeployments(
			ctx, azdClient, projectRoot, deployments,
		)
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
		rollbackCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			projectWorkflowRollbackTimeout,
		)
		defer cancel()
		return setEnvValue(
			rollbackCtx,
			azdClient,
			envName,
			"AZURE_AI_MODEL_DEPLOYMENT_NAME",
			defaultName,
		)
	}
	if err := authorFoundryDeployments(
		ctx, azdClient, projectRoot, deployments,
	); err != nil {
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
	projectRoot string,
	operation string,
	errorCode string,
) error {
	projectRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return exterrors.Dependency(
			errorCode,
			fmt.Sprintf("%s failed: resolving the project directory: %s", operation, err),
			"retry the command from the azd project directory",
		)
	}
	args = append(slices.Clone(args), "--cwd", projectRoot)
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
