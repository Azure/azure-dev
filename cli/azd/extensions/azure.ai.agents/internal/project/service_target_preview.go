// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/projectconfig"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext/preview"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/proto"
)

var _ preview.ServiceTargetPreviewProvider = (*AgentServiceTargetProvider)(nil)

const previewImagePath = "definition.container_configuration.image"

type agentPreviewReader interface {
	GetAgent(context.Context, string, string, bool) (*agent_api.AgentObject, error)
}

// Preview compares unified hosted-agent configuration with the latest remote
// version on a fresh provider, without deployment preparation or terminal output.
func (p *AgentServiceTargetProvider) Preview(
	ctx context.Context, betaService *v1beta.ServiceConfig,
) (*v1beta.ServiceDeployPreviewResult, error) {
	if betaService == nil {
		return nil, previewConfigurationError()
	}
	// Stable and beta ServiceConfig messages share the same wire fields.
	wire, err := proto.Marshal(betaService)
	if err != nil {
		return nil, previewConfigurationError()
	}
	service := &azdext.ServiceConfig{}
	if err := proto.Unmarshal(wire, service); err != nil {
		return nil, previewConfigurationError()
	}
	project, err := p.azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || project.GetProject().GetPath() == "" {
		return nil, exterrors.Dependency(exterrors.CodeProjectNotFound,
			"Cannot read the project for deployment preview.", "run preview from an initialized azd project")
	}
	original := project.GetProject().GetServices()[service.Name]
	if original == nil {
		return nil, previewConfigurationError()
	}
	// Check provenance before a resolved transport config can hide a legacy include.
	if _, err := resolvePreviewSource(original, project.Project.Path); err != nil {
		return nil, err
	}
	service, err = resolvePreviewSource(service, project.Project.Path)
	if err != nil {
		return nil, err
	}
	current, err := p.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || current.GetEnvironment().GetName() == "" {
		return nil, exterrors.Dependency(exterrors.CodeEnvironmentNotFound,
			"An existing azd environment is required for deployment preview.",
			"select an existing environment with --environment")
	}
	values, err := p.azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
		Name: current.Environment.Name,
	})
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || values == nil {
		return nil, exterrors.Dependency(exterrors.CodeEnvironmentValuesFailed,
			"Cannot read environment values for deployment preview.", "check the selected azd environment")
	}
	environment := make(map[string]string, len(values.KeyValues))
	for _, entry := range values.KeyValues {
		environment[entry.GetKey()] = entry.GetValue()
	}
	endpoint, err := previewProjectEndpoint(environment["FOUNDRY_PROJECT_ENDPOINT"])
	if err != nil {
		return nil, err
	}
	pending, err := previewPendingInputs(project.Project.Path, service, environment)
	if err != nil {
		return nil, err
	}
	if slices.Contains(pending, previewImagePath) {
		service.Image = "preview.invalid/unknown"
	}
	service, definition, err := resolvePreviewDefinition(service, project.Project.Path)
	if err != nil {
		return nil, err
	}
	request, inputs, err := preparePreviewRequest(service, definition, environment, pending)
	if err != nil {
		return nil, err
	}
	subscription := environment["AZURE_SUBSCRIPTION_ID"]
	if subscription == "" {
		return nil, exterrors.Dependency(exterrors.CodeMissingAzureSubscription,
			"AZURE_SUBSCRIPTION_ID is required for deployment preview.", "check the selected azd environment")
	}
	tenant, err := p.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: subscription})
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil || tenant.GetTenantId() == "" {
		return nil, exterrors.Auth(exterrors.CodeTenantLookupFailed,
			"Cannot resolve the user access tenant for deployment preview.",
			"run 'azd auth login' and check your subscription access")
	}
	factory := p.previewReader
	if factory == nil {
		factory = newAgentPreviewReader
	}
	reader, err := factory(endpoint, tenant.TenantId)
	if err != nil {
		return nil, exterrors.Auth(exterrors.CodeCredentialCreationFailed,
			"Cannot create the preview credential.", "run 'azd auth login'")
	}
	return previewAgentRequest(ctx, reader, service.Name, request, inputs)
}

func newAgentPreviewReader(endpoint, tenantID string) (agentPreviewReader, error) {
	credential, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID: tenantID, AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, err
	}
	return agent_api.NewAgentClient(endpoint, credential), nil
}

