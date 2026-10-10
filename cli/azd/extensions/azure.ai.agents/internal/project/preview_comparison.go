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
	"azureaiagent/internal/pkg/containerref"

	v1beta "github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta"
	"google.golang.org/protobuf/types/known/structpb"
)

type previewChange struct {
	Group     string `json:"group"`
	Path      string `json:"path"`
	Operation string `json:"operation"`
	Before    any    `json:"before,omitempty"`
	After     any    `json:"after,omitempty"`
}

type agentPreviewResult struct {
	Service        string                 `json:"service"`
	Agent          string                 `json:"agent"`
	Status         string                 `json:"status"`
	Changes        []previewChange        `json:"changes"`
	Unknown        []string               `json:"unknown"`
	Notes          []string               `json:"notes"`
	ContainerImage *previewContainerImage `json:"containerImage,omitempty"`
}

type previewContainerImage struct {
	Build *bool `json:"build"`
	Push  *bool `json:"push"`
}

type previewInputs struct {
	Unknown          []string
	ContainerImage   *previewContainerImage
	IgnoreImage      bool
	Declared         map[string]bool
	Description      *string
	ProtocolVersions map[string]bool
}

func comparePreviewRequest(
	service string, desired *agent_api.CreateAgentRequest, existing *agent_api.AgentObject, inputs previewInputs,
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
		var remoteSecrets []string
		before, remoteSecrets, err = previewRequestState(remote)
		if err != nil {
			return nil, fmt.Errorf("Foundry returned an invalid hosted-agent definition; preview cannot compare it")
		}
		secrets = append(secrets, remoteSecrets...)
	}
	if inputs.Description != nil {
		after["description"] = *inputs.Description
	}
	if err := previewProtocolPresence(after, inputs.ProtocolVersions); err != nil {
		return nil, previewConfigurationError()
	}
	if err := previewProtocolPresence(before, inputs.ProtocolVersions); err != nil {
		return nil, fmt.Errorf("Foundry returned an invalid protocol definition; preview cannot compare it")
	}
	if inputs.Declared != nil {
		for path := range after {
			if !inputs.Declared[path] {
				// Normalization-only defaults are neither additions nor removals.
				delete(after, path)
				delete(before, path)
			}
		}
	}
	if inputs.IgnoreImage {
		delete(before, previewImagePath)
		delete(after, previewImagePath)
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
			"Sensitive values are redacted; known configuration values are shown.",
			"Comparison is limited to metadata, protocols, resources, environment, model reference, " +
				"and container image intent.",
			"Deploy creates a new agent version even when configuration has no changes.",
		},
	}
	keys := maps.Clone(before)
	maps.Copy(keys, after)
	for _, path := range slices.Sorted(maps.Keys(keys)) {
		if slices.Contains(inputs.Unknown, path) {
			continue
		}
		oldValue, oldExists := before[path]
		newValue, newExists := after[path]
		if oldExists == newExists && reflect.DeepEqual(oldValue, newValue) {
			continue
		}
		operation := "update"
		if !oldExists {
			operation = "add"
		} else if !newExists {
			operation = "remove"
		}
		change := previewChange{
			Group: previewFieldGroup(path), Path: clean(path), Operation: operation,
		}
		if oldExists {
			change.Before = previewDisplayValue(path, oldValue, clean)
		}
		if newExists {
			change.After = previewDisplayValue(path, newValue, clean)
		}
		result.Changes = append(result.Changes, change)
	}
	containerChanged := existing == nil && inputs.ContainerImage != nil
	for _, change := range result.Changes {
		if change.Group == "containerImage" {
			containerChanged = true
		}
	}
	if containerChanged {
		result.ContainerImage = inputs.ContainerImage
	}
	for _, path := range inputs.Unknown {
		if !containerChanged && (path == "containerImage.build" || path == "containerImage.push") {
			continue
		}
		if previewFieldGroup(path) != "" {
			result.Unknown = append(result.Unknown, clean(path))
		}
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
			"Resolve missing configuration inputs or select an image deployment strategy to compare these fields.")
	}
	return previewResponse(result)
}

func previewProtocolPresence(state map[string]any, versions map[string]bool) error {
	raw, exists := state["definition.protocol_versions"]
	if !exists || versions == nil {
		return nil
	}
	records, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("invalid protocol records")
	}
	for _, record := range records {
		fields, ok := record.(map[string]any)
		if !ok {
			return fmt.Errorf("invalid protocol record")
		}
		protocol, ok := fields["protocol"].(string)
		if !ok {
			return fmt.Errorf("invalid protocol name")
		}
		if declared, tracked := versions[protocol]; tracked && !declared && fields["version"] == "" {
			delete(fields, "version")
		}
	}
	return nil
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
	} else if definition.ContainerConfiguration.Image == "" {
		return nil, nil, fmt.Errorf("invalid container configuration")
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
	for name, value := range definition.EnvironmentVariables {
		if name != "AZURE_AI_MODEL_DEPLOYMENT_NAME" {
			secrets = append(secrets, value)
		}
	}
	clone := *request
	clone.Definition = definition
	clone.AgentEndpoint = nil
	clone.AgentCard = nil
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
	for path := range result {
		if previewFieldGroup(path) == "" {
			delete(result, path)
		}
	}
	return result, secrets, nil
}

