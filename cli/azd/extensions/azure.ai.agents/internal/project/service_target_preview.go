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
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
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
	sourceInputs, err := previewSourceInputs(project.Project.Path, service, environment)
	if err != nil {
		return nil, err
	}
	if slices.Contains(sourceInputs.Unknown, previewImagePath) {
		service.Image = "preview.invalid/unknown"
	}
	service, definition, err := resolvePreviewDefinition(service, project.Project.Path)
	if err != nil {
		return nil, err
	}
	request, inputs, err := preparePreviewRequest(service, definition, environment, sourceInputs.Unknown)
	if err != nil {
		return nil, err
	}
	// The transport config is already interpolated; only authored sources establish provenance.
	inputs.PublicEnvironment = sourceInputs.PublicEnvironment
	inputs.Secrets = append(inputs.Secrets, sourceInputs.Secrets...)
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
		Unknown: slices.Clone(pending), Declared: previewDeclaredFields(service, definition),
		Description: definition.Description, ProtocolVersions: map[string]bool{}, PublicEnvironment: map[string]bool{},
	}
	for name, value := range service.Environment {
		if name == "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
			inputs.PublicEnvironment[name] = true
			continue
		}
		public, err := previewEnvironmentSource(value, func(key string) string { return environment[key] }, &inputs.Secrets)
		if err != nil {
			return nil, previewInputs{}, previewConfigurationError()
		}
		inputs.PublicEnvironment[name] = public
	}
	for _, protocol := range service.GetAdditionalProperties().GetFields()["protocols"].GetListValue().GetValues() {
		fields := protocol.GetStructValue().GetFields()
		if fields["version"].GetStringValue() == "" {
			name := fields["protocol"].GetStringValue()
			_, declared := fields["version"]
			inputs.ProtocolVersions[name] = inputs.ProtocolVersions[name] || declared
		}
	}
	prebuilt := definition.Image != "" && (service.GetDocker().GetImagePassthrough() ||
		definition.RegistryConnectionID != "" ||
		strings.EqualFold(strings.TrimSpace(environment["AZD_AGENT_SKIP_ACR"]), "true"))
	switch {
	case definition.CodeConfiguration != nil:
		// The code path has no azd container build/push intent or authored image.
	case prebuilt || service.GetDocker().GetImagePassthrough():
		inputs.ContainerImage = &previewContainerImage{Build: new(false), Push: new(false)}
	case definition.Image != "" && !azdext.DetectInteractive().NoPrompt:
		inputs.IgnoreImage = true
		inputs.ContainerImage = &previewContainerImage{}
		inputs.Unknown = append(inputs.Unknown, "containerImage.build", "containerImage.push")
	default:
		inputs.IgnoreImage = true
		inputs.ContainerImage = &previewContainerImage{Build: new(true), Push: new(true)}
	}
	if inputs.IgnoreImage || definition.CodeConfiguration != nil {
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
			secrets := &inputs.Secrets
			if variable.Name == "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
				secrets = new([]string)
			}
			public, err := previewEnvironmentSource(variable.Value,
				func(name string) string { value, _ := lookup(name); return value }, secrets)
			if err != nil {
				return nil, previewInputs{}, previewConfigurationError()
			}
			inputs.PublicEnvironment[variable.Name] = public
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
	for _, path := range unknown {
		inputs.Declared[path] = true
	}
	return prepared.request, inputs, nil
}

func previewDeclaredFields(service *azdext.ServiceConfig, definition agent_yaml.ContainerAgent) map[string]bool {
	props := service.GetAdditionalProperties().GetFields()
	fields := map[string]bool{}
	for _, path := range []string{"name", "description"} {
		if _, declared := props[path]; declared {
			fields[path] = true
		}
	}
	if definition.Metadata != nil {
		for name := range *definition.Metadata {
			fields["metadata."+name] = true
		}
	}
	if _, declared := props["protocols"]; declared {
		fields["definition.protocol_versions"] = true
	}
	resources := props["container"].GetStructValue().GetFields()["resources"].GetStructValue().GetFields()
	for _, name := range []string{"cpu", "memory"} {
		if _, declared := resources[name]; declared {
			fields["definition."+name] = true
		}
	}
	for name := range service.GetEnvironment() {
		fields["definition.environment_variables."+name] = true
	}
	if definition.EnvironmentVariables != nil {
		for _, variable := range *definition.EnvironmentVariables {
			fields["definition.environment_variables."+variable.Name] = true
		}
	}
	if definition.CodeConfiguration == nil {
		if definition.Image != "" {
			fields[previewImagePath] = true
		}
		if _, declared := props["registryConnectionId"]; declared {
			fields["definition.container_configuration.registry_connection_id"] = true
		}
	}
	return fields
}

