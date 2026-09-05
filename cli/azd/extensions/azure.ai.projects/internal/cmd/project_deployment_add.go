// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
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
		Short: "Add an azd-managed model deployment before ejection.",
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
	project, _, err := ensureProjectWithEnvironment(
		ctx,
		client,
		projectRoot,
		a.environmentName(),
	)
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
	service, projectConfig, err := reconciler.discoverProjectService(ctx)
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
		service, projectConfig, err = reconciler.discoverProjectService(ctx)
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
	if ejected, err := findEjectedFoundryProjectInfrastructure(
		projectConfig,
	); err != nil {
		return err
	} else if ejected != nil {
		return projectDeploymentEjectedInfraError(ejected.parameterFile)
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
		ctx, client, values, a.flags.location, a.noPrompt(),
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
	environmentWriteAttempted := map[string]bool{}
	restoreEnvironment := func() error {
		return withProjectRollbackContext(ctx, func(rollbackCtx context.Context) error {
			keys := make([]string, 0, len(environmentWriteAttempted))
			for key := range environmentWriteAttempted {
				keys = append(keys, key)
			}
			slices.Sort(keys)
			var restoreErrs []error
			for _, key := range keys {
				if _, err := client.Environment().SetValue(rollbackCtx, &azdext.SetEnvRequest{
					EnvName: envName,
					Key:     key,
					Value:   values[key],
				}); err != nil {
					restoreErrs = append(
						restoreErrs,
						fmt.Errorf("restore environment value %s: %w", key, err),
					)
				}
			}
			return errors.Join(restoreErrs...)
		})
	}
	setEnvironmentValue := func(key, value string) error {
		if strings.TrimSpace(value) == "" || value == values[key] {
			return nil
		}
		environmentWriteAttempted[key] = true
		if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     key,
			Value:   value,
		}); err != nil {
			return err
		}
		return nil
	}
	contextValues := deploymentAzureContextValues(values, azureContext)
	contextKeys := make([]string, 0, len(contextValues))
	for key := range contextValues {
		contextKeys = append(contextKeys, key)
	}
	slices.Sort(contextKeys)
	for _, key := range contextKeys {
		if err := setEnvironmentValue(key, contextValues[key]); err != nil {
			return rollbackProjectDeploymentAdd(
				fmt.Errorf("set project environment value %s: %w", key, err),
				restoreEnvironment,
			)
		}
	}
	mutation, restoreDeployment, err := reconcileDeploymentWithRollback(
		ctx, reconciler, service.Name, selected.Deployment, force,
	)
	if err != nil {
		return rollbackProjectDeploymentAdd(err, restoreEnvironment)
	}
	if selected.Location != "" {
		if err := setEnvironmentValue(
			"AZURE_AI_DEPLOYMENTS_LOCATION",
			selected.Location,
		); err != nil {
			return rollbackProjectDeploymentAdd(
				fmt.Errorf("set deployment location: %w", err),
				restoreEnvironment,
				restoreDeployment,
			)
		}
	}
	if setAsDefault {
		if err := setEnvironmentValue(
			"AZURE_AI_MODEL_DEPLOYMENT_NAME",
			selected.Deployment.Name,
		); err != nil {
			return rollbackProjectDeploymentAdd(
				fmt.Errorf("set default model deployment: %w", err),
				restoreEnvironment,
				restoreDeployment,
			)
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

func deploymentAzureContextValues(
	oldValues map[string]string,
	azureContext *azdext.AzureContext,
) map[string]string {
	if azureContext == nil || azureContext.Scope == nil {
		return nil
	}
	updates := map[string]string{}
	for key, value := range map[string]string{
		"AZURE_SUBSCRIPTION_ID": azureContext.Scope.SubscriptionId,
		"AZURE_TENANT_ID":       azureContext.Scope.TenantId,
		"AZURE_LOCATION":        azureContext.Scope.Location,
	} {
		value = strings.TrimSpace(value)
		if value != "" && strings.TrimSpace(oldValues[key]) == "" {
			updates[key] = value
		}
	}
	return updates
}

func rollbackProjectDeploymentAdd(
	operationErr error,
	rollbacks ...func() error,
) error {
	var rollbackErrs []error
	for _, rollback := range rollbacks {
		if rollback == nil {
			continue
		}
		if err := rollback(); err != nil {
			rollbackErrs = append(
				rollbackErrs,
				fmt.Errorf("rollback project deployment add: %w", err),
			)
		}
	}
	if len(rollbackErrs) == 0 {
		return operationErr
	}
	return errors.Join(append([]error{operationErr}, rollbackErrs...)...)
}

func resolveDeploymentAzureContext(
	ctx context.Context,
	client *azdext.AzdClient,
	values map[string]string,
	explicitLocation string,
	noPrompt bool,
) (*azdext.AzureContext, error) {
	location := firstNonEmpty(
		strings.TrimSpace(explicitLocation),
		strings.TrimSpace(values["AZURE_AI_DEPLOYMENTS_LOCATION"]),
		strings.TrimSpace(values["AZURE_LOCATION"]),
	)
	azureContext := &azdext.AzureContext{
		Scope: &azdext.AzureScope{
			TenantId:       strings.TrimSpace(values["AZURE_TENANT_ID"]),
			SubscriptionId: strings.TrimSpace(values["AZURE_SUBSCRIPTION_ID"]),
			Location:       location,
		},
	}
	if azureContext.Scope.SubscriptionId == "" {
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
	} else if azureContext.Scope.Location == "" &&
		!noPrompt &&
		azureContext.Scope.TenantId == "" {
		subscriptionID, userTenantID, err := resolveInteractiveSubscription(
			ctx, client, values,
		)
		if err != nil {
			return nil, err
		}
		azureContext.Scope.SubscriptionId = subscriptionID
		azureContext.Scope.TenantId = userTenantID
	}
	if azureContext.Scope.Location == "" {
		if noPrompt {
			return nil, missingDeploymentLocationError()
		}
		response, promptErr := client.Prompt().PromptLocation(
			ctx,
			&azdext.PromptLocationRequest{AzureContext: azureContext},
		)
		if promptErr != nil {
			if exterrors.IsCancellation(promptErr) {
				return nil, exterrors.Cancelled(
					"Azure location selection was cancelled",
				)
			}
			return nil, fmt.Errorf("select Azure location: %w", promptErr)
		}
		if response.GetLocation() == nil {
			return nil, missingDeploymentLocationError()
		}
		azureContext.Scope.Location = strings.TrimSpace(
			response.Location.GetName(),
		)
		if azureContext.Scope.Location == "" {
			return nil, missingDeploymentLocationError()
		}
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
