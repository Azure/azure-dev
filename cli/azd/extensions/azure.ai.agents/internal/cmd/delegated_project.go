// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"azureaiagent/internal/project"
	"azureaiagent/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

const delegatedProjectSchemaVersion = 2

const delegatedProjectSource = "azure.ai.agents/init"

type delegatedProjectTarget struct {
	ResourceID string `json:"resourceId,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	Name       string `json:"name,omitempty"`
}

type delegatedProjectInfra struct {
	EjectProvider string `json:"ejectProvider,omitempty"`
}

type delegatedProjectRequirements struct {
	AllowedLocations []string `json:"allowedLocations,omitempty"`
}

type delegatedProjectDeployment struct {
	Name  string                          `json:"name"`
	Model delegatedProjectDeploymentModel `json:"model"`
	SKU   delegatedProjectDeploymentSKU   `json:"sku"`
}

type delegatedProjectDeploymentModel struct {
	Format  string `json:"format"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type delegatedProjectDeploymentSKU struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
}

type delegatedProjectConstraints struct {
	Requirements         delegatedProjectRequirements
	RequiredCapabilities []string
	AllowedLocations     []string
	ExcludedModelNames   []string
}

func delegatedProjectConstraintsForLocation(
	location string,
) delegatedProjectConstraints {
	location = strings.TrimSpace(location)
	constraints := delegatedProjectConstraints{
		RequiredCapabilities: []string{agentsV2ModelCapability},
	}
	if location != "" {
		constraints.Requirements.AllowedLocations = []string{location}
		constraints.AllowedLocations = []string{location}
	}
	return constraints
}

func delegatedProjectConstraintsForContext(
	azureContext *azdext.AzureContext,
) delegatedProjectConstraints {
	if azureContext == nil || azureContext.Scope == nil {
		return delegatedProjectConstraintsForLocation("")
	}
	return delegatedProjectConstraintsForLocation(azureContext.Scope.Location)
}

type delegatedProjectInitRequest struct {
	SchemaVersion       int                          `json:"schemaVersion"`
	Source              string                       `json:"source"`
	SourceVersion       string                       `json:"sourceVersion"`
	Project             delegatedProjectTarget       `json:"project"`
	Infra               delegatedProjectInfra        `json:"infra"`
	Requirements        delegatedProjectRequirements `json:"requirements"`
	ResolveAzureContext bool                         `json:"resolveAzureContext"`
	Force               bool                         `json:"force"`
	ReplaceDeployments  bool                         `json:"replaceDeployments,omitempty"`
	Deployments         []delegatedProjectDeployment `json:"deployments,omitempty"`
}

type delegatedProjectModel struct {
	Name                 string   `json:"name"`
	DeploymentName       string   `json:"deploymentName,omitempty"`
	Format               string   `json:"format,omitempty"`
	Version              string   `json:"version,omitempty"`
	SKU                  string   `json:"sku,omitempty"`
	Capacity             int      `json:"capacity,omitempty"`
	AllowedLocations     []string `json:"allowedLocations,omitempty"`
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty"`
	ExcludedModelNames   []string `json:"excludedModelNames,omitempty"`
}

type delegatedProjectDeploymentRequest struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Source        string                `json:"source"`
	SourceVersion string                `json:"sourceVersion"`
	Model         delegatedProjectModel `json:"model"`
	SetAsDefault  bool                  `json:"setAsDefault"`
	Force         bool                  `json:"force"`
}