func previewProjectEndpoint(raw string) (string, error) {
	if raw == "" {
		return "", exterrors.Dependency(exterrors.CodeMissingAiProjectEndpoint,
			"FOUNDRY_PROJECT_ENDPOINT is required for deployment preview.",
			"connect this environment to an existing Microsoft Foundry project")
	}
	endpoint, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" {
		return "", exterrors.Validation(exterrors.CodeInvalidParameter,
			"Invalid Foundry project endpoint for deployment preview.", "provide an HTTPS project endpoint")
	}
	endpoint.User = nil
	endpoint.RawQuery = ""
	endpoint.ForceQuery = false
	endpoint.Fragment = ""
	endpoint.RawFragment = ""
	return strings.TrimRight(endpoint.String(), "/"), nil
}

func preparePreviewRequest(
	service *azdext.ServiceConfig, definition agent_yaml.ContainerAgent,
	environment map[string]string, pending []string,
) (*agent_api.CreateAgentRequest, previewInputs, error) {
	if definition.CodeConfiguration == nil &&
		service.GetDocker().GetImagePassthrough() && service.GetDocker().GetRemoteBuild() {
		return nil, previewInputs{}, previewConfigurationError()
	}
	inputs := previewInputs{
		Unknown: slices.Clone(pending), ContainerImage: &previewContainerImage{},
	}
	prebuilt := definition.Image != "" && (service.GetDocker().GetImagePassthrough() ||
		definition.RegistryConnectionID != "" ||
		strings.EqualFold(strings.TrimSpace(environment["AZD_AGENT_SKIP_ACR"]), "true"))
	switch {
	case definition.CodeConfiguration != nil:
		inputs.IgnoreImage = true
		inputs.ContainerImage.Build, inputs.ContainerImage.Push = new(false), new(false)
	case prebuilt || service.GetDocker().GetImagePassthrough():
		inputs.ContainerImage.Build, inputs.ContainerImage.Push = new(false), new(false)
	case definition.Image != "" && !azdext.DetectInteractive().NoPrompt:
		inputs.IgnoreImage = true
		inputs.Unknown = append(inputs.Unknown, "containerImage.build", "containerImage.push")
	default:
		inputs.IgnoreImage = true
		inputs.ContainerImage.Build, inputs.ContainerImage.Push = new(true), new(true)
	}
	if inputs.IgnoreImage {
		inputs.Unknown = slices.DeleteFunc(inputs.Unknown, func(path string) bool { return path == previewImagePath })
	}
	unknown := inputs.Unknown
	validationService := service
	if slices.Contains(unknown, previewImagePath) {
		validationService = proto.CloneOf(service)
		validationService.Image = "preview.invalid/unknown"
	}
	if err := validateRegistryConnectionServiceConfig(validationService); err != nil {
		return nil, previewInputs{}, previewConfigurationError()
	}
	resolved := maps.Clone(service.GetEnvironment())
	if resolved == nil {
		resolved = map[string]string{}
	}
	if definition.EnvironmentVariables != nil {
		for _, variable := range *definition.EnvironmentVariables {
			if _, overridden := resolved[variable.Name]; overridden {
				continue
			}
			lookup := func(name string) (string, bool) {
				if value, found := service.Environment[name]; found {
					return value, true
				}
				value, found := environment[name]
				return value, found
			}
			missing, err := previewEnvironmentInputUnknown(variable.Value, lookup)
			if err != nil {
				return nil, previewInputs{}, previewConfigurationError()
			}
			if missing {
				unknown = append(unknown, "definition.environment_variables."+variable.Name)
			}
			value, err := ResolveAgentEnvironmentVariable(variable.Name, variable.Value, service.Environment,
				func(name string) string { value, _ := lookup(name); return value })
			if err != nil {
				return nil, previewInputs{}, previewConfigurationError()
			}
			resolved[variable.Name] = value
		}
	}
	var options []agent_yaml.AgentBuildOption
	if definition.CodeConfiguration == nil {
		switch {
		case slices.Contains(unknown, previewImagePath):
			options = append(options, agent_yaml.WithImageURL("preview.invalid/unknown"))
		case prebuilt:
			options = append(options, agent_yaml.WithImageURL(definition.Image))
		case service.GetDocker().GetImagePassthrough():
			return nil, previewInputs{}, previewConfigurationError()
		default:
			options = append(options, agent_yaml.WithImageURL("preview.invalid/unknown"))
		}
	}
	prepared, err := prepareDeployRequest(service, definition, resolved, options)
	if err != nil {
		return nil, previewInputs{}, previewConfigurationError()
	}
	config, err := LoadServiceTargetAgentConfig(service)
	if err != nil {
		return nil, previewInputs{}, previewConfigurationError()
	}
	profile, err := ResolveActivityProfileForDeploy(definition, config.Activity)
	if err != nil {
		return nil, previewInputs{}, previewConfigurationError()
	}
	// Deploy applies endpoint auth normalization after creating the agent version.
	ensureActivityEndpointAuthSchemeForProfile(prepared.request, profile)
	inputs.Unknown = unknown
	return prepared.request, inputs, nil
}

