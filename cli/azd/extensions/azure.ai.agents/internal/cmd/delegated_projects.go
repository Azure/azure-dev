// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/anypb"
	"gopkg.in/yaml.v3"
)

const delegatedProjectsSchemaVersion = 1

const (
	delegatedProjectsSource = "azure.ai.agents/init"
	delegatedProjectsInit   = "ai project init"
	delegatedProjectsAdd    = "ai project deployment add"
)

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

type delegatedProjectInitResult struct {
	SchemaVersion   int    `json:"schemaVersion"`
	ProducerVersion string `json:"producerVersion"`
	ServiceName     string `json:"serviceName"`
	Mode            string `json:"mode"`
	Mutation        string `json:"mutation"`
	Endpoint        string `json:"endpoint,omitempty"`
	ResourceID      string `json:"resourceId,omitempty"`
}

type delegatedProjectDeploymentResult struct {
	SchemaVersion   int                         `json:"schemaVersion"`
	ProducerVersion string                      `json:"producerVersion"`
	ServiceName     string                      `json:"serviceName"`
	DeploymentName  string                      `json:"deploymentName"`
	Model           delegatedProjectResultModel `json:"model"`
	SKU             delegatedProjectResultSKU   `json:"sku"`
	Mutation        string                      `json:"mutation"`
}

type delegatedProjectResultModel struct {
	Format  string `json:"format"`
	Name    string `json:"name"`
	Version string `json:"version"`
}

