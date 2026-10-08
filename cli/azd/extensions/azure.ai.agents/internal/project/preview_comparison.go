// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode"

	"azureaiagent/internal/pkg/agents/agent_api"
	"azureaiagent/internal/pkg/agents/agent_yaml"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"google.golang.org/protobuf/types/known/structpb"
)

type previewChange struct {
	Group     string `json:"group"`
	Path      string `json:"path"`
	Operation string `json:"operation"`
}

type agentPreviewResult struct {
	Service string          `json:"service"`
	Agent   string          `json:"agent"`
	Status  string          `json:"status"`
	Changes []previewChange `json:"changes"`
	Unknown []string        `json:"unknown"`
	Notes   []string        `json:"notes"`
}

func comparePreviewRequest(
	service string, desired *agent_api.CreateAgentRequest, existing *agent_api.AgentObject, unknown []string,
) (*v1beta.ServiceDeployPreviewResult, error) {
	after, secrets, err := previewRequestState(desired)
	if err != nil {
		return nil, previewConfigurationError()
	}
	before := map[string]any{}
	if existing != nil {
		latest := existing.Versions.Latest
		remote := &agent_api.CreateAgentRequest{
			Name: existing.Name,
			CreateAgentVersionRequest: agent_api.CreateAgentVersionRequest{
				Description: latest.Description, Metadata: latest.Metadata, Definition: latest.Definition,
				DigitalWorkerType: existing.DigitalWorkerType,
			},
		}
		if desired.AgentEndpoint != nil {
			remote.AgentEndpoint = existing.AgentEndpoint
		}
		if desired.AgentCard != nil {
			remote.AgentCard = existing.AgentCard
		}
		var remoteSecrets []string
		before, remoteSecrets, err = previewRequestState(remote)
		if err != nil {
			return nil, fmt.Errorf("Foundry returned an invalid hosted-agent definition; preview cannot compare it")
		}
		secrets = append(secrets, remoteSecrets...)
	}
	slices.SortFunc(secrets, func(a, b string) int { return len(b) - len(a) })
	clean := func(value string) string {
		value = redactPreviewURLs(value)
		for _, secret := range secrets {
			if secret != "" {
				value = strings.ReplaceAll(value, secret, "[redacted]")
			}
		}
		return strings.Map(func(r rune) rune {
			if unicode.IsControl(r) {
				return '?'
			}
			return r
		}, value)
	}
	result := agentPreviewResult{
		Service: clean(service), Agent: clean(desired.Name),
		Status: "noChange", Changes: []previewChange{}, Unknown: []string{},
		Notes: []string{
			"Read-only comparison of the latest agent version; infrastructure and dependencies are not previewed.",
			"Change values are omitted to protect environment values and credentials.",
			"Deploy creates a new agent version even when configuration has no changes.",
		},
	}
	keys := maps.Clone(before)
	maps.Copy(keys, after)
	for _, path := range slices.Sorted(maps.Keys(keys)) {
		if slices.Contains(unknown, path) {
			continue
		}
		oldValue, oldExists := before[path]
		newValue, newExists := after[path]
		if !newExists && (strings.HasPrefix(path, "agent_endpoint.") || strings.HasPrefix(path, "agent_card.")) {
			// Endpoint/card PATCH preserves unmentioned fields.
			continue
		}
		if oldExists == newExists && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		operation := "update"
		if !oldExists {
			operation = "add"
		} else if !newExists {
			operation = "remove"
		}
		result.Changes = append(result.Changes, previewChange{
			Group: previewFieldGroup(path), Path: clean(path), Operation: operation,
		})
	}
	for _, path := range unknown {
		result.Unknown = append(result.Unknown, clean(path))
	}
	slices.Sort(result.Unknown)
	result.Unknown = slices.Compact(result.Unknown)
	switch {
	case existing == nil:
		result.Status = "create"
	case len(result.Changes) > 0:
		result.Status = "update"
	case len(result.Unknown) > 0:
		result.Status = "unknown"
	}
	if len(result.Unknown) > 0 {
		result.Notes = append(result.Notes,
			"Unknown fields require build/upload or unresolved inputs; preview does not build, push, or upload artifacts.")
	}
	return previewResponse(result)
}