func delegateFoundryProjectResources(
	ctx context.Context,
	client *azdext.AzdClient,
	projectName string,
	projectResourceID string,
	projectEndpoint string,
	deployments []project.Deployment,
	constraints ...delegatedProjectConstraints,
) (string, error) {
	var delegation delegatedProjectConstraints
	if len(constraints) > 0 {
		delegation = constraints[0]
	}
	serviceName, err := delegateFoundryProjectInit(
		ctx, client, projectName, projectResourceID, projectEndpoint,
		delegation.Requirements,
	)
	if err != nil {
		return "", err
	}

	for i := range deployments {
		deployment := deployments[i]
		if deployment.Sku.Capacity < 0 ||
			deployment.Sku.Capacity > math.MaxInt32 {
			return "", fmt.Errorf(
				"deployment %q capacity %d is outside the delegated range",
				deployment.Name, deployment.Sku.Capacity,
			)
		}
		modelName := deployment.Model.Name
		if deployment.Model.Format != "" {
			modelName = deployment.Model.Format + "/" + modelName
		}
		requiredCapabilities := slices.Clone(delegation.RequiredCapabilities)
		if len(requiredCapabilities) == 0 {
			requiredCapabilities = []string{agentsV2ModelCapability}
		}
		allowedLocations := slices.Clone(delegation.AllowedLocations)
		if len(allowedLocations) == 0 {
			allowedLocations = slices.Clone(
				delegation.Requirements.AllowedLocations,
			)
		}
		request := delegatedProjectDeploymentRequest{
			SchemaVersion: delegatedProjectSchemaVersion,
			Source:        delegatedProjectSource,
			SourceVersion: version.Version,
			Model: delegatedProjectModel{
				Name:                 modelName,
				DeploymentName:       deployment.Name,
				Format:               deployment.Model.Format,
				Version:              deployment.Model.Version,
				SKU:                  deployment.Sku.Name,
				Capacity:             deployment.Sku.Capacity,
				RequiredCapabilities: requiredCapabilities,
				AllowedLocations:     allowedLocations,
				ExcludedModelNames:   slices.Clone(delegation.ExcludedModelNames),
			},
			SetAsDefault: i == 0,
			Force:        true,
		}
		if err := runDelegatedProjectCommand(
			ctx, client, []string{"ai", "project", "deployment", "add", "--no-prompt"},
			request,
		); err != nil {
			return "", fmt.Errorf(
				"delegate Foundry deployment %q: %w", deployment.Name, err,
			)
		}
	}
	return serviceName, nil
}

func delegateFoundryProjectInit(
	ctx context.Context,
	client *azdext.AzdClient,
	projectName string,
	projectResourceID string,
	projectEndpoint string,
	requirements ...delegatedProjectRequirements,
) (string, error) {
	projectResourceID = strings.TrimSpace(projectResourceID)
	projectEndpoint = strings.TrimSpace(projectEndpoint)
	if projectResourceID != "" {
		projectEndpoint = ""
	}
	initRequest := newDelegatedProjectInitRequest(
		projectName, projectResourceID, projectEndpoint,
	)
	if len(requirements) > 0 {
		initRequest.Requirements = requirements[0]
	}
	if err := runDelegatedProjectCommand(
		ctx, client, []string{"ai", "project", "init", "--no-prompt"},
		initRequest,
	); err != nil {
		return "", fmt.Errorf("delegate Foundry project initialization: %w", err)
	}

	serviceName, err := delegatedProjectServiceName(ctx, client)
	if err != nil {
		return "", err
	}
	return serviceName, nil
}

func delegateFoundryProjectDeployments(
	ctx context.Context,
	client *azdext.AzdClient,
	projectName string,
	projectResourceID string,
	projectEndpoint string,
	deployments []project.Deployment,
) error {
	request := newDelegatedProjectInitRequest(
		projectName, projectResourceID, projectEndpoint,
	)
	request.ReplaceDeployments = true
	request.Deployments = make([]delegatedProjectDeployment, len(deployments))
	for i, deployment := range deployments {
		request.Deployments[i] = delegatedProjectDeployment{
			Name: deployment.Name,
			Model: delegatedProjectDeploymentModel{
				Format:  deployment.Model.Format,
				Name:    deployment.Model.Name,
				Version: deployment.Model.Version,
			},
			SKU: delegatedProjectDeploymentSKU{
				Name:     deployment.Sku.Name,
				Capacity: deployment.Sku.Capacity,
			},
		}
	}
	if err := runDelegatedProjectCommand(
		ctx, client, []string{"ai", "project", "init", "--no-prompt"},
		request,
	); err != nil {
		return fmt.Errorf("delegate Foundry deployment declarations: %w", err)
	}
	return nil
}

func newDelegatedProjectInitRequest(
	projectName string,
	projectResourceID string,
	projectEndpoint string,
) delegatedProjectInitRequest {
	projectResourceID = strings.TrimSpace(projectResourceID)
	projectEndpoint = strings.TrimSpace(projectEndpoint)
	if projectResourceID != "" {
		projectEndpoint = ""
	}
	return delegatedProjectInitRequest{
		SchemaVersion: delegatedProjectSchemaVersion,
		Source:        delegatedProjectSource,
		SourceVersion: version.Version,
		Project: delegatedProjectTarget{
			ResourceID: projectResourceID,
			Endpoint:   projectEndpoint,
			Name:       strings.TrimSpace(projectName),
		},
		ResolveAzureContext: true,
		Force:               true,
	}
}

