// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

// DirectDeployPreviewResult describes configuration changes without deploying.
// Source contents are not compared; a real deploy always uploads a new version.
type DirectDeployPreviewResult struct {
	Name            string                        `json:"name"`
	Service         string                        `json:"service,omitempty"`
	CurrentVersion  string                        `json:"currentVersion,omitempty"`
	Operation       string                        `json:"operation"`
	HasChanges      bool                          `json:"hasChanges"`
	Changes         []DeployPreviewChangeGroup    `json:"changes"`
	SourcePath      string                        `json:"sourcePath"`
	Notes           []string                      `json:"notes"`
	Sources         []string                      `json:"sources,omitempty"`
	Image           *DeployPreviewImage           `json:"image,omitempty"`
	SourceConflicts []DeployPreviewSourceConflict `json:"sourceConflicts,omitempty"`
}

// DeployPreviewSourceConflict records lower-priority local values that differ
// from the effective configuration. These are not additional remote changes.
type DeployPreviewSourceConflict struct {
	Source      string                     `json:"source"`
	Differences []DeployPreviewChangeGroup `json:"differences"`
}

// DeployPreviewChangeGroup groups changes to related agent settings.
type DeployPreviewChangeGroup struct {
	Group   string                `json:"group"`
	Changes []DeployPreviewChange `json:"changes"`
}

