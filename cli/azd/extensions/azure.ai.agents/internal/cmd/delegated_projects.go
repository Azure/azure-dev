// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/project"
	"azureaiagent/internal/version"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v3"
)

const delegatedProjectsSchemaVersion = 1

const (
	delegatedProjectsSource = "azure.ai.agents/init"
	delegatedProjectsInit   = "ai project init"
	delegatedProjectsAdd    = "ai project deployment add"
)

func delegatedModelName(raw string) string {
	raw = strings.TrimSpace(raw)
	if slash := strings.IndexByte(raw, '/'); slash >= 0 &&
		slash < len(raw)-1 {
		return raw[slash+1:]
	}
	return raw
}

type delegatedProjectTarget struct {
	ResourceID string `json:"resourceId,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
}

type delegatedProjectInfra struct {
	EjectProvider string `json:"ejectProvider,omitempty"`
}

type delegatedProjectRequirements struct {
	AllowedLocations []string `json:"allowedLocations,omitempty"`
}

type delegatedProjectInitRequest struct {
	SchemaVersion       int                          `json:"schemaVersion"`
	Source              string                       `json:"source"`
	SourceVersion       string                       `json:"sourceVersion"`
	Project             delegatedProjectTarget       `json:"project"`
	Infra               delegatedProjectInfra        `json:"infra,omitempty"`
	Requirements        delegatedProjectRequirements `json:"requirements,omitempty"`
	ResolveAzureContext bool                         `json:"resolveAzureContext"`
	Force               bool                         `json:"force"`
}

type delegatedProjectModel struct {
	Name                 string   `json:"name"`
	DeploymentName       string   `json:"deploymentName,omitempty"`
	RequiredCapabilities []string `json:"requiredCapabilities,omitempty"`
	AllowedLocations     []string `json:"allowedLocations,omitempty"`
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

type delegatedProjectState struct {
	ServiceName string
	Mode        string
	ResourceID  string
	Endpoint    string
	Deployments []project.Deployment
}

var errDelegatedProjectsUnavailable = errors.New("azure.ai.projects delegated commands are unavailable")

func validateDelegatedProjectInitRequest(request delegatedProjectInitRequest) error {
	if request.SchemaVersion != delegatedProjectsSchemaVersion {
		return fmt.Errorf("unsupported delegated project schema version %d", request.SchemaVersion)
	}
	if request.Source != delegatedProjectsSource || strings.TrimSpace(request.SourceVersion) == "" {
		return fmt.Errorf("invalid delegated project source")
	}
	if request.Project.ResourceID != "" && request.Project.Endpoint != "" {
		return fmt.Errorf("project.resourceId and project.endpoint are mutually exclusive")
	}
	if request.Infra.EjectProvider != "" &&
		request.Infra.EjectProvider != project.BicepProviderName &&
		request.Infra.EjectProvider != project.TerraformProviderName {
		return fmt.Errorf("unsupported delegated infrastructure provider %q", request.Infra.EjectProvider)
	}
	if request.Requirements.AllowedLocations != nil && len(request.Requirements.AllowedLocations) == 0 {
		return fmt.Errorf("requirements.allowedLocations must contain a location")
	}
	return nil
}

func validateDelegatedProjectDeploymentRequest(request delegatedProjectDeploymentRequest) error {
	if request.SchemaVersion != delegatedProjectsSchemaVersion {
		return fmt.Errorf("unsupported delegated project schema version %d", request.SchemaVersion)
	}
	if request.Source != delegatedProjectsSource || strings.TrimSpace(request.SourceVersion) == "" {
		return fmt.Errorf("invalid delegated project source")
	}
	if strings.TrimSpace(request.Model.Name) == "" {
		return fmt.Errorf("model.name is required")
	}
	for _, capability := range request.Model.RequiredCapabilities {
		if capability != agentsV2ModelCapability {
			return fmt.Errorf("unknown required capability %q", capability)
		}
	}
	return nil
}

func (a *InitAction) delegatedProjectRoot() (string, error) {
	root := ""
	if a.projectConfig != nil {
		root = a.projectConfig.Path
	}
	if root == "" {
		var err error
		root, err = os.Getwd()
		if err != nil {
			return "", fmt.Errorf("resolving project root: %w", err)
		}
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving project root: %w", err)
	}
	return filepath.Clean(root), nil
}

func (a *InitAction) delegatedEnvironmentName() string {
	if a.flags != nil && a.flags.env != "" {
		return a.flags.env
	}
	if a.environment != nil {
		return a.environment.Name
	}
	return ""
}

func (a *InitAction) runDelegatedProjectStep(
	ctx context.Context,
	command []string,
	request any,
) error {
	root, err := a.delegatedProjectRoot()
	if err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp("", "azd-agent-project-*")
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeProjectInitFailed,
			fmt.Sprintf("creating delegated project workspace: %s", err),
			"check write permissions on the system temporary directory",
		)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()
	if err := os.Chmod(tempDir, 0700); err != nil {
		return fmt.Errorf("protecting delegated project workspace: %w", err)
	}
	requestPath := filepath.Join(tempDir, "request.json")
	if err := writeDelegatedJSON(requestPath, request); err != nil {
		return err
	}

	args := append([]string{}, command...)
	args = append(args,
		"--request-file="+requestPath,
		"--output=none",
		"--cwd="+root,
	)
	if environment := a.delegatedEnvironmentName(); environment != "" {
		args = append(args, "--environment="+environment)
	}

	workflow := &azdext.Workflow{
		Name: "agent-project-delegation",
		Steps: []*azdext.WorkflowStep{{
			Command: &azdext.WorkflowCommand{Args: args},
		}},
	}
	if _, err := a.azdClient.Workflow().Run(ctx, &azdext.RunWorkflowRequest{Workflow: workflow}); err != nil {
		// Older projects extensions do not know these commands. Stage A keeps
		// the old path for that case only.
		if isDelegatedProjectsUnavailable(err) {
			return errDelegatedProjectsUnavailable
		}
		return err
	}
	return nil
}

func isDelegatedProjectsUnavailable(err error) bool {
	if err == nil {
		return false
	}

	if st, ok := status.FromError(err); ok && st.Code() == codes.Unimplemented {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "unknown command") ||
		strings.Contains(message, "command not found") ||
		strings.Contains(message, "not installed") ||
		strings.Contains(message, "unimplemented")
}

func writeDelegatedJSON(path string, value any) error {
	file, err := os.OpenFile(
		path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, osutil.PermissionFileOwnerOnly,
	)
	if err != nil {
		return fmt.Errorf("creating delegated request file: %w", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("writing delegated request file: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return fmt.Errorf("flushing delegated request file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("closing delegated request file: %w", err)
	}
	return nil
}

func (s delegatedProjectState) deployment(name string) (*project.Deployment, error) {
	for index := range s.Deployments {
		if strings.EqualFold(s.Deployments[index].Name, name) {
			return &s.Deployments[index], nil
		}
	}
	return nil, exterrors.Compatibility(
		exterrors.CodeIncompatibleAzdVersion,
		fmt.Sprintf("project state is missing deployment %q", name),
		"upgrade azure.ai.agents and azure.ai.projects to compatible versions",
	)
}

func (a *InitAction) readDelegatedProjectState(
	ctx context.Context,
) (delegatedProjectState, error) {
	response, err := a.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		if isDelegatedProjectsUnavailable(err) {
			return delegatedProjectState{}, errDelegatedProjectsUnavailable
		}
		return delegatedProjectState{}, fmt.Errorf("read delegated project state: %w", err)
	}
	if response.GetProject() == nil {
		return delegatedProjectState{}, exterrors.Dependency(
			exterrors.CodeProjectNotFound,
			"the azd project disappeared after delegated initialization",
			"restore the project manifest and retry",
		)
	}

	var services []*azdext.ServiceConfig
	for _, service := range response.GetProject().GetServices() {
		if service != nil && service.GetHost() == AiProjectHost {
			services = append(services, service)
		}
	}
	if len(services) == 0 {
		return delegatedProjectState{}, exterrors.Validation(
			"project_service_missing",
			"delegated initialization completed without an azure.ai.project service",
			"upgrade azure.ai.projects and retry",
		)
	}
	if len(services) > 1 {
		return delegatedProjectState{}, exterrors.Validation(
			"project_service_ambiguous",
			"delegated initialization produced multiple azure.ai.project services",
			"keep exactly one azure.ai.project service and retry",
		)
	}

	properties := services[0].GetAdditionalProperties()
	config := project.ServiceTargetAgentConfig{}
	if properties != nil {
		if err := project.UnmarshalStruct(properties, &config); err != nil {
			return delegatedProjectState{}, fmt.Errorf(
				"decode delegated project service state: %w", err,
			)
		}
	}
	values, err := a.readDelegatedEnvironment(ctx)
	if err != nil {
		return delegatedProjectState{}, err
	}
	resourceID := strings.TrimSpace(values["AZURE_AI_PROJECT_ID"])
	endpoint := strings.TrimSpace(config.Endpoint)
	if endpoint == "" {
		endpoint = strings.TrimSpace(values["FOUNDRY_PROJECT_ENDPOINT"])
	}
	mode := "new"
	if resourceID != "" {
		mode = "existing-id"
	} else if endpoint != "" {
		mode = "existing-endpoint"
	}
	return delegatedProjectState{
		ServiceName: services[0].GetName(),
		Mode:        mode,
		ResourceID:  resourceID,
		Endpoint:    endpoint,
		Deployments: config.Deployments,
	}, nil
}

func (a *InitAction) readDelegatedEnvironment(
	ctx context.Context,
) (map[string]string, error) {
	envName := a.delegatedEnvironmentName()
	if envName == "" {
		current, err := a.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
		if err != nil || current.GetEnvironment() == nil {
			return nil, exterrors.Dependency(
				exterrors.CodeEnvironmentNotFound,
				"no environment is selected for delegated project state",
				"select an azd environment and retry",
			)
		}
		envName = current.GetEnvironment().GetName()
	}
	response, err := a.azdClient.Environment().GetValues(
		ctx, &azdext.GetEnvironmentRequest{Name: envName},
	)
	if err != nil {
		return nil, fmt.Errorf("read delegated environment state: %w", err)
	}
	values := make(map[string]string, len(response.GetKeyValues()))
	for _, pair := range response.GetKeyValues() {
		if pair != nil {
			values[pair.GetKey()] = pair.GetValue()
		}
	}
	return values, nil
}

func (a *InitAction) delegateProjectInit(
	ctx context.Context,
	allowedLocations []string,
) (delegatedProjectState, error) {
	request := delegatedProjectInitRequest{
		SchemaVersion:       delegatedProjectsSchemaVersion,
		Source:              delegatedProjectsSource,
		SourceVersion:       version.Version,
		Project:             delegatedProjectTarget{ResourceID: a.flags.projectResourceId},
		Infra:               delegatedProjectInfra{EjectProvider: a.flags.infra},
		Requirements:        delegatedProjectRequirements{AllowedLocations: allowedLocations},
		ResolveAzureContext: true,
		Force:               a.flags.force,
	}
	if err := validateDelegatedProjectInitRequest(request); err != nil {
		return delegatedProjectState{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid delegated project init request: %s", err),
			"upgrade azure.ai.agents and azure.ai.projects to compatible versions",
		)
	}
	if err := a.runDelegatedProjectStep(ctx, strings.Fields(delegatedProjectsInit), request); err != nil {
		return delegatedProjectState{}, err
	}
	state, err := a.readDelegatedProjectState(ctx)
	if err != nil {
		return delegatedProjectState{}, err
	}
	a.projectServiceName = state.ServiceName
	if a.flags != nil {
		a.flags.delegatedProjectInit = true
	}
	return state, nil
}

func (a *InitAction) delegateProjectDeployment(
	ctx context.Context,
	model, deploymentName string,
	setAsDefault bool,
	allowedLocations []string,
) (delegatedProjectState, error) {
	if strings.TrimSpace(deploymentName) == "" {
		deploymentName = delegatedModelName(model)
	}
	request := delegatedProjectDeploymentRequest{
		SchemaVersion: delegatedProjectsSchemaVersion,
		Source:        delegatedProjectsSource,
		SourceVersion: version.Version,
		Model: delegatedProjectModel{
			Name:                 model,
			DeploymentName:       deploymentName,
			RequiredCapabilities: []string{agentsV2ModelCapability},
			AllowedLocations:     allowedLocations,
		},
		SetAsDefault: setAsDefault,
		Force:        a.flags.force,
	}
	if err := validateDelegatedProjectDeploymentRequest(request); err != nil {
		return delegatedProjectState{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid delegated deployment request: %s", err),
			"upgrade azure.ai.agents and azure.ai.projects to compatible versions",
		)
	}
	if err := a.runDelegatedProjectStep(ctx, strings.Fields(delegatedProjectsAdd), request); err != nil {
		return delegatedProjectState{}, err
	}
	state, err := a.readDelegatedProjectState(ctx)
	if err != nil {
		return delegatedProjectState{}, err
	}
	if _, err := state.deployment(deploymentName); err != nil {
		return delegatedProjectState{}, err
	}
	a.projectServiceName = state.ServiceName
	return state, nil
}

func (a *InitAction) hostedAgentAllowedLocations(ctx context.Context) ([]string, error) {
	if !a.skipACR() {
		return nil, nil
	}
	locations, err := supportedRegionsForInit(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		fmt.Fprintf(os.Stderr, "warning: failed to resolve hosted-agent regions: %v\n", err)
		return nil, nil
	}
	return locations, nil
}

func modelResourceFromManifest(resource any) (agent_yaml.ModelResource, bool) {
	model, ok := resource.(agent_yaml.ModelResource)
	return model, ok
}

func projectInfoFromDelegatedState(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	state delegatedProjectState,
) (*FoundryProjectInfo, error) {
	if state.Mode != "existing-id" || state.ResourceID == "" {
		return nil, nil
	}
	projectInfo, err := extractProjectDetails(state.ResourceID)
	if err != nil {
		return nil, err
	}
	projectInfo.Location, _ = getEnvValue(ctx, azdClient, envName, "AZURE_LOCATION")
	if projectInfo.Location == "" {
		projectInfo.Location, _ = getEnvValue(ctx, azdClient, envName, "AZURE_AI_DEPLOYMENTS_LOCATION")
	}
	return projectInfo, nil
}

func (a *InitAction) configureDelegatedAgentResources(
	ctx context.Context,
	projectInfo *FoundryProjectInfo,
	mode string,
) error {
	if a.environment == nil {
		return nil
	}
	if projectInfo == nil || mode == "new" {
		return setACREnvVar(ctx, a.azdClient, a.environment.Name, a.skipACR())
	}
	projectInfo.NetworkInjected = foundryAccountNetworkInjected(ctx, a.credential, projectInfo)
	a.selectedFoundryProject = projectInfo
	if err := configureExistingProjectAgentConnections(
		ctx, a.azdClient, a.credential, a.environment.Name,
		*projectInfo, projectInfo.SubscriptionId, a.skipACR(),
	); err != nil {
		return err
	}
	return setACREnvVar(ctx, a.azdClient, a.environment.Name, a.skipACR())
}

func (a *InitAction) configureModelChoiceDelegated(
	ctx context.Context,
	agentManifest *agent_yaml.AgentManifest,
) (*agent_yaml.AgentManifest, error) {
	allowedLocations, err := a.hostedAgentAllowedLocations(ctx)
	if err != nil {
		return nil, err
	}
	initState, err := a.delegateProjectInit(ctx, allowedLocations)
	if err != nil {
		return nil, err
	}
	projectInfo, err := projectInfoFromDelegatedState(
		ctx, a.azdClient, a.delegatedEnvironmentName(), initState,
	)
	if err != nil {
		return nil, err
	}

	templateBytes, err := yaml.Marshal(agentManifest.Template)
	if err != nil {
		return nil, fmt.Errorf("marshaling agent template: %w", err)
	}
	var definition agent_yaml.AgentDefinition
	if err := yaml.Unmarshal(templateBytes, &definition); err != nil {
		return nil, fmt.Errorf("reading agent definition: %w", err)
	}
	paramValues := agent_yaml.ParameterValues{}
	var firstResolved *project.Deployment
	anyModelProcessed := false
	anyNewDeployment := false
	managedModelIndex := 0

	for _, rawResource := range agentManifest.Resources {
		resource, ok := modelResourceFromManifest(rawResource)
		if !ok || definition.Kind != agent_yaml.AgentKindHosted {
			continue
		}
		var (
			modelDeployment *project.Deployment
			isNew           = a.flags.modelDeployment == ""
		)
		if !isNew {
			// External deployment references remain an agent operation and are
			// verified against Azure before being injected into the manifest.
			deployment, _, resolveErr := a.getModelDeploymentDetails(
				ctx, agent_yaml.Model{Id: resource.Id},
			)
			if resolveErr != nil {
				if errors.Is(resolveErr, errModelSkipped) {
					continue
				}
				return nil, fmt.Errorf("failed to resolve model %q: %w", resource.Id, resolveErr)
			}
			modelDeployment = deployment
			if modelDeployment == nil {
				return nil, fmt.Errorf("model deployment %q was not resolved", a.flags.modelDeployment)
			}
		} else {
			modelName := resource.Id
			if managedModelIndex == 0 && strings.TrimSpace(a.flags.model) != "" {
				modelName = a.flags.model
			}
			modelDeployment = &project.Deployment{
				Model: project.DeploymentModel{Name: modelName},
			}
			managedModelIndex++
		}
		anyModelProcessed = true
		finalName := modelDeployment.Name
		if isNew {
			setAsDefault := firstResolved == nil
			deploymentState, err := a.delegateProjectDeployment(
				ctx, modelDeployment.Model.Name, "",
				setAsDefault, allowedLocations,
			)
			if err != nil {
				return nil, err
			}
			defaultName := delegatedModelName(modelDeployment.Model.Name)
			resolved, err := deploymentState.deployment(defaultName)
			if err != nil {
				return nil, err
			}
			finalName = resolved.Name
			modelDeployment = resolved
			anyNewDeployment = true
		}
		if firstResolved == nil {
			firstResolved = modelDeployment
			if !isNew {
				if err := setEnvValue(
					ctx, a.azdClient, a.environment.Name,
					"AZURE_AI_MODEL_DEPLOYMENT_NAME", finalName,
				); err != nil {
					return nil, err
				}
			}
		}
		paramValues[resource.Name] = finalName
	}

	updated, err := agent_yaml.InjectParameterValuesIntoManifest(agentManifest, paramValues)
	if err != nil {
		return nil, fmt.Errorf("injecting deployment names into manifest: %w", err)
	}
	if err := a.configureDelegatedAgentResources(
		ctx, projectInfo, initState.Mode,
	); err != nil {
		return nil, err
	}
	a.deploymentDetails = nil
	if anyModelProcessed {
		if err := updatePendingModelDeploymentSignal(
			ctx, a.azdClient, a.environment.Name, true, anyNewDeployment,
		); err != nil {
			// The signal is advisory and has the same best-effort semantics as
			// the legacy model path.
			fmt.Fprintf(os.Stderr, "warning: failed to update model deployment signal: %v\n", err)
		}
	}
	return updated, nil
}