type delegatedProjectResultSKU struct {
	Name     string `json:"name"`
	Capacity int    `json:"capacity"`
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

func validateDelegatedProjectInitResult(result delegatedProjectInitResult) error {
	if result.SchemaVersion != delegatedProjectsSchemaVersion ||
		strings.TrimSpace(result.ProducerVersion) == "" ||
		strings.TrimSpace(result.ServiceName) == "" {
		return fmt.Errorf("delegated project init result is missing required fields")
	}
	if result.Mode != "new" && result.Mode != "existing-id" && result.Mode != "existing-endpoint" {
		return fmt.Errorf("invalid delegated project mode %q", result.Mode)
	}
	if result.Mutation != "created" && result.Mutation != "updated" &&
		result.Mutation != "migrated" && result.Mutation != "unchanged" {
		return fmt.Errorf("invalid delegated project mutation %q", result.Mutation)
	}
	if result.Mode == "existing-id" && result.ResourceID == "" {
		return fmt.Errorf("delegated existing-id result is missing resourceId")
	}
	if result.Mode != "new" && result.Endpoint == "" {
		return fmt.Errorf("delegated existing project result is missing endpoint")
	}
	return nil
}

func validateDelegatedProjectDeploymentResult(result delegatedProjectDeploymentResult) error {
	if result.SchemaVersion != delegatedProjectsSchemaVersion ||
		strings.TrimSpace(result.ProducerVersion) == "" ||
		strings.TrimSpace(result.ServiceName) == "" ||
		strings.TrimSpace(result.DeploymentName) == "" ||
		strings.TrimSpace(result.Model.Name) == "" ||
		strings.TrimSpace(result.Model.Format) == "" ||
		strings.TrimSpace(result.Model.Version) == "" ||
		strings.TrimSpace(result.SKU.Name) == "" ||
		result.SKU.Capacity <= 0 {
		return fmt.Errorf("delegated deployment result is missing required fields")
	}
	if result.Mutation != "created" && result.Mutation != "replaced" && result.Mutation != "unchanged" {
		return fmt.Errorf("invalid delegated deployment mutation %q", result.Mutation)
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
	result any,
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
	resultPath := filepath.Join(tempDir, "result.json")
	if err := writeDelegatedJSON(requestPath, request); err != nil {
		return err
	}

	args := append([]string{}, command...)
	args = append(args,
		"--request-file="+requestPath,
		"--result-file="+resultPath,
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
		return unwrapDelegatedWorkflowError(err)
	}

	if err := readDelegatedJSON(resultPath, result); err != nil {
		if errors.Is(err, os.ErrNotExist) {
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

// unwrapDelegatedWorkflowError preserves structured workflow errors.
// It also reads the wire shape used by older azd modules.
func unwrapDelegatedWorkflowError(err error) error {
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	for _, detail := range st.Details() {
		if anyDetail, ok := detail.(*anypb.Any); ok &&
			strings.HasSuffix(anyDetail.GetTypeUrl(), "WorkflowErrorDetail") {
			if extensionError := extensionErrorFromWorkflowDetail(anyDetail.Value); extensionError != nil {
				return azdext.UnwrapError(extensionError)
			}
		}
		if message, ok := detail.(protoreflect.ProtoMessage); ok &&
			strings.HasSuffix(string(message.ProtoReflect().Descriptor().FullName()), "WorkflowErrorDetail") {
			field := message.ProtoReflect().Get(message.ProtoReflect().Descriptor().Fields().ByName("error"))
			if field.IsValid() {
				if extensionError, ok := field.Message().Interface().(*azdext.ExtensionError); ok {
					return azdext.UnwrapError(extensionError)
				}
			}
		}
	}
	return err
}

func extensionErrorFromWorkflowDetail(data []byte) *azdext.ExtensionError {
	for len(data) > 0 {
		fieldNumber, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil
		}
		data = data[n:]
		if fieldNumber == 1 && wireType == protowire.BytesType {
			value, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 {
				return nil
			}
			result := &azdext.ExtensionError{}
			if err := proto.Unmarshal(value, result); err != nil {
				return nil
			}
			return result
		}
		consumed := protowire.ConsumeFieldValue(fieldNumber, wireType, data)
		if consumed < 0 {
			return nil
		}
		data = data[consumed:]
	}
	return nil
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

func readDelegatedJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	if err := decoder.Decode(value); err != nil {
		return fmt.Errorf("reading delegated result file: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("delegated result file contains multiple JSON documents")
	}
	return nil
}

func (a *InitAction) delegateProjectInit(
	ctx context.Context,
	allowedLocations []string,
) (delegatedProjectInitResult, error) {
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
		return delegatedProjectInitResult{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid delegated project init request: %s", err),
			"upgrade azure.ai.agents and azure.ai.projects to compatible versions",
		)
	}
	var result delegatedProjectInitResult
	if err := a.runDelegatedProjectStep(ctx, strings.Fields(delegatedProjectsInit), request, &result); err != nil {
		return result, err
	}
	if err := validateDelegatedProjectInitResult(result); err != nil {
		return result, exterrors.Compatibility(
			exterrors.CodeIncompatibleAzdVersion,
			fmt.Sprintf("azure.ai.projects returned an invalid project init result: %s", err),
			"upgrade azure.ai.agents and azure.ai.projects to compatible versions",
		)
	}
	a.projectServiceName = result.ServiceName
	if a.flags != nil {
		a.flags.delegatedProjectInit = true
	}
	return result, nil
}

func (a *InitAction) delegateProjectDeployment(
	ctx context.Context,
	model, deploymentName string,
	setAsDefault bool,
	allowedLocations []string,
) (delegatedProjectDeploymentResult, error) {
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
		return delegatedProjectDeploymentResult{}, exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid delegated deployment request: %s", err),
			"upgrade azure.ai.agents and azure.ai.projects to compatible versions",
		)
	}
	var result delegatedProjectDeploymentResult
	if err := a.runDelegatedProjectStep(ctx, strings.Fields(delegatedProjectsAdd), request, &result); err != nil {
		return result, err
	}
	if err := validateDelegatedProjectDeploymentResult(result); err != nil {
		return result, exterrors.Compatibility(
			exterrors.CodeIncompatibleAzdVersion,
			fmt.Sprintf("azure.ai.projects returned an invalid deployment result: %s", err),
			"upgrade azure.ai.agents and azure.ai.projects to compatible versions",
		)
	}
	if a.projectServiceName == "" {
		a.projectServiceName = result.ServiceName
	}
	return result, nil
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

func projectInfoFromDelegatedResult(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	envName string,
	result delegatedProjectInitResult,
) (*FoundryProjectInfo, error) {
	if result.Mode != "existing-id" || result.ResourceID == "" {
		return nil, nil
	}
	projectInfo, err := extractProjectDetails(result.ResourceID)
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
	initResult, err := a.delegateProjectInit(ctx, allowedLocations)
	if err != nil {
		return nil, err
	}
	projectInfo, err := projectInfoFromDelegatedResult(
		ctx, a.azdClient, a.delegatedEnvironmentName(), initResult,
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
			result, err := a.delegateProjectDeployment(
				ctx, modelDeployment.Model.Name, "",
				setAsDefault, allowedLocations,
			)
			if err != nil {
				return nil, err
			}
			finalName = result.DeploymentName
			modelDeployment = &project.Deployment{
				Name: finalName,
				Model: project.DeploymentModel{
					Format:  result.Model.Format,
					Name:    result.Model.Name,
					Version: result.Model.Version,
				},
				Sku: project.DeploymentSku{
					Name:     result.SKU.Name,
					Capacity: result.SKU.Capacity,
				},
			}
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
		ctx, projectInfo, initResult.Mode,
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