// DeployPreviewChange describes a single added, removed, modified, or pending field.
// Pending fields are resolved by a dependency deployment. Sensitive values are redacted.
type DeployPreviewChange struct {
	Field     string `json:"field"`
	Kind      string `json:"kind"`
	Before    any    `json:"before,omitempty"`
	After     any    `json:"after,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
}

type standaloneAgentReader interface {
	GetAgent(context.Context, string, string, bool) (*agent_api.AgentObject, error)
}

type standaloneAgentReaderFactory func(string) (standaloneAgentReader, error)

type hostedAgentPreview struct {
	request         *agent_api.CreateAgentRequest
	definition      agent_yaml.ContainerAgent
	activityProfile ActivityProfile
	projectEndpoint string
	codePath        string
	image           *DeployPreviewImage
	sources         []string
	alternatives    []previewRequestAlternative
}

type previewRequestAlternative struct {
	source  string
	request *agent_api.CreateAgentRequest
}

// PreviewStandaloneHostedAgent compares the standalone deployment request with
// the latest remote version using only a GET. It never packages or uploads code.
// pendingEnvironment lists variables whose values require a dependency deployment.
func PreviewStandaloneHostedAgent(
	ctx context.Context,
	options DirectDeployOptions,
	pendingEnvironment []string,
) (*DirectDeployPreviewResult, error) {
	return previewStandaloneHostedAgent(ctx, options, pendingEnvironment, newDeploymentPreviewClient)
}

func newDeploymentPreviewClient(endpoint string) (standaloneAgentReader, error) {
	credential, err := newStandaloneCredential()
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run 'azd auth login' to authenticate",
		)
	}
	// Remote definitions contain environment secrets. Do not log the GET body,
	// even when debugging; only the redacted comparison should expose values.
	return agent_api.NewAgentClientWithOptions(endpoint, credential, nil), nil
}

func previewStandaloneHostedAgent(
	ctx context.Context,
	options DirectDeployOptions,
	pendingEnvironment []string,
	newClient standaloneAgentReaderFactory,
) (*DirectDeployPreviewResult, error) {
	loaded := options.PreviewDefinition
	if loaded == nil {
		var err error
		loaded, err = LoadAgentPreviewDefinition(options.DefinitionPath, options.Environment)
		if err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(options.ProjectEndpoint) == "" {
		return nil, exterrors.Dependency(exterrors.CodeMissingAiProjectEndpoint,
			"a Foundry project endpoint is required for deployment preview", "pass --project-endpoint")
	}
	definition := loaded.Definition
	plan, err := planPreviewImage(definition, nil, "", options.Environment)
	if err != nil {
		return nil, err
	}
	if plan.Mode == "prebuilt" && options.CodePath != "" {
		return nil, exterrors.Validation(exterrors.CodeConflictingArguments,
			"--code cannot be used with a prebuilt-image preview", "omit --code to preview the configured image")
	}
	codePath := options.CodePath
	if codePath == "" {
		codePath = filepath.Dir(options.DefinitionPath)
	}
	if plan.Mode != "prebuilt" {
		codePath, err = resolveAgentCodePath(codePath)
		if err != nil {
			return nil, err
		}
	}
	if err := previewDefinitionDefaults(&definition, plan.Mode == "code"); err != nil {
		return nil, err
	}
	environment, err := normalizePreviewEnvironment(&definition, options.Environment, true)
	if err != nil {
		return nil, err
	}
	for name, value := range environment {
		if previewContainsUnknown(value) {
			pendingEnvironment = append(pendingEnvironment, name)
		}
	}
	request, err := buildPreviewRequest(definition, environment, plan)
	if err != nil {
		return nil, err
	}
	input := hostedAgentPreview{
		request: request, definition: definition,
		activityProfile: ResolveActivityProfile(definition),
		projectEndpoint: strings.TrimRight(options.ProjectEndpoint, "/"), codePath: codePath,
		image: plan, sources: loaded.Sources,
	}
	if err := addPreviewAlternatives(&input, loaded, options.Environment); err != nil {
		return nil, err
	}
	return previewHostedAgentRequest(ctx, input, pendingEnvironment, newClient)
}

func buildPreviewRequest(
	definition agent_yaml.ContainerAgent, environment map[string]string, plan *DeployPreviewImage,
) (*agent_api.CreateAgentRequest, error) {
	options := append([]agent_yaml.AgentBuildOption{
		agent_yaml.WithEnvironmentVariables(environment),
		agent_yaml.WithCPU(definition.Resources.Cpu), agent_yaml.WithMemory(definition.Resources.Memory),
	}, previewImageOption(plan)...)
	request, err := agent_yaml.CreateAgentAPIRequestFromDefinition(definition, options...)
	if err != nil {
		return nil, exterrors.Validation(exterrors.CodeInvalidAgentRequest,
			fmt.Sprintf("invalid agent preview configuration: %s", err), "fix the agent definition and retry")
	}
	applyAgentMetadata(request)
	return request, nil
}

func addPreviewAlternatives(
	input *hostedAgentPreview, loaded *AgentPreviewDefinition, environment map[string]string,
) error {
	for _, alternative := range loaded.alternatives {
		definition := alternative.definition
		if err := previewDefinitionDefaults(&definition, input.image.Mode == "code"); err != nil {
			return err
		}
		values, err := normalizePreviewEnvironment(&definition, environment, true)
		if err != nil {
			return err
		}
		plan := *input.image
		if plan.Mode == "prebuilt" && definition.Image != "" {
			plan.Image = definition.Image
			plan.Known = !previewContainsUnknown(definition.Image)
		}
		request, err := buildPreviewRequest(definition, values, &plan)
		if err != nil {
			return err
		}
		input.alternatives = append(input.alternatives, previewRequestAlternative{source: alternative.source, request: request})
	}
	return nil
}

func previewHostedAgentRequest(
	ctx context.Context,
	input hostedAgentPreview,
	pendingEnvironment []string,
	newClient standaloneAgentReaderFactory,
) (*DirectDeployPreviewResult, error) {
	request := input.request
	client, err := newClient(input.projectEndpoint)
	if err != nil {
		return nil, err
	}
	existing, err := client.GetAgent(ctx, request.Name, agent_api.AgentEndpointAPIVersion, true)
	if err != nil {
		if responseErr, ok := errors.AsType[*azcore.ResponseError](err); !ok ||
			responseErr.StatusCode != http.StatusNotFound {
			return nil, redactPreviewError(exterrors.ServiceFromAzure(err, exterrors.OpGetAgent))
		}
		existing = nil
	} else {
		if existing == nil || existing.Versions.Latest.Definition == nil || existing.Versions.Latest.Version == "" {
			return nil, fmt.Errorf("agent response does not contain the latest deployed version and definition")
		}
		if err := reconcileStandaloneEndpointWithDeployedAgent(
			request, input.activityProfile, existing,
		); err != nil {
			return nil, err
		}
	}

	result, err := compareAgentDeployment(request, existing, pendingEnvironment)
	if err != nil {
		return nil, err
	}
	result.SourcePath = filepath.Clean(input.codePath)
	for _, source := range input.sources {
		result.Sources = append(result.Sources, normalizePreviewSourcePath(source))
	}
	if input.image != nil {
		image := *input.image
		image.Image = redactPreviewString(image.Image)
		image.Repository = redactPreviewString(image.Repository)
		image.Tag = redactPreviewString(image.Tag)
		image.AlternativeImage = redactPreviewString(image.AlternativeImage)
		result.Image = &image
	}
	result.Notes = []string{
		"Deploy creates a new agent version even when configuration has no changes.",
	}
	if input.image != nil {
		switch input.image.Mode {
		case "code":
			result.Notes = append(result.Notes,
				"Source code would be packaged and uploaded; source contents are not compared.")
		case "prebuilt":
			if result.HasImageChanges() {
				result.Notes = append(result.Notes,
					"The configured container image would be used; no image would be built or pushed.")
			}
			if !input.image.Known {
				markPreviewImagePending(result)
			}
		case "build":
			result.Notes = append(result.Notes, "A container image would be built and pushed; no build or push was performed.")
			if !input.image.Known {
				result.Notes = append(result.Notes,
					"The final image reference is known after build/provisioning; default tags use azd-deploy-<timestamp>.")
				markPreviewImagePending(result)
			}
			if input.image.AlternativeImage != "" {
				result.Notes = append(result.Notes,
					"Interactive deploy can choose the configured prebuilt image; this preview uses the default build choice.")
			}
		}
	}
	code := input.definition.CodeConfiguration
	if code != nil && code.DependencyResolution != nil && *code.DependencyResolution == "bundled" {
		if strings.HasPrefix(code.Runtime, "dotnet_") {
			result.Notes = append(result.Notes, "Bundled .NET dependencies would be built locally during deployment.")
		}
	} else if code != nil {
		result.Notes = append(result.Notes, "Dependencies would be built remotely by Foundry during deployment.")
	}
	if err := addPreviewSourceConflicts(result, input); err != nil {
		return nil, err
	}
	return result, nil
}

func markPreviewImagePending(result *DirectDeployPreviewResult) {
	for i := range result.Changes {
		if result.Changes[i].Group == "containerImage" {
			for j := range result.Changes[i].Changes {
				if result.Changes[i].Changes[j].Field == "image" {
					result.Changes[i].Changes[j].Kind = "pending"
					result.Changes[i].Changes[j].After = nil
					return
				}
			}
		}
	}
}

func addPreviewSourceConflicts(
	result *DirectDeployPreviewResult, input hostedAgentPreview,
) error {
	local := &agent_api.AgentObject{Name: input.request.Name}
	local.Versions.Latest = agent_api.AgentVersionObject{
		Definition: input.request.Definition, Description: input.request.Description, Metadata: input.request.Metadata,
	}
	local.AgentEndpoint, local.AgentCard = input.request.AgentEndpoint, input.request.AgentCard
	for _, alternative := range input.alternatives {
		difference, err := compareAgentDeployment(alternative.request, local, nil, true)
		if err != nil {
			return err
		}
		if len(difference.Changes) == 0 {
			continue
		}
		result.SourceConflicts = append(result.SourceConflicts, DeployPreviewSourceConflict{
			Source: normalizePreviewSourcePath(alternative.source), Differences: difference.Changes,
		})
	}
	return nil
}

func normalizePreviewSourcePath(source string) string {
	path, fragment, hasFragment := strings.Cut(source, "#")
	result := filepath.Clean(path)
	if hasFragment {
		result += "#" + fragment
	}
	return redactPreviewString(result)
}

const previewModelDeploymentKey = "AZURE_AI_MODEL_DEPLOYMENT_NAME"

func compareAgentDeployment(
	desired *agent_api.CreateAgentRequest,
	existing *agent_api.AgentObject,
	pendingEnvironment []string,
	compareSources ...bool,
) (*DirectDeployPreviewResult, error) {
	after, err := deploymentPreviewState(
		desired.Name, desired.Description, desired.Metadata, desired.Definition, desired.AgentEndpoint, desired.AgentCard,
	)
	if err != nil {
		return nil, fmt.Errorf("normalize local agent definition: %w", err)
	}
	result := &DirectDeployPreviewResult{
		Name:      desired.Name,
		Operation: "create",
		Changes:   []DeployPreviewChangeGroup{},
	}
	before := map[string]map[string]any{}
	if existing != nil {
		latest := existing.Versions.Latest
		result.CurrentVersion = redactPreviewString(latest.Version)
		result.Operation = "create_version"
		before, err = deploymentPreviewState(
			existing.Name, latest.Description, latest.Metadata, latest.Definition, existing.AgentEndpoint, existing.AgentCard,
		)
		if err != nil {
			return nil, fmt.Errorf("normalize deployed agent definition: %w", err)
		}
	}
	if desired.DigitalWorkerType != "" {
		after["metadata"]["digital_worker_type"] = string(desired.DigitalWorkerType)
		if existing != nil && existing.DigitalWorkerType != "" {
			before["metadata"]["digital_worker_type"] = string(existing.DigitalWorkerType)
		}
	}
	for _, group := range []string{
		"metadata", "protocols", "resources", "environmentVariables", "modelDeployment",
		"containerImage", "code", "session", "contentSafety", "endpoint", "agentCard",
	} {
		oldFields := before[group]
		newFields := after[group]
		if group == "endpoint" || group == "agentCard" {
			// These fields are PATCHed: omitted properties are preserved, not removed.
			oldFields = previewPatchedFields(oldFields, newFields)
		}
		var pending []string
		for _, name := range pendingEnvironment {
			if (group == "environmentVariables" && name != previewModelDeploymentKey) ||
				(group == "modelDeployment" && name == previewModelDeploymentKey) {
				pending = append(pending, name)
			}
		}
		changes := comparePreviewFields(oldFields, newFields, "", group == "environmentVariables", pending,
			len(compareSources) > 0 && compareSources[0])
		if len(changes) > 0 {
			result.Changes = append(result.Changes, DeployPreviewChangeGroup{Group: group, Changes: changes})
		}
	}
	result.HasChanges = existing == nil || len(result.Changes) > 0
	return result, nil
}

func deploymentPreviewState(
	name string,
	description *string,
	metadata map[string]string,
	definition any,
	endpoint *agent_api.AgentEndpoint,
	card *agent_api.AgentCard,
) (map[string]map[string]any, error) {
	data, err := json.Marshal(definition)
	if err != nil {
		return nil, err
	}
	var hosted agent_api.HostedAgentDefinition
	if err := json.Unmarshal(data, &hosted); err != nil {
		return nil, err
	}
	if hosted.Kind != agent_api.AgentKindHosted {
		return nil, fmt.Errorf("expected a hosted agent definition, got kind %q", hosted.Kind)
	}

	for i := range hosted.ProtocolVersions {
		protocol := &hosted.ProtocolVersions[i]
		if agent_api.IsActivityProtocolName(protocol.Protocol) {
			protocol.Protocol = agent_api.AgentProtocolActivityProtocol
		}
		if protocol.Version == "v1" {
			protocol.Version = "1.0.0"
		}
	}
	slices.SortFunc(hosted.ProtocolVersions, func(a, b agent_api.ProtocolVersionRecord) int {
		return cmp.Or(cmp.Compare(a.Protocol, b.Protocol), cmp.Compare(a.Version, b.Version))
	})
	hosted.ProtocolVersions = slices.Compact(hosted.ProtocolVersions)
	if cpu, err := strconv.ParseFloat(hosted.CPU, 64); err == nil {
		hosted.CPU = strconv.FormatFloat(cpu, 'f', -1, 64)
	}

	environment := maps.Clone(hosted.EnvironmentVariables)
	model := map[string]string{}
	if value, exists := environment[previewModelDeploymentKey]; exists {
		model[previewModelDeploymentKey] = value
		delete(environment, previewModelDeploymentKey)
	}
	text := ""
	if description != nil {
		text = *description
	}
	if hosted.CodeConfiguration != nil {
		// A service-generated image is not an authored image override.
		hosted.ContainerConfiguration = nil
		if hosted.CodeConfiguration.DependencyResolution == "" {
			hosted.CodeConfiguration.DependencyResolution = "remote_build"
		}
	}
	if hosted.SessionConfiguration == nil {
		hosted.SessionConfiguration = &agent_api.SessionConfigurationAPI{IdleTimeoutSeconds: 900}
	}
	data, err = json.Marshal(map[string]any{
		"metadata": map[string]any{
			"name": name, "description": text, "metadata": deploymentPreviewMetadata(metadata),
		},
		"protocols":            map[string]any{"protocols": hosted.ProtocolVersions},
		"resources":            map[string]string{"cpu": hosted.CPU, "memory": hosted.Memory},
		"environmentVariables": environment,
		"modelDeployment":      model,
		"containerImage":       hosted.ContainerConfiguration,
		"code":                 hosted.CodeConfiguration,
		"session":              hosted.SessionConfiguration,
		"contentSafety":        hosted.RaiConfig,
		"endpoint":             endpoint,
		"agentCard":            card,
	})
	if err != nil {
		return nil, err
	}
	var state map[string]map[string]any
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, err
	}
	if protocols, ok := state["endpoint"]["protocols"].([]any); ok {
		slices.SortFunc(protocols, func(a, b any) int {
			return cmp.Compare(fmt.Sprint(a), fmt.Sprint(b))
		})
	}
	return state, nil
}

func deploymentPreviewMetadata(metadata map[string]string) map[string]any {
	result := make(map[string]any, len(metadata))
	for key, value := range metadata {
		if key == "tags" {
			if value == "" {
				continue
			}
			var tags []string
			if err := json.Unmarshal([]byte(value), &tags); err == nil && tags != nil {
				if len(tags) == 0 {
					continue
				}
				slices.Sort(tags)
				result[key] = slices.Compact(tags)
				continue
			}
			// Previously authored scalar metadata remains an opaque string.
		}
		result[key] = value
	}
	return result
}

func previewPatchedFields(before, after map[string]any) map[string]any {
	selected := map[string]any{}
	for key, value := range after {
		if previous, exists := before[key]; exists {
			previousMap, previousIsMap := previous.(map[string]any)
			valueMap, valueIsMap := value.(map[string]any)
			if previousIsMap && valueIsMap {
				selected[key] = previewPatchedFields(previousMap, valueMap)
			} else {
				selected[key] = previous
			}
		}
	}
	return selected
}

func comparePreviewFields(
	before, after map[string]any, prefix string, sensitive bool, pending []string,
	compareSources bool,
) []DeployPreviewChange {
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	for _, key := range pending {
		keys[key] = true
	}
	var changes []DeployPreviewChange
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		oldValue, hadValue := before[key]
		newValue, hasValue := after[key]
		field := prefix + key
		if compareSources && hadValue == hasValue && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		if slices.Contains(pending, key) || (!sensitive && previewContainsUnknown(newValue)) {
			change := DeployPreviewChange{Field: redactPreviewString(field), Kind: "pending", Sensitive: sensitive}
			if hadValue {
				change.Before = redactPreviewValue(oldValue, sensitive)
			}
			changes = append(changes, change)
			continue
		}
		oldMap, oldIsMap := oldValue.(map[string]any)
		newMap, newIsMap := newValue.(map[string]any)
		if (oldIsMap || oldValue == nil) && (newIsMap || newValue == nil) && (oldIsMap || newIsMap) {
			changes = append(changes, comparePreviewFields(oldMap, newMap, field+".", sensitive, nil, compareSources)...)
			continue
		}
		if hadValue == hasValue && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		change := DeployPreviewChange{Field: redactPreviewString(field), Kind: "modify", Sensitive: sensitive}
		if !hadValue {
			change.Kind = "add"
		} else if !hasValue {
			change.Kind = "remove"
		}
		if hadValue {
			change.Before = redactPreviewValue(oldValue, sensitive)
		}
		if hasValue {
			change.After = redactPreviewValue(newValue, sensitive)
		}
		changes = append(changes, change)
	}
	return changes
}

var previewURLPattern = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://[^\s<>"']+`)