const previewRedactedValue = "[redacted]"

var (
	previewIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.:/\\-]+$`)
	previewQuantity   = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?(?:(?:K|M|G|T)i?)?$`)
	previewVersion    = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)*$`)
)

// previewDisplayValue is deny-by-default: new API fields must opt in before their
// values can reach either terminal or JSON output. Only tags opt in as free-form text.
func previewDisplayValue(path string, value any, clean func(string) string) any {
	if text, ok := value.(string); ok && text == "" && previewFieldGroup(path) != "" {
		return text
	}
	switch path {
	case "metadata.tags":
		if text, ok := value.(string); ok {
			// Lists use a string-valued wire field; decode only actual string lists.
			var tags []any
			if json.Unmarshal([]byte(text), &tags) == nil && tags != nil {
				for i, item := range tags {
					tag, ok := item.(string)
					if !ok {
						return clean(text)
					}
					tags[i] = clean(tag)
				}
				return tags
			}
			return clean(text)
		}
	case "definition.cpu", "definition.memory":
		if text, ok := value.(string); ok && previewQuantity.MatchString(text) {
			return text
		}
	case "metadata.enableVnextExperience":
		if value == "true" || value == "false" {
			return value
		}
	case "definition.protocol_versions.version":
		if text, ok := value.(string); ok && (text == "" || previewVersion.MatchString(text)) {
			return text
		}
	case "definition.protocol_versions.protocol":
		if text, ok := value.(string); ok && slices.Contains([]string{
			"responses", "invocations", "invocations_ws", "activity", "activity_protocol", "a2a", "mcp",
		}, text) {
			return text
		}
	case "name", "definition.environment_variables.AZURE_AI_MODEL_DEPLOYMENT_NAME",
		"definition.container_configuration.registry_connection_id":
		if text, ok := value.(string); ok && (text == "" || previewIdentifier.MatchString(text)) {
			return clean(text)
		}
	case previewImagePath:
		if text, ok := value.(string); ok {
			text = clean(text)
			if containerref.IsValid(text) || strings.Contains(text, "://") {
				return text
			}
		}
	}
	switch path {
	case "definition.protocol_versions":
		if items, ok := value.([]any); ok {
			result := make([]any, len(items))
			for i, item := range items {
				result[i] = previewDisplayValue(path, item, clean)
			}
			return result
		}
		if object, ok := value.(map[string]any); ok {
			result := make(map[string]any, len(object))
			for key, item := range object {
				result[key] = previewDisplayValue(path+"."+key, item, clean)
			}
			return result
		}
	}
	return previewRedactedValue
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
	case path == previewImagePath || path == "definition.container_configuration.registry_connection_id" ||
		path == "containerImage.build" || path == "containerImage.push":
		return "containerImage"
	case path == "name" || path == "description" || strings.HasPrefix(path, "metadata."):
		return "metadata"
	default:
		return ""
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
		message.WriteString("Known configuration matches; some preview inputs are unresolved.\n")
	default:
		message.WriteString("No deployment configuration changes.\n")
	}
	for _, group := range []struct{ key, label string }{
		{"metadata", "Metadata"}, {"protocols", "Protocols"}, {"resources", "Resources"},
		{"environmentVariables", "Environment variables"}, {"modelDeployment", "Model deployment"},
		{"containerImage", "Container image"},
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
			before, err := json.Marshal(change.Before)
			if err != nil {
				return fmt.Errorf("cannot encode deployment preview before value")
			}
			after, err := json.Marshal(change.After)
			if err != nil {
				return fmt.Errorf("cannot encode deployment preview after value")
			}
			fmt.Fprintf(&message, "    %s: %s: ", change.Operation, change.Path)
			switch change.Operation {
			case "add":
				fmt.Fprintf(&message, "%s\n", after)
			case "remove":
				fmt.Fprintf(&message, "%s -> (removed)\n", before)
			default:
				fmt.Fprintf(&message, "%s -> %s\n", before, after)
			}
		}
		if group.key == "containerImage" && result.ContainerImage != nil &&
			(result.ContainerImage.Build != nil || result.ContainerImage.Push != nil) {
			if !shown {
				fmt.Fprintf(&message, "  %s:\n", group.label)
			}
			for _, operation := range []struct {
				label string
				value *bool
			}{{"build", result.ContainerImage.Build}, {"push", result.ContainerImage.Push}} {
				if operation.value != nil {
					fmt.Fprintf(&message, "    %s: %t\n", operation.label, *operation.value)
				}
			}
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
