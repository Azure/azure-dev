// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
)

const (
	projectReconcileSchemaVersion  = 1
	projectReconcileMaxRequestSize = 1024 * 1024
)

// projectReconcileRequest is the versioned projects handoff.
// Omitted NoPrompt defaults to true for deterministic agents.
type projectReconcileRequest struct {
	SchemaVersion     int                          `json:"schemaVersion"`
	Environment       string                       `json:"environment,omitempty"`
	Project           projectReconcileProject      `json:"project"`
	Deployments       []projectReconcileDeployment `json:"deployments,omitempty"`
	DefaultDeployment string                       `json:"defaultDeployment,omitempty"`
	Infra             string                       `json:"infra,omitempty"`
	Force             bool                         `json:"force,omitempty"`
	NoPrompt          *bool                        `json:"noPrompt,omitempty"`
}

type projectReconcileProject struct {
	ProjectID       string `json:"projectId,omitempty"`
	ProjectEndpoint string `json:"projectEndpoint,omitempty"`
}

type projectReconcileDeployment struct {
	Name     string                    `json:"name"`
	Model    synthesis.DeploymentModel `json:"model"`
	Sku      synthesis.DeploymentSku   `json:"sku"`
	Location string                    `json:"location,omitempty"`
}

func newProjectInternalCommand(
	extCtx *azdext.ExtensionContext,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:    "internal",
		Hidden: true,
		Args:   cobra.NoArgs,
	}
	cmd.AddCommand(newProjectReconcileCommand(extCtx))
	return cmd
}

func newProjectReconcileCommand(
	extCtx *azdext.ExtensionContext,
) *cobra.Command {
	var requestFile string
	cmd := &cobra.Command{
		Use:    "reconcile --request-file <path>",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			request, err := loadProjectReconcileRequest(requestFile)
			if err != nil {
				return err
			}
			return (&projectReconcileAction{
				request: request,
				extCtx:  extCtx,
			}).Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(
		&requestFile,
		"request-file",
		"",
		"Path to a projects reconciliation request",
	)
	return cmd
}

type projectReconcileAction struct {
	client  *azdext.AzdClient
	request *projectReconcileRequest
	extCtx  *azdext.ExtensionContext
}

func (a *projectReconcileAction) Run(ctx context.Context) error {
	if a.request == nil {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"the project reconciliation request is missing",
			"provide a valid reconciliation request file",
		)
	}
	if err := validateProjectReconcileRequest(a.request); err != nil {
		return err
	}

	client := a.client
	var err error
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

	extCtx := &azdext.ExtensionContext{
		Environment: a.request.Environment,
		NoPrompt:    a.request.noPrompt(),
	}
	if a.extCtx != nil {
		if extCtx.Environment == "" {
			extCtx.Environment = a.extCtx.Environment
		}
		extCtx.NoPrompt = extCtx.NoPrompt || a.extCtx.NoPrompt
	}

	projectAction := &ProjectAddAction{
		client: client,
		flags: &projectAddFlags{
			projectID:       a.request.Project.ProjectID,
			projectEndpoint: a.request.Project.ProjectEndpoint,
			infra:           a.request.Infra,
			force:           a.request.Force,
			forceSet:        a.request.Force,
			noPrompt:        extCtx.NoPrompt,
			output:          "none",
		},
		extCtx: extCtx,
	}
	if err := projectAction.Run(ctx); err != nil {
		return err
	}

	if len(a.request.Deployments) == 0 {
		return nil
	}
	return a.reconcileDeployments(ctx, client, extCtx)
}