func previewRequestState(request *agent_api.CreateAgentRequest) (map[string]any, []string, error) {
	data, err := json.Marshal(request.Definition)
	if err != nil {
		return nil, nil, err
	}
	var definition agent_api.HostedAgentDefinition
	if err := json.Unmarshal(data, &definition); err != nil {
		return nil, nil, err
	}
	if definition.Kind != agent_api.AgentKindHosted || definition.CPU == "" || definition.Memory == "" ||
		(definition.ContainerConfiguration == nil && definition.CodeConfiguration == nil) {
		return nil, nil, fmt.Errorf("invalid hosted definition")
	}
	if definition.CodeConfiguration != nil {
		if definition.CodeConfiguration.Runtime == "" || len(definition.CodeConfiguration.EntryPoint) == 0 {
			return nil, nil, fmt.Errorf("invalid code configuration")
		}
		// The service can expose its built image alongside the source code config.
		definition.ContainerConfiguration = nil
		if definition.CodeConfiguration.DependencyResolution == "" {
			definition.CodeConfiguration.DependencyResolution = agent_yaml.DefaultDependencyResolution
		}
	} else if definition.ContainerConfiguration.Image == "" {
		return nil, nil, fmt.Errorf("invalid container configuration")
	}
	if definition.SessionConfiguration == nil {
		definition.SessionConfiguration = &agent_api.SessionConfigurationAPI{IdleTimeoutSeconds: 900}
	}
	if cpu, err := strconv.ParseFloat(definition.CPU, 64); err == nil {
		definition.CPU = strconv.FormatFloat(cpu, 'f', -1, 64)
	}
	slices.SortFunc(definition.ProtocolVersions, func(a, b agent_api.ProtocolVersionRecord) int {
		return strings.Compare(string(a.Protocol)+"/"+a.Version, string(b.Protocol)+"/"+b.Version)
	})
	definition.ProtocolVersions = slices.Compact(definition.ProtocolVersions)
	definition.Image = ""
	var secrets []string
	for _, value := range definition.EnvironmentVariables {
		secrets = append(secrets, value)
	}
	clone := *request
	clone.Definition = definition
	if clone.Description != nil && *clone.Description == "" {
		clone.Description = nil
	}
	if clone.AgentEndpoint != nil {
		endpoint := *clone.AgentEndpoint
		endpoint.Protocols = slices.Clone(endpoint.Protocols)
		slices.Sort(endpoint.Protocols)
		endpoint.Protocols = slices.Compact(endpoint.Protocols)
		clone.AgentEndpoint = &endpoint
	}
	data, err = json.Marshal(clone)
	if err != nil {
		return nil, nil, err
	}
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil {
		return nil, nil, err
	}
	result := map[string]any{}
	flattenPreviewState("", object, result)
	return result, secrets, nil
}

func previewFieldGroup(path string) string {
	switch {
	case path == "definition.environment_variables.AZURE_AI_MODEL_DEPLOYMENT_NAME":
		return "modelDeployment"
	case strings.HasPrefix(path, "definition.environment_variables."):
		return "environmentVariables"
	case path == "definition.cpu" || path == "definition.memory":
		return "resources"
	case path == "definition.protocol_versions":
		return "protocols"
	case strings.HasPrefix(path, "definition.container_configuration."):
		return "containerImage"
	case strings.HasPrefix(path, "definition.code_configuration."):
		return "code"
	case strings.HasPrefix(path, "definition.session_configuration."):
		return "session"
	case strings.HasPrefix(path, "agent_endpoint."):
		return "endpoint"
	case strings.HasPrefix(path, "agent_card."):
		return "agentCard"
	case path == "definition.kind":
		return "metadata"
	case strings.HasPrefix(path, "definition."):
		return "contentSafety"
	default:
		return "metadata"
	}
}

func flattenPreviewState(prefix string, object map[string]any, result map[string]any) {
	for key, value := range object {
		path := key
		if prefix != "" {
			path = prefix + "." + key
		}
		if nested, ok := value.(map[string]any); ok {
			flattenPreviewState(path, nested, result)
		} else {
			result[path] = value
		}
	}
}

var previewURLPattern = regexp.MustCompile(`(?i)[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

func redactPreviewURLs(value string) string {
	return previewURLPattern.ReplaceAllStringFunc(value, func(raw string) string {
		parsed, err := url.Parse(raw)
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

func previewResponse(result agentPreviewResult) (*v1beta.ServiceDeployPreviewResult, error) {
	var message bytes.Buffer
	if err := writeAgentPreview(&message, result); err != nil {
		return nil, err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("cannot encode deployment preview")
	}
	var values map[string]any
	if err := json.Unmarshal(data, &values); err != nil {
		return nil, fmt.Errorf("cannot encode deployment preview data")
	}
	fields, err := structpb.NewStruct(values)
	if err != nil {
		return nil, fmt.Errorf("cannot encode deployment preview fields")
	}
	return &v1beta.ServiceDeployPreviewResult{Message: message.String(), Data: fields}, nil
}

func writeAgentPreview(writer io.Writer, result agentPreviewResult) error {
	var message strings.Builder
	fmt.Fprintf(&message, "Service: %s (agent: %s)\n", result.Service, result.Agent)
	switch result.Status {
	case "create":
		message.WriteString("Would create the hosted agent.\n")
	case "update":
		message.WriteString("Would update the hosted agent.\n")
	case "unknown":
		message.WriteString("Known configuration matches; artifact or input changes are unknown.\n")
	default:
		message.WriteString("No deployment configuration changes.\n")
	}
	for _, group := range []struct{ key, label string }{
		{"metadata", "Metadata"}, {"protocols", "Protocols"}, {"resources", "Resources"},
		{"environmentVariables", "Environment variables"}, {"modelDeployment", "Model deployment"},
		{"containerImage", "Container image"}, {"code", "Code"}, {"session", "Session"},
		{"contentSafety", "Content safety"}, {"endpoint", "Endpoint"}, {"agentCard", "Agent card"},
	} {
		shown := false
		for _, change := range result.Changes {
			if change.Group != group.key {
				continue
			}
			if !shown {
				fmt.Fprintf(&message, "  %s:\n", group.label)
				shown = true
			}
			fmt.Fprintf(&message, "    %s: %s\n", change.Operation, change.Path)
		}
	}
	for _, unknown := range result.Unknown {
		fmt.Fprintf(&message, "  unknown: %s\n", unknown)
	}
	for _, note := range result.Notes {
		fmt.Fprintln(&message, note)
	}
	_, err := io.WriteString(writer, message.String())
	return err
}
