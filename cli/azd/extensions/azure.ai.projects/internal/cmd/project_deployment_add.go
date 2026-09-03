// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"azure.ai.projects/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

type projectDeploymentFlags struct {
	model    string
	name     string
	version  string
	sku      string
	capacity int32
	location string
	force    bool
	output   string
}

// ProjectDeploymentAddAction implements deployment add.
type ProjectDeploymentAddAction struct {
	client *azdext.AzdClient
	flags  *projectDeploymentFlags
	extCtx *azdext.ExtensionContext
}

func newProjectDeploymentCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	cmd := &cobra.Command{
		Use:   "deployment",
		Short: "Manage managed model deployments for a Foundry project.",
	}
	cmd.AddCommand(newProjectDeploymentAddCommand(extCtx))
	return cmd
}

func newProjectDeploymentAddCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &projectDeploymentFlags{}
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Add an azd-managed model deployment to the project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.output = extCtx.OutputFormat
			action := &ProjectDeploymentAddAction{flags: flags, extCtx: extCtx}
			return action.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&flags.model, "model", "", "Model name or publisher/model")
	cmd.Flags().StringVar(&flags.name, "name", "", "Deployment name")
	cmd.Flags().StringVar(&flags.version, "version", "", "Model version")
	cmd.Flags().StringVar(&flags.sku, "sku", "", "Deployment SKU name")
	cmd.Flags().Int32Var(&flags.capacity, "capacity", 0, "Deployment capacity")
	cmd.Flags().StringVar(&flags.location, "location", "", "Deployment location")
	cmd.Flags().BoolVar(&flags.force, "force", false, "Replace a conflicting inline declaration")
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default", "json", "none"},
		Default:       "default",
		Usage:         "The output format",
	})
	return cmd
}

func (a *ProjectDeploymentAddAction) Run(ctx context.Context) error {
	if a.flags == nil {
		a.flags = &projectDeploymentFlags{}
	}
	if strings.TrimSpace(a.flags.model) == "" {
		if a.noPrompt() {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"--model is required in --no-prompt mode",
				"provide a model with --model and retry",
			)
		}
	}
	var err error
	client := a.client
	if client == nil {
		client, err = azdext.NewAzdClient()
		if err != nil {
			return exterrors.Dependency(
				exterrors.CodeAzdClientFailed,
				"could not connect to the azd daemon",
				"run this command from an azd extension host",
			)
		}
		defer client.Close()
	}
	a.client = client
	if strings.TrimSpace(a.flags.model) == "" {
		response, promptErr := client.Prompt().Prompt(ctx, &azdext.PromptRequest{
			Options: &azdext.PromptOptions{
				Message:        "Model name",
				IgnoreHintKeys: true,
			},
		})
		if promptErr != nil {
			return fmt.Errorf("select model: %w", promptErr)
		}
		a.flags.model = response.GetValue()
		if strings.TrimSpace(a.flags.model) == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidParameter,
				"model name cannot be empty",
				"provide a model name and retry",
			)
		}
	}

	projectRoot := projectRootPath()
	project, _, err := ensureProject(ctx, client, projectRoot)
	if err != nil {
		return err
	}
	if project.GetPath() != "" {
		projectRoot = project.GetPath()
	}
	envName, err := resolveProjectEnvironmentName(ctx, client, a.environmentName(), projectRoot)
	if err != nil {
		return err
	}
	values, err := currentProjectEnvironment(ctx, client, envName)
	if err != nil {
		return err
	}
	reconciler := &projectServiceReconciler{
		client:            client,
		projectRoot:       projectRoot,
		environmentValues: values,
	}
	service, _, err := reconciler.discoverProjectService(ctx)
	if err != nil {
		return err
	}
	if service == nil {
		projectAction := &ProjectAddAction{
			client: client,
			flags: &projectAddFlags{
				noPrompt: a.noPrompt(),
				output:   "none",
			},
			extCtx: a.extCtx,
		}
		if err := projectAction.Run(ctx); err != nil {
			return err
		}

		envName, err = resolveProjectEnvironmentName(
			ctx, client, a.environmentName(), projectRoot,
		)
		if err != nil {
			return err
		}
		values, err = currentProjectEnvironment(ctx, client, envName)
		if err != nil {
			return err
		}
		reconciler = &projectServiceReconciler{
			client:            client,
			projectRoot:       projectRoot,
			environmentValues: values,
		}
		service, _, err = reconciler.discoverProjectService(ctx)
		if err != nil {
			return err
		}
		if service == nil {
			return exterrors.Dependency(
				"project_service_not_found",
				"project add completed without creating an azure.ai.project service",
				"run `azd ai project add` and retry adding the deployment",
			)
		}
	}
	if err := validateConfiguredProjectIdentity(values, service); err != nil {
		return err
	}
	if requiresExistingProjectID(values, service) {
		return exterrors.Validation(
			"project_deployment_requires_id",
			"managed model deployments for an existing Foundry project "+
				"require a resource ID",
			"rerun `azd ai project add --project-id <resource-id>` "+
				"before adding managed deployments",
		)
	}
	azureContext, err := resolveDeploymentAzureContext(
		ctx, client, values, a.noPrompt(),
	)
	if err != nil {
		return err
	}
	model := modelSelection{
		Name:           a.flags.model,
		DeploymentName: a.flags.name,
	}
	force := a.flags.force
	setAsDefault := true
	selection := deploymentSelectionOptions{
		Version:  a.flags.version,
		SKU:      a.flags.sku,
		Capacity: a.flags.capacity,
		Location: a.flags.location,
	}
	selected, err := selectModelDeployment(
		ctx, client, azureContext, model, selection, a.noPrompt(),
	)
	if err != nil {
		return err
	}
	if model.DeploymentName != "" {
		selected.Deployment.Name = chooseDeploymentName(
			model.DeploymentName,
			selected.Deployment.Name,
		)
	}
	if selected.Deployment.Model.Format == "" ||
		selected.Deployment.Model.Name == "" ||
		selected.Deployment.Model.Version == "" ||
		selected.Deployment.Sku.Name == "" ||
		selected.Deployment.Sku.Capacity <= 0 {
		return exterrors.Validation(
			"model_deployment_invalid",
			"the selected model deployment is missing a required version, SKU, or capacity",
			"specify a deployable model tuple and retry",
		)
	}
	mutation, err := reconcileDeployment(
		ctx, reconciler, service.Name, selected.Deployment, force,
	)
	if err != nil {
		return err
	}
	if selected.Location != "" && selected.Location != values["AZURE_AI_DEPLOYMENTS_LOCATION"] {
		if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     "AZURE_AI_DEPLOYMENTS_LOCATION",
			Value:   selected.Location,
		}); err != nil {
			return fmt.Errorf("set deployment location: %w", err)
		}
	}
	if setAsDefault {
		if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     "AZURE_AI_MODEL_DEPLOYMENT_NAME",
			Value:   selected.Deployment.Name,
		}); err != nil {
			return fmt.Errorf("set default model deployment: %w", err)
		}
	}
	result := projectDeploymentAddOutput{
		SchemaVersion:   projectOutputSchemaVersion,
		ProducerVersion: projectOutputProducerVersion(),
		ServiceName:     service.Name,
		DeploymentName:  selected.Deployment.Name,
		Model: deploymentOutputModel{
			Format:  selected.Deployment.Model.Format,
			Name:    selected.Deployment.Model.Name,
			Version: selected.Deployment.Model.Version,
		},
		SKU: deploymentOutputSKU{
			Name:     selected.Deployment.Sku.Name,
			Capacity: selected.Deployment.Sku.Capacity,
		},
		Mutation: string(mutation),
	}
	if a.flags.output == "none" {
		return nil
	}
	if a.flags.output == "json" {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	switch mutation {
	case deploymentUnchanged:
		fmt.Printf("Managed deployment %q is unchanged.\n", selected.Deployment.Name)
	default:
		fmt.Printf("Managed deployment %q %s.\n", selected.Deployment.Name, mutation)
	}
	return nil
}