func (a *projectReconcileAction) reconcileDeployments(
	ctx context.Context,
	client *azdext.AzdClient,
	extCtx *azdext.ExtensionContext,
) error {
	projectRoot := projectRootPath()
	project, _, err := ensureProjectWithEnvironment(
		ctx, client, projectRoot, extCtx.Environment,
	)
	if err != nil {
		return err
	}
	if project.GetPath() != "" {
		projectRoot = project.GetPath()
	}
	envName, err := resolveProjectEnvironmentName(
		ctx, client, extCtx.Environment, projectRoot,
	)
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
	if service == nil || service.Legacy {
		return exterrors.Dependency(
			"project_service_not_found",
			"project initialization did not create an azure.ai.project service",
			"run `azd ai project init` and retry the reconciliation",
		)
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
			"include projectId in the reconciliation request and retry",
		)
	}

	rollbacks := make([]func() error, 0, len(a.request.Deployments))
	for _, requested := range a.request.Deployments {
		_, rollback, err := reconcileDeploymentWithRollback(
			ctx,
			reconciler,
			service.Name,
			synthesis.Deployment{
				Name:  requested.Name,
				Model: requested.Model,
				Sku:   requested.Sku,
			},
			a.request.Force,
		)
		if err != nil {
			return rollbackProjectReconcile(err, rollbacks...)
		}
		if rollback != nil {
			rollbacks = append(rollbacks, rollback)
		}
	}

	environmentSets := projectReconcileEnvironmentSets(a.request)
	if len(environmentSets) == 0 {
		return nil
	}
	environmentPlan := environmentPlan{Sets: environmentSets}
	environmentRollback := func() error {
		return withProjectRollbackContext(ctx, func(rollbackCtx context.Context) error {
			return restoreProjectEnvironment(
				rollbackCtx,
				client,
				envName,
				values,
				environmentPlan,
			)
		})
	}
	for _, key := range slices.Sorted(maps.Keys(environmentSets)) {
		if environmentSets[key] == values[key] {
			continue
		}
		if _, err := client.Environment().SetValue(ctx, &azdext.SetEnvRequest{
			EnvName: envName,
			Key:     key,
			Value:   environmentSets[key],
		}); err != nil {
			rollbacks = append(rollbacks, environmentRollback)
			return rollbackProjectReconcile(
				fmt.Errorf("set project environment value %s: %w", key, err),
				rollbacks...,
			)
		}
	}
	return nil
}

func rollbackProjectReconcile(
	operationErr error,
	rollbacks ...func() error,
) error {
	var rollbackErrs []error
	for index := len(rollbacks) - 1; index >= 0; index-- {
		if err := rollbacks[index](); err != nil {
			rollbackErrs = append(
				rollbackErrs,
				fmt.Errorf("rollback project reconciliation: %w", err),
			)
		}
	}
	if len(rollbackErrs) == 0 {
		return operationErr
	}
	return errors.Join(append([]error{operationErr}, rollbackErrs...)...)
}

func projectReconcileEnvironmentSets(
	request *projectReconcileRequest,
) map[string]string {
	sets := map[string]string{}
	for _, deployment := range request.Deployments {
		if location := strings.TrimSpace(deployment.Location); location != "" {
			sets["AZURE_AI_DEPLOYMENTS_LOCATION"] = location
		}
	}
	if request.DefaultDeployment != "" {
		sets["AZURE_AI_MODEL_DEPLOYMENT_NAME"] = request.DefaultDeployment
	}
	return sets
}

func loadProjectReconcileRequest(
	requestFile string,
) (*projectReconcileRequest, error) {
	if strings.TrimSpace(requestFile) == "" {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"--request-file is required",
			"provide a JSON reconciliation request file",
		)
	}
	// #nosec G304 -- caller-owned request file.
	file, err := os.Open(requestFile)
	if err != nil {
		return nil, fmt.Errorf("open project reconciliation request: %w", err)
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(
		file,
		projectReconcileMaxRequestSize+1,
	))
	if err != nil {
		return nil, fmt.Errorf("read project reconciliation request: %w", err)
	}
	if len(raw) > projectReconcileMaxRequestSize {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"the project reconciliation request is too large",
			"keep the request file below 1 MiB and retry",
		)
	}
	var request projectReconcileRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("the project reconciliation request is invalid: %s", err),
			"provide a JSON request with schemaVersion 1",
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"the project reconciliation request contains trailing data",
			"provide exactly one JSON object and retry",
		)
	}
	if err := validateProjectReconcileRequest(&request); err != nil {
		return nil, err
	}
	return &request, nil
}