func previewSourceInputs(
	root string, service *azdext.ServiceConfig, environment map[string]string,
) (previewInputs, error) {
	raw, err := projectconfig.LoadServiceEnvironment(root, service.Name)
	if err != nil {
		return previewInputs{}, previewConfigurationError()
	}
	lookup := func(variable string) (string, bool) {
		value, found := environment[variable]
		if !found {
			value, found = os.LookupEnv(variable)
		}
		return value, found
	}
	inputs := previewInputs{PublicEnvironment: map[string]bool{}}
	for name, expression := range raw {
		secrets := &inputs.Secrets
		if name == "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
			secrets = new([]string)
		}
		public, err := previewEnvironmentSource(expression,
			func(key string) string { value, _ := lookup(key); return value }, secrets)
		if err != nil {
			return previewInputs{}, previewConfigurationError()
		}
		inputs.PublicEnvironment[name] = public
		missing, err := previewEnvironmentInputUnknown(expression, lookup)
		if err != nil {
			return previewInputs{}, previewConfigurationError()
		}
		if missing {
			inputs.Unknown = append(inputs.Unknown, "definition.environment_variables."+name)
		}
	}
	data, _, err := projectconfig.ReadProjectFile(root)
	if err != nil {
		return previewInputs{}, previewConfigurationError()
	}
	var document struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	if err := yaml.Unmarshal(data, &document); err != nil {
		return previewInputs{}, previewConfigurationError()
	}
	source, err := foundry.ResolveFileRefs(document.Services[service.Name], root)
	if err != nil {
		return previewInputs{}, previewConfigurationError()
	}
	// Scan only previewed inputs, not excluded code/artifact settings.
	for _, key := range []string{"name", "description", "metadata", "protocols", "image", "registryConnectionId"} {
		if err := previewSourceSecrets(source[key], lookup, &inputs.Secrets); err != nil {
			return previewInputs{}, previewConfigurationError()
		}
	}
	if container, ok := source["container"].(map[string]any); ok {
		if err := previewSourceSecrets(container["resources"], lookup, &inputs.Secrets); err != nil {
			return previewInputs{}, previewConfigurationError()
		}
	}
	// The deprecated list form still has ordinary deployment support.
	if variables, ok := source["environmentVariables"].([]any); ok {
		for _, item := range variables {
			fields, ok := item.(map[string]any)
			if !ok {
				return previewInputs{}, previewConfigurationError()
			}
			name, ok := fields["name"].(string)
			if !ok || name == "" {
				return previewInputs{}, previewConfigurationError()
			}
			if _, overridden := raw[name]; overridden {
				continue
			}
			value, ok := fields["value"].(string)
			if !ok {
				return previewInputs{}, previewConfigurationError()
			}
			secrets := &inputs.Secrets
			if name == "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
				secrets = new([]string)
			}
			public, err := previewEnvironmentSource(value,
				func(key string) string { value, _ := lookup(key); return value }, secrets)
			if err != nil {
				return previewInputs{}, previewConfigurationError()
			}
			inputs.PublicEnvironment[name] = public
		}
	}
	image, _ := source["image"].(string)
	missing, err := previewEnvironmentInputUnknown(image, lookup)
	if err != nil {
		return previewInputs{}, previewConfigurationError()
	}
	if missing {
		inputs.Unknown = append(inputs.Unknown, previewImagePath)
	}
	return inputs, nil
}

func previewEnvironmentSource(expression string, lookup func(string) string, secrets *[]string) (bool, error) {
	public := true
	_, err := ExpandEnv(expression, func(name string) string {
		public = false
		value := lookup(name)
		if value != "" {
			*secrets = append(*secrets, value)
		}
		return value
	})
	return public, err
}

func previewSourceSecrets(value any, lookup func(string) (string, bool), secrets *[]string) error {
	switch typed := value.(type) {
	case string:
		_, err := previewEnvironmentSource(typed,
			func(name string) string { value, _ := lookup(name); return value }, secrets)
		return err
	case []any:
		for _, item := range typed {
			if err := previewSourceSecrets(item, lookup, secrets); err != nil {
				return err
			}
		}
	case map[string]any:
		for _, item := range typed {
			if err := previewSourceSecrets(item, lookup, secrets); err != nil {
				return err
			}
		}
	}
	return nil
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