func previewPendingInputs(
	root string, service *azdext.ServiceConfig, environment map[string]string,
) ([]string, error) {
	raw, err := projectconfig.LoadServiceEnvironment(root, service.Name)
	if err != nil {
		return nil, previewConfigurationError()
	}
	lookup := func(variable string) (string, bool) {
		value, found := environment[variable]
		if !found {
			value, found = os.LookupEnv(variable)
		}
		return value, found
	}
	var pending []string
	for name, expression := range raw {
		missing, err := previewEnvironmentInputUnknown(expression, lookup)
		if err != nil {
			return nil, previewConfigurationError()
		}
		if missing {
			pending = append(pending, "definition.environment_variables."+name)
		}
	}
	data, _, err := projectconfig.ReadProjectFile(root)
	if err != nil {
		return nil, previewConfigurationError()
	}
	var document struct {
		Services map[string]struct{ Image string } `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, previewConfigurationError()
	}
	missing, err := previewEnvironmentInputUnknown(document.Services[service.Name].Image, lookup)
	if err != nil {
		return nil, previewConfigurationError()
	}
	if missing {
		pending = append(pending, previewImagePath)
	}
	return pending, nil
}

var (
	previewRequiredVariable = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	previewServerTemplate   = regexp.MustCompile(`(?s)\$\{\{.*?\}\}`)
)

func previewEnvironmentInputUnknown(expression string, lookup func(string) (string, bool)) (bool, error) {
	missing := map[string]bool{}
	if _, err := ExpandEnv(expression, func(variable string) string {
		value, found := lookup(variable)
		missing[variable] = !found
		return value
	}); err != nil {
		return false, err
	}
	expression = previewServerTemplate.ReplaceAllString(expression, "")
	for _, match := range previewRequiredVariable.FindAllStringSubmatch(expression, -1) {
		if missing[match[1]] {
			return true, nil
		}
	}
	return false, nil
}

func previewAgentRequest(
	ctx context.Context, reader agentPreviewReader, service string,
	request *agent_api.CreateAgentRequest, inputs previewInputs,
) (*v1beta.ServiceDeployPreviewResult, error) {
	existing, err := reader.GetAgent(ctx, request.Name, agent_api.AgentEndpointAPIVersion,
		activityProfileFromCreateRequest(request).IsActivity)
	if err != nil {
		response, isResponse := errors.AsType[*azcore.ResponseError](err)
		if !isResponse || response.StatusCode != http.StatusNotFound {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			// Response bodies and transport/auth errors can contain credentials.
			if isResponse {
				return nil, &azdext.ServiceError{
					Message:    fmt.Sprintf("Foundry deployment preview read failed (HTTP %d).", response.StatusCode),
					StatusCode: response.StatusCode, ServiceName: "foundry",
					Suggestion: "check your login, project permissions, and network access",
				}
			}
			return nil, fmt.Errorf("cannot read the deployed agent; check authentication, connectivity, and response format")
		}
		existing = nil
	} else if existing == nil || existing.Name != request.Name || existing.Versions.Latest.Version == "" ||
		existing.Versions.Latest.Definition == nil {
		return nil, fmt.Errorf("Foundry returned a malformed agent response; preview cannot compare it")
	}
	return comparePreviewRequest(service, request, existing, inputs)
}