func validateProjectReconcileRequest(
	request *projectReconcileRequest,
) error {
	if request == nil {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			"the project reconciliation request is missing",
			"provide a valid reconciliation request",
		)
	}
	if request.SchemaVersion != projectReconcileSchemaVersion {
		return exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf(
				"unsupported project reconciliation schema version %d",
				request.SchemaVersion,
			),
			"provide a request with schemaVersion 1",
		)
	}
	request.Project.ProjectID = strings.TrimSpace(request.Project.ProjectID)
	request.Project.ProjectEndpoint = strings.TrimSpace(
		request.Project.ProjectEndpoint,
	)
	if request.Project.ProjectID != "" &&
		request.Project.ProjectEndpoint != "" {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"projectId and projectEndpoint are mutually exclusive",
			"provide only one project target",
		)
	}
	if request.Force &&
		request.Project.ProjectID == "" &&
		request.Project.ProjectEndpoint == "" {
		return exterrors.Validation(
			exterrors.CodeConflictingArguments,
			"force requires projectId or projectEndpoint",
			"provide an explicit project target or remove force",
		)
	}
	if request.Infra != "" {
		if _, err := parseInfraProvider(request.Infra); err != nil {
			return err
		}
		if len(request.Deployments) > 0 {
			return exterrors.Validation(
				exterrors.CodeConflictingArguments,
				"infra ejection cannot be combined with managed deployments",
				"initialize infrastructure first, then reconcile deployments",
			)
		}
	}

	names := make(map[string]struct{}, len(request.Deployments))
	for index := range request.Deployments {
		deployment := &request.Deployments[index]
		deployment.Name = strings.TrimSpace(deployment.Name)
		deployment.Model.Name = strings.TrimSpace(deployment.Model.Name)
		deployment.Model.Format = strings.TrimSpace(deployment.Model.Format)
		deployment.Model.Version = strings.TrimSpace(deployment.Model.Version)
		deployment.Sku.Name = strings.TrimSpace(deployment.Sku.Name)
		deployment.Location = strings.TrimSpace(deployment.Location)
		if deployment.Name == "" || deployment.Model.Name == "" ||
			deployment.Model.Format == "" || deployment.Model.Version == "" ||
			deployment.Sku.Name == "" || deployment.Sku.Capacity <= 0 {
			return exterrors.Validation(
				"project_deployment_invalid",
				fmt.Sprintf(
					"deployments[%d] is missing a required name, model, version, SKU, or capacity",
					index,
				),
				"provide a complete managed deployment tuple and retry",
			)
		}
		key := strings.ToLower(deployment.Name)
		if _, exists := names[key]; exists {
			return exterrors.Validation(
				"project_deployment_duplicate",
				fmt.Sprintf(
					"the reconciliation request contains duplicate deployment %q",
					deployment.Name,
				),
				"keep one declaration for each deployment name and retry",
			)
		}
		names[key] = struct{}{}
	}
	request.DefaultDeployment = strings.TrimSpace(request.DefaultDeployment)
	if request.DefaultDeployment != "" {
		if _, exists := names[strings.ToLower(request.DefaultDeployment)]; !exists {
			return exterrors.Validation(
				"project_deployment_default_invalid",
				fmt.Sprintf(
					"defaultDeployment %q is not in deployments",
					request.DefaultDeployment,
				),
				"set defaultDeployment to one of the managed deployments",
			)
		}
	}
	locations := map[string]struct{}{}
	for _, deployment := range request.Deployments {
		if deployment.Location != "" {
			locations[strings.ToLower(deployment.Location)] = struct{}{}
		}
	}
	if len(locations) > 1 {
		return exterrors.Validation(
			"project_deployment_location_conflict",
			"managed deployments in one reconciliation request use different locations",
			"submit deployments with one shared AZURE_AI_DEPLOYMENTS_LOCATION",
		)
	}
	return nil
}

func (r *projectReconcileRequest) noPrompt() bool {
	return r.NoPrompt == nil || *r.NoPrompt
}