func redactPreviewString(value string) string {
	return previewURLPattern.ReplaceAllStringFunc(value, func(rawURL string) string {
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return "[redacted URL]"
		}
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.ForceQuery = false
		parsed.Fragment = ""
		parsed.RawFragment = ""
		return parsed.String()
	})
}

func redactPreviewError(err error) error {
	if serviceErr, ok := errors.AsType[*azdext.ServiceError](err); ok {
		redacted := *serviceErr
		redacted.Message = redactPreviewString(serviceErr.Message)
		return &redacted
	}
	if localErr, ok := errors.AsType[*azdext.LocalError](err); ok {
		redacted := *localErr
		redacted.Message = redactPreviewString(localErr.Message)
		return &redacted
	}
	return err
}

func redactPreviewValue(value any, sensitive bool) any {
	if sensitive {
		return "[redacted]"
	}
	switch value := value.(type) {
	case string:
		return redactPreviewString(value)
	case []any:
		redacted := make([]any, len(value))
		for i, item := range value {
			redacted[i] = redactPreviewValue(item, false)
		}
		return redacted
	case map[string]any:
		redacted := make(map[string]any, len(value))
		for key, item := range value {
			redacted[redactPreviewString(key)] = redactPreviewValue(item, false)
		}
		return redacted
	default:
		return value
	}
}