func delegateFoundryInfra(
	ctx context.Context,
	provider string,
) error {
	client, err := azdext.NewAzdClient()
	if err != nil {
		return fmt.Errorf("create azd client for Foundry infrastructure delegation: %w", err)
	}
	defer client.Close()
	return delegateFoundryInfraWithClient(ctx, client, provider)
}

func delegateFoundryInfraWithClient(
	ctx context.Context,
	client *azdext.AzdClient,
	provider string,
) error {
	request := delegatedProjectInitRequest{
		SchemaVersion:       delegatedProjectSchemaVersion,
		Source:              delegatedProjectSource,
		SourceVersion:       version.Version,
		Infra:               delegatedProjectInfra{EjectProvider: provider},
		ResolveAzureContext: true,
		Force:               true,
	}
	if err := runDelegatedProjectCommand(
		ctx, client, []string{"ai", "project", "init", "--no-prompt"},
		request,
	); err != nil {
		return fmt.Errorf("delegate Foundry infrastructure ejection: %w", err)
	}
	return nil
}

func delegateFoundryInfraAfterInit(
	ctx context.Context,
	client *azdext.AzdClient,
	provider string,
) error {
	if provider == "" {
		return nil
	}
	projectRoot, err := azdext.GetProjectDir()
	if errors.Is(err, azdext.ErrProjectNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("resolve azd project directory after init: %w", err)
	}
	hasFoundry, err := hasFoundryServiceForEject(projectRoot)
	if err != nil {
		return err
	}
	if !hasFoundry {
		return nil
	}
	return delegateFoundryInfraWithClient(ctx, client, provider)
}

func delegatedProjectServiceName(
	ctx context.Context,
	client *azdext.AzdClient,
) (string, error) {
	response, err := client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return "", fmt.Errorf("read delegated Foundry project service: %w", err)
	}
	var names []string
	for name, service := range response.GetProject().GetServices() {
		if service.GetHost() == AiProjectHost {
			names = append(names, name)
		}
	}
	slices.Sort(names)
	if len(names) == 0 {
		return "", fmt.Errorf(
			"delegated Foundry project initialization did not create an %s service",
			AiProjectHost,
		)
	}
	return names[0], nil
}

func projectResourceIDHint(project *FoundryProjectInfo) string {
	if project == nil {
		return ""
	}
	return project.ResourceId
}

func runDelegatedProjectCommand(
	ctx context.Context,
	client *azdext.AzdClient,
	args []string,
	request any,
) (err error) {
	projectRoot, err := azdext.GetProjectDir()
	if errors.Is(err, azdext.ErrProjectNotFound) {
		projectRoot, err = filepath.Abs(".")
	}
	if err != nil {
		return fmt.Errorf("resolve azd project directory: %w", err)
	}
	data, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode delegated request: %w", err)
	}
	file, err := os.CreateTemp(projectRoot, ".azd-agents-project-*.json")
	if err != nil {
		return fmt.Errorf("create delegated request file: %w", err)
	}
	requestPath := file.Name()
	defer func() {
		if removeErr := os.Remove(requestPath); removeErr != nil &&
			!errors.Is(removeErr, os.ErrNotExist) {
			if err == nil {
				err = fmt.Errorf("remove delegated request file: %w", removeErr)
			} else {
				err = fmt.Errorf(
					"%w; remove delegated request file: %w",
					err,
					removeErr,
				)
			}
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("secure delegated request file: %w", err)
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return fmt.Errorf("write delegated request file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close delegated request file: %w", err)
	}

	commandArgs := append(slices.Clone(args), "--request-file", requestPath)
	_, err = client.Workflow().Run(ctx, &azdext.RunWorkflowRequest{
		Workflow: &azdext.Workflow{
			Name: "agents-project-delegation",
			Steps: []*azdext.WorkflowStep{{
				Command: &azdext.WorkflowCommand{Args: commandArgs},
			}},
		},
	})
	if err != nil {
		return fmt.Errorf("run delegated project command: %w", err)
	}
	return nil
}