func resolveDeploymentAzureContext(
	ctx context.Context,
	client *azdext.AzdClient,
	values map[string]string,
	noPrompt bool,
) (*azdext.AzureContext, error) {
	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       values["AZURE_TENANT_ID"],
			SubscriptionId: values["AZURE_SUBSCRIPTION_ID"],
			Location:       values["AZURE_AI_DEPLOYMENTS_LOCATION"],
		},
	}
	if azureContext.Scope.Location == "" {
		azureContext.Scope.Location = values["AZURE_LOCATION"]
	}
	if azureContext.Scope.SubscriptionId != "" {
		return azureContext, nil
	}
	if noPrompt {
		return nil, exterrors.Dependency(
			exterrors.CodeMissingAzureSubscription,
			"an Azure subscription is required to resolve model deployments",
			"set AZURE_SUBSCRIPTION_ID in the active azd environment and retry",
		)
	}
	subscriptionID, userTenantID, err := resolveInteractiveSubscription(
		ctx, client, values,
	)
	if err != nil {
		return nil, err
	}
	azureContext.Scope.SubscriptionId = subscriptionID
	if userTenantID != "" {
		azureContext.Scope.TenantId = userTenantID
	}
	return azureContext, nil
}

func fillEmptyAzureScope(dst, src *azdext.AzureContext) {
	if dst == nil || src == nil || src.Scope == nil {
		return
	}
	if dst.Scope == nil {
		dst.Scope = &azdext.AzureScope{}
	}
	if dst.Scope.TenantId == "" {
		dst.Scope.TenantId = src.Scope.TenantId
	}
	if dst.Scope.SubscriptionId == "" {
		dst.Scope.SubscriptionId = src.Scope.SubscriptionId
	}
	if dst.Scope.Location == "" {
		dst.Scope.Location = src.Scope.Location
	}
	if dst.Scope.ResourceGroup == "" {
		dst.Scope.ResourceGroup = src.Scope.ResourceGroup
	}
}

func validateConfiguredProjectIdentity(
	values map[string]string,
	service *projectServiceInfo,
) error {
	projectID := strings.TrimSpace(values["AZURE_AI_PROJECT_ID"])
	if projectID == "" {
		return nil
	}
	project, err := projectFromResourceID(projectID)
	if err != nil {
		return err
	}
	if service == nil {
		return nil
	}
	endpoint := serviceEndpoint(service.Resolved)
	if endpoint == "" || equalProjectEndpoint(endpoint, project.Endpoint) {
		return nil
	}
	return exterrors.Validation(
		"project_target_mismatch",
		"the configured project endpoint and AZURE_AI_PROJECT_ID identify different projects",
		"rerun `azd ai project add` with the intended project target",
	)
}

func requiresExistingProjectID(
	values map[string]string,
	service *projectServiceInfo,
) bool {
	if strings.TrimSpace(values["AZURE_AI_PROJECT_ID"]) != "" {
		return false
	}
	if strings.EqualFold(
		strings.TrimSpace(values["USE_EXISTING_AI_PROJECT"]),
		"true",
	) {
		return true
	}
	return service != nil && serviceEndpoint(service.Resolved) != ""
}

func (a *ProjectDeploymentAddAction) noPrompt() bool {
	return a.extCtx != nil && a.extCtx.NoPrompt
}

func (a *ProjectDeploymentAddAction) environmentName() string {
	if a.extCtx != nil {
		return a.extCtx.Environment
	}
	return ""
}
